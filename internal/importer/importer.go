// Package importer hardlinks a finished torrent into a Jellyfin library layout.
// The download stays in place so the torrent can keep seeding.
package importer

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"animarr/internal/parser"
)

// link is replaced in tests to force the copy fallback.
var link = os.Link

// copyData is io.Copy, replaced in tests to fail mid-copy.
var copyData = io.Copy

var (
	copyWarn         sync.Once
	lastImportCopied atomic.Bool
)

// LastImportCopied reports whether the most recent successful import in this
// process copied the file because a hardlink was not possible.
func LastImportCopied() bool { return lastImportCopied.Load() }

// NoteImportMethod records how the latest successful import placed a file.
// "copy" raises the settings-page warning and logs once per process.
func NoteImportMethod(method string) {
	switch method {
	case "copy":
		lastImportCopied.Store(true)
		copyWarn.Do(func() {
			slog.Warn("import copied a file because the hardlink failed; the download stays for seeding, so this uses about twice the disk. exFAT does not support hardlinks")
		})
	default:
		lastImportCopied.Store(false)
	}
}

// Show is the naming input.
// A movie that is not Special uses `<Title> (Year)/<Title> (Year).ext`.
// Special is Season 00. Season <= 0 on a series means Season 01, which keeps
// single-entry shows on the old path.
type Show struct {
	Title   string
	Year    int
	Format  string
	Season  int
	Special bool
}

// File is a torrent file. Name may include directories.
type File struct {
	Name string
	Size int64
}

// DestPath builds the library path for one episode or movie.
func DestPath(library string, show Show, episode int, ext string) string {
	title := Sanitize(show.Title)
	if title == "Unknown" {
		title = "Unknown"
	}
	if ext == "" {
		ext = ".mkv"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	if strings.EqualFold(show.Format, "MOVIE") && !show.Special {
		folder := title
		if show.Year > 0 {
			folder = fmt.Sprintf("%s (%d)", title, show.Year)
		}
		return filepath.Join(library, folder, folder+ext)
	}
	sn := show.Season
	if show.Special {
		sn = 0
	} else if sn <= 0 {
		sn = 1
	}
	file := fmt.Sprintf("%s - S%02dE%02d%s", title, sn, episode, ext)
	return filepath.Join(library, title, fmt.Sprintf("Season %02d", sn), file)
}

// Sanitize makes a single path segment safe for Jellyfin and common filesystems.
func Sanitize(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\r', '\t':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	clean := strings.Join(strings.Fields(b.String()), " ")
	clean = strings.TrimRight(clean, ". ")
	if utf8.RuneCountInString(clean) > 120 {
		rs := []rune(clean)[:120]
		clean = strings.TrimRight(string(rs), ". ")
	}
	if clean == "" || clean == "." || clean == ".." {
		return "Unknown"
	}
	return clean
}

// PickVideo chooses the episode file from a multi-file torrent.
// Sample files are ignored. A name that parses to the episode wins; otherwise the largest video.
func PickVideo(files []File, episode int) (File, bool) {
	var videos []File
	for _, f := range files {
		base := filepath.Base(f.Name)
		low := strings.ToLower(base)
		if strings.Contains(low, "sample") {
			continue
		}
		switch strings.ToLower(filepath.Ext(base)) {
		case ".mkv", ".mp4", ".m4v", ".avi":
			videos = append(videos, f)
		}
	}
	if len(videos) == 0 {
		return File{}, false
	}
	if len(videos) == 1 {
		return videos[0], true
	}
	if episode > 0 {
		for _, f := range videos {
			p := parser.Parse(filepath.Base(f.Name))
			if !p.Batch && p.Episode == episode {
				return f, true
			}
		}
	}
	best := videos[0]
	for _, f := range videos[1:] {
		if f.Size > best.Size {
			best = f
		}
	}
	return best, true
}

// ImportFile hardlinks src to dst, or copies when the link fails.
// An existing destination is replaced. method is "hardlink" or "copy".
// A copy is written to a temporary name in the destination folder and renamed
// into place when it finishes, so a crash mid-copy does not leave a partial
// file under the final name.
func ImportFile(src, dst string) (string, error) {
	if src == "" || dst == "" {
		return "", fmt.Errorf("import path is empty")
	}
	if filepath.Clean(src) == filepath.Clean(dst) {
		NoteImportMethod("same")
		return "same", nil
	}
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("source: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := link(src, dst); err == nil {
		NoteImportMethod("hardlink")
		return "hardlink", nil
	} else {
		linkErr := err
		if err := copyFile(src, dst); err != nil {
			return "", fmt.Errorf("hardlink: %v; copy: %w", linkErr, err)
		}
		NoteImportMethod("copy")
		return "copy", nil
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".animarr-partial-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	done := false
	defer func() {
		if !done {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := copyData(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}
	done = true
	return nil
}
