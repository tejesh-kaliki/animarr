package importer

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDestPath(t *testing.T) {
	got := DestPath("/data/media/anime", Show{Title: "Frieren: Beyond Journey's End", Format: "TV"}, 7, ".mkv")
	want := filepath.Join("/data/media/anime", "Frieren Beyond Journey's End", "Season 01", "Frieren Beyond Journey's End - S01E07.mkv")
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	s2 := DestPath("/data/media/anime", Show{Title: "Ascendance of a Bookworm", Format: "TV", Season: 2}, 1, ".mkv")
	wantS2 := filepath.Join("/data/media/anime", "Ascendance of a Bookworm", "Season 02", "Ascendance of a Bookworm - S02E01.mkv")
	if s2 != wantS2 {
		t.Fatalf("got %s", s2)
	}
	special := DestPath("/data/media/anime", Show{Title: "Ascendance of a Bookworm", Format: "OVA", Special: true}, 2, ".mkv")
	wantSpecial := filepath.Join("/data/media/anime", "Ascendance of a Bookworm", "Season 00", "Ascendance of a Bookworm - S00E02.mkv")
	if special != wantSpecial {
		t.Fatalf("got %s", special)
	}
	movie := DestPath("/data/media/anime", Show{Title: "Spirited Away", Year: 2001, Format: "MOVIE"}, 1, ".mkv")
	wantMovie := filepath.Join("/data/media/anime", "Spirited Away (2001)", "Spirited Away (2001).mkv")
	if movie != wantMovie {
		t.Fatalf("got %s", movie)
	}
	if Sanitize(`bad/name:*.`) != "bad name" {
		t.Fatalf("%q", Sanitize(`bad/name:*.`))
	}
}

func TestPickVideo(t *testing.T) {
	files := []File{
		{Name: "Show/Sample.mkv", Size: 99999},
		{Name: "Show/Title - 08.mkv", Size: 50},
		{Name: "Show/Title - 07.mkv", Size: 10},
		{Name: "Show/notes.txt", Size: 1},
	}
	got, ok := PickVideo(files, 7)
	if !ok || got.Name != "Show/Title - 07.mkv" {
		t.Fatalf("%+v %v", got, ok)
	}
	one, ok := PickVideo([]File{{Name: "only.mp4", Size: 3}}, 1)
	if !ok || one.Name != "only.mp4" {
		t.Fatal(one)
	}
	largest, ok := PickVideo([]File{{Name: "a.mkv", Size: 1}, {Name: "b.mkv", Size: 9}}, 0)
	if !ok || largest.Name != "b.mkv" {
		t.Fatal(largest)
	}
}

func TestHardlinkAndCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	if err := os.WriteFile(src, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "Show", "Season 01", "Show - S01E07.mkv")
	method, err := ImportFile(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if method != "hardlink" {
		t.Fatalf("method %s", method)
	}
	a, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(a, b) {
		t.Fatal("expected the same inode")
	}

	orig := link
	link = func(string, string) error { return errors.New("cross device") }
	t.Cleanup(func() { link = orig })
	dst2 := filepath.Join(dir, "copy", "out.mkv")
	method, err = ImportFile(src, dst2)
	if err != nil {
		t.Fatal(err)
	}
	if method != "copy" {
		t.Fatalf("method %s", method)
	}
	b2, err := os.ReadFile(dst2)
	if err != nil || string(b2) != "video" {
		t.Fatalf("copy %q %v", b2, err)
	}
	entries, err := os.ReadDir(filepath.Dir(dst2))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".animarr-partial-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	if !LastImportCopied() {
		t.Fatal("expected the copy fallback to be recorded")
	}
}

func TestCopyIsAtomicAndWarnsOnce(t *testing.T) {
	copyWarn = sync.Once{}
	lastImportCopied.Store(false)
	t.Cleanup(func() {
		copyWarn = sync.Once{}
		lastImportCopied.Store(false)
	})
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	if err := os.WriteFile(src, []byte("video-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	origLink := link
	link = func(string, string) error { return errors.New("exfat") }
	t.Cleanup(func() { link = origLink })

	origCopy := copyData
	t.Cleanup(func() { copyData = origCopy })
	copyData = func(w io.Writer, r io.Reader) (int64, error) {
		n, _ := w.Write([]byte("partial"))
		return int64(n), errors.New("stopped")
	}
	dst := filepath.Join(dir, "lib", "Show - S01E01.mkv")
	if _, err := ImportFile(src, dst); err == nil {
		t.Fatal("expected a failed copy")
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial file kept the final name: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "lib"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp files left behind: %v", names(entries))
	}

	copyData = origCopy
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	dst2 := filepath.Join(dir, "lib", "Show - S01E02.mkv")
	dst3 := filepath.Join(dir, "lib", "Show - S01E03.mkv")
	if _, err := ImportFile(src, dst2); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportFile(src, dst3); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), "hardlink failed") != 1 {
		t.Fatalf("warnings:\n%s", buf.String())
	}
	if !LastImportCopied() {
		t.Fatal("last import should be a copy")
	}
	link = origLink
	dst4 := filepath.Join(dir, "lib", "Show - S01E04.mkv")
	method, err := ImportFile(src, dst4)
	if err != nil || method != "hardlink" {
		t.Fatalf("%s %v", method, err)
	}
	if LastImportCopied() {
		t.Fatal("a later hardlink should clear the copy note")
	}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name()
	}
	return out
}
