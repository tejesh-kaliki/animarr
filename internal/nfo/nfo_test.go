package nfo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWriteAndPoster(t *testing.T) {
	dir := t.TempDir()
	series := filepath.Join(dir, "Bookworm")
	if err := WriteSeries(series, Series{
		Title: "Ascendance of a Bookworm", Original: "Honzuki", Plot: "A <i>girl</i> &amp; books",
		Year: 2019, Premiered: "2019-10-03", Genres: []string{"Fantasy"}, Studio: "Ajia-do", AniListID: 108268,
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(series, "tvshow.nfo"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, `<uniqueid type="anilist" default="true">108268</uniqueid>`) {
		t.Fatalf("uniqueid missing\n%s", text)
	}
	if !strings.Contains(text, "girl & books") && !strings.Contains(text, "girl &amp; books") {
		t.Fatalf("plot %s", text)
	}
	seasonDir := filepath.Join(series, "Season 02")
	if err := WriteSeason(seasonDir, Season{Number: 2, Title: "Part 2", AniListID: 222, Year: 2020}); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(seasonDir, "Bookworm - S02E01.mkv")
	if err := os.WriteFile(video, []byte("v"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteEpisode(Episode{
		VideoPath: video, Season: 2, Number: 1, Aired: time.Date(2020, 4, 5, 0, 0, 0, 0, time.UTC), AniListID: 222,
	}); err != nil {
		t.Fatal(err)
	}
	ep, err := os.ReadFile(strings.TrimSuffix(video, ".mkv") + ".nfo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ep), "<title>Episode 1</title>") || !strings.Contains(string(ep), "<aired>2020-04-05</aired>") {
		t.Fatalf("%s", ep)
	}

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg-bytes"))
	}))
	defer srv.Close()
	if err := EnsurePoster(context.Background(), srv.Client(), series, srv.URL+"/p.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePoster(context.Background(), srv.Client(), series, srv.URL+"/p.jpg"); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("poster downloaded %d times", hits.Load())
	}
	if info, err := os.Stat(filepath.Join(series, "folder.jpg")); err != nil || info.Size() == 0 {
		t.Fatal(err)
	}
}
