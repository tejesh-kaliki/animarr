// Package nfo writes Jellyfin/Kodi XML next to a library folder.
// Writing the same files again replaces them. A poster that is already on disk is left alone.
package nfo

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Series is tvshow.nfo plus the series poster.
type Series struct {
	Title     string
	Original  string
	Plot      string
	Year      int
	Premiered string
	Genres    []string
	Studio    string
	AniListID int
	PosterURL string
}

// Season is season.nfo inside a Season NN folder.
type Season struct {
	Number    int
	Title     string
	Plot      string
	Year      int
	AniListID int
}

// Episode is the .nfo beside one video file.
type Episode struct {
	VideoPath string
	Season    int
	Number    int
	Title     string
	Plot      string
	Aired     time.Time
	AniListID int
}

type uniqueID struct {
	Type    string `xml:"type,attr"`
	Default string `xml:"default,attr,omitempty"`
	Value   int    `xml:",chardata"`
}

// WriteMovie writes movie.nfo for a standalone movie folder.
func WriteMovie(dir string, s Series) error {
	return writeMovie(filepath.Join(dir, "movie.nfo"), s)
}

// WriteMovieFile writes <video>.nfo beside a movie file.
func WriteMovieFile(videoPath string, s Series) error {
	if videoPath == "" {
		return fmt.Errorf("movie path is empty")
	}
	path := strings.TrimSuffix(videoPath, filepath.Ext(videoPath)) + ".nfo"
	return writeMovie(path, s)
}

func writeMovie(path string, s Series) error {
	type doc struct {
		XMLName   xml.Name `xml:"movie"`
		Title     string   `xml:"title"`
		Original  string   `xml:"originaltitle,omitempty"`
		Plot      string   `xml:"plot,omitempty"`
		Year      int      `xml:"year,omitempty"`
		Premiered string   `xml:"premiered,omitempty"`
		Genres    []string `xml:"genre,omitempty"`
		Studio    string   `xml:"studio,omitempty"`
		UniqueID  uniqueID `xml:"uniqueid"`
	}
	return writeXML(path, doc{
		Title: s.Title, Original: s.Original, Plot: Plain(s.Plot), Year: s.Year, Premiered: s.Premiered,
		Genres: s.Genres, Studio: s.Studio,
		UniqueID: uniqueID{Type: "anilist", Default: "true", Value: s.AniListID},
	})
}

// WriteSeries writes tvshow.nfo in dir.
func WriteSeries(dir string, s Series) error {
	type doc struct {
		XMLName   xml.Name `xml:"tvshow"`
		Title     string   `xml:"title"`
		Original  string   `xml:"originaltitle,omitempty"`
		Plot      string   `xml:"plot,omitempty"`
		Year      int      `xml:"year,omitempty"`
		Premiered string   `xml:"premiered,omitempty"`
		Genres    []string `xml:"genre,omitempty"`
		Studio    string   `xml:"studio,omitempty"`
		UniqueID  uniqueID `xml:"uniqueid"`
	}
	return writeXML(filepath.Join(dir, "tvshow.nfo"), doc{
		Title: s.Title, Original: s.Original, Plot: Plain(s.Plot), Year: s.Year, Premiered: s.Premiered,
		Genres: s.Genres, Studio: s.Studio,
		UniqueID: uniqueID{Type: "anilist", Default: "true", Value: s.AniListID},
	})
}

// WriteSeason writes season.nfo in dir.
func WriteSeason(dir string, s Season) error {
	type doc struct {
		XMLName      xml.Name `xml:"season"`
		SeasonNumber int      `xml:"seasonnumber"`
		Title        string   `xml:"title,omitempty"`
		Plot         string   `xml:"plot,omitempty"`
		Year         int      `xml:"year,omitempty"`
		UniqueID     uniqueID `xml:"uniqueid"`
	}
	return writeXML(filepath.Join(dir, "season.nfo"), doc{
		SeasonNumber: s.Number, Title: s.Title, Plot: Plain(s.Plot), Year: s.Year,
		UniqueID: uniqueID{Type: "anilist", Default: "true", Value: s.AniListID},
	})
}

// WriteEpisode writes the nfo beside the video. Episode titles fall back to "Episode N".
func WriteEpisode(ep Episode) error {
	if ep.VideoPath == "" {
		return fmt.Errorf("episode path is empty")
	}
	title := strings.TrimSpace(ep.Title)
	if title == "" {
		title = fmt.Sprintf("Episode %d", ep.Number)
	}
	type doc struct {
		XMLName  xml.Name `xml:"episodedetails"`
		Title    string   `xml:"title"`
		Season   int      `xml:"season"`
		Episode  int      `xml:"episode"`
		Plot     string   `xml:"plot,omitempty"`
		Aired    string   `xml:"aired,omitempty"`
		UniqueID uniqueID `xml:"uniqueid"`
	}
	aired := ""
	if !ep.Aired.IsZero() {
		aired = ep.Aired.UTC().Format("2006-01-02")
	}
	path := strings.TrimSuffix(ep.VideoPath, filepath.Ext(ep.VideoPath)) + ".nfo"
	return writeXML(path, doc{
		Title: title, Season: ep.Season, Episode: ep.Number, Plot: Plain(ep.Plot), Aired: aired,
		UniqueID: uniqueID{Type: "anilist", Default: "true", Value: ep.AniListID},
	})
}

// SeasonPosterName is the Jellyfin filename for a season poster kept in the series folder.
func SeasonPosterName(season int) string {
	if season <= 0 {
		return "season-specials-poster.jpg"
	}
	return fmt.Sprintf("season%02d-poster.jpg", season)
}

// EnsureImage downloads rawURL to path unless a non-empty file is already there.
func EnsureImage(ctx context.Context, client *http.Client, path, rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if client == nil {
		client = http.DefaultClient
	}
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("image %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return fmt.Errorf("image was empty")
	}
	return os.WriteFile(path, body, 0o644)
}

// EnsurePoster downloads poster.jpg and folder.jpg when poster.jpg is missing or empty.
func EnsurePoster(ctx context.Context, client *http.Client, dir, rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}
	poster := filepath.Join(dir, "poster.jpg")
	folder := filepath.Join(dir, "folder.jpg")
	if info, err := os.Stat(poster); err == nil && info.Size() > 0 {
		if _, err := os.Stat(folder); err != nil {
			return copyFile(poster, folder)
		}
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if client == nil {
		client = http.DefaultClient
	}
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("poster %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return fmt.Errorf("poster was empty")
	}
	if err := os.WriteFile(poster, body, 0o644); err != nil {
		return err
	}
	return os.WriteFile(folder, body, 0o644)
}

var reTag = regexp.MustCompile(`<[^>]+>`)

// Plain strips a bit of HTML from an AniList description.
func Plain(s string) string {
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(s)
}

func writeXML(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(xml.Header)
	b.Write(body)
	b.WriteByte('\n')
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0o644)
}
