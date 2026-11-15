package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"animarr/internal/config"
	"animarr/internal/pipeline"
	"animarr/internal/score"
	"animarr/internal/store"
)

type countLib struct{ n int }

func (c *countLib) Enabled() bool                        { return true }
func (c *countLib) Test(context.Context) (string, error) { return "ok", nil }
func (c *countLib) Refresh(context.Context) error {
	c.n++
	return nil
}

func TestEncodeAPIAndPage(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "animarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	sh, err := st.UpsertShow(ctx, store.Show{
		AniListID: 9, TitleEnglish: "Encode Demo", TitleRomaji: "Encode Demo",
		Format: "TV", Status: "FINISHED", Episodes: 2, Monitored: true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	ep := seedDownloaded(t, st, sh.ID, 1, "/data/media/anime/Demo/Season 01/Demo - S01E01.mp4")
	cfg := config.Config{
		ListenAddr: ":8990", DataPath: dir,
		LibraryDir: filepath.Join(dir, "lib"), DownloadsDir: filepath.Join(dir, "dl"),
		MatchThreshold: 0.82, Score: score.DefaultConfig(), NyaaCategory: "1_2", QBCategory: "animarr",
	}
	lib := &countLib{}
	pipe := &pipeline.Service{
		Config: cfg, Store: st, Jellyfin: lib,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	srv, err := New(pipe, st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	if _, err := st.EnqueueEncode(ctx, ep.ID, ep.FilePath, time.Now()); err != nil {
		t.Fatal(err)
	}
	if code := statusOf(t, http.MethodGet, ts.URL+"/api/encode/next", nil); code != http.StatusNoContent {
		t.Fatalf("disabled next %d", code)
	}
	off := postFollow(t, ts.URL+"/encode/enqueue")
	if !strings.Contains(off, "Encode is off") {
		t.Fatalf("enqueue while off: %s", off)
	}

	srv.Config.EncodeEnabled = true
	pipe.Config.EncodeEnabled = true
	next := getJSON(t, ts.URL+"/api/encode/next")
	id, _ := next["id"].(float64)
	if id == 0 || next["filePath"] != ep.FilePath {
		t.Fatalf("next %+v", next)
	}
	if code := statusOf(t, http.MethodPost, ts.URL+"/api/encode/7/heartbeat", []byte(`{"encoder":"nope","percent":1}`)); code != http.StatusNotFound {
		t.Fatalf("missing heartbeat %d", code)
	}
	postJSON(t, ts.URL+"/api/encode/"+formatID(id)+"/heartbeat", map[string]any{
		"encoder": "hevc_videotoolbox", "percent": 42, "fps": 80, "speed": 2.3,
	})
	table := get(t, ts, "/encode/table")
	if !strings.Contains(table, "42%") || !strings.Contains(table, "80 fps") || !strings.Contains(table, "2.3x") || !strings.Contains(table, "running") {
		t.Fatalf("table: %s", table)
	}
	page := get(t, ts, "/encode")
	if !strings.Contains(page, "Encode") || !strings.Contains(page, "every 3s") || !strings.Contains(page, "running 1") {
		t.Fatalf("page: %s", page)
	}

	newPath := "/data/media/anime/Demo/Season 01/Demo - S01E01.mkv"
	postJSON(t, ts.URL+"/api/encode/"+formatID(id)+"/done", map[string]any{
		"filePath": newPath, "codec": "hevc", "encoder": "hevc_videotoolbox",
		"srcSize": 8000000, "dstSize": 3000000, "speed": 2.3,
	})
	if lib.n != 1 {
		t.Fatalf("jellyfin refreshes %d", lib.n)
	}
	got, err := st.GetEpisode(ctx, sh.ID, 1)
	if err != nil || got.FilePath != newPath {
		t.Fatalf("path %q %v", got.FilePath, err)
	}
	donePage := get(t, ts, "/encode")
	if !strings.Contains(donePage, "saved 62%") && !strings.Contains(donePage, "saved 63%") {
		t.Fatalf("saved percent: %s", donePage)
	}

	ep2 := seedDownloaded(t, st, sh.ID, 2, "/data/media/anime/Demo/Season 01/Demo - S01E02.mkv")
	if _, err := st.EnqueueEncode(ctx, ep2.ID, ep2.FilePath, time.Now()); err != nil {
		t.Fatal(err)
	}
	next = getJSON(t, ts.URL+"/api/encode/next")
	id2, _ := next["id"].(float64)
	postJSON(t, ts.URL+"/api/encode/"+formatID(id2)+"/done", map[string]any{
		"filePath": ep2.FilePath, "codec": "hevc", "skipped": true, "error": "already hevc",
	})
	if lib.n != 1 {
		t.Fatalf("skipped encode refreshed jellyfin: %d", lib.n)
	}
	got, err = st.GetEpisode(ctx, sh.ID, 2)
	if err != nil || got.FilePath != ep2.FilePath {
		t.Fatalf("skipped path %q %v", got.FilePath, err)
	}

	ep3 := seedDownloaded(t, st, sh.ID, 3, "/data/media/anime/Demo/Season 01/Demo - S01E03.mkv")
	if _, err := st.EnqueueEncode(ctx, ep3.ID, ep3.FilePath, time.Now()); err != nil {
		t.Fatal(err)
	}
	next = getJSON(t, ts.URL+"/api/encode/next")
	id3, _ := next["id"].(float64)
	postJSON(t, ts.URL+"/api/encode/"+formatID(id3)+"/fail", map[string]any{"error": "verification failed: output has no video stream"})
	failed, err := st.GetEncode(ctx, int64(id3))
	if err != nil || failed.State != store.EncodeFailed || !strings.Contains(failed.Error, "verification failed") {
		t.Fatalf("failed %+v %v", failed, err)
	}
	re := postFollow(t, ts.URL+"/encode/requeue")
	if !strings.Contains(re, "Requeued 1") {
		t.Fatalf("requeue: %s", re)
	}
	again, err := st.GetEncode(ctx, int64(id3))
	if err != nil || again.State != store.EncodePending || again.Error != "" {
		t.Fatalf("requeued %+v %v", again, err)
	}
}

func seedDownloaded(t *testing.T, st *store.Store, showID int64, number int, path string) store.Episode {
	t.Helper()
	ctx := context.Background()
	if err := st.EnsureEpisode(ctx, showID, number, nil, store.StateDownloaded); err != nil {
		t.Fatal(err)
	}
	ep, err := st.GetEpisode(ctx, showID, number)
	if err != nil {
		t.Fatal(err)
	}
	ep.State = store.StateDownloaded
	ep.FilePath = path
	if err := st.UpdateEpisode(ctx, ep); err != nil {
		t.Fatal(err)
	}
	return ep
}

func statusOf(t *testing.T, method, url string, body []byte) int {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	_, _ = io.ReadAll(res.Body)
	return res.StatusCode
}

func postFollow(t *testing.T, url string) string {
	t.Helper()
	res, err := http.Post(url, "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST %s %d %s", url, res.StatusCode, b)
	}
	return string(b)
}

func postJSON(t *testing.T, url string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("POST %s %d %s", url, res.StatusCode, out)
	}
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("GET %s %d %s", url, res.StatusCode, b)
	}
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func formatID(id float64) string {
	return strconv.FormatInt(int64(id), 10)
}
