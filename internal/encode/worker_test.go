package encode

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeAPI struct {
	mu       sync.Mutex
	pending  []Job
	beats    []Heartbeat
	dones    []Report
	doneIDs  []int64
	fails    map[int64]string
	released []int64
}

func (f *fakeAPI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/encode/next", f.next)
	mux.HandleFunc("POST /api/encode/{id}/heartbeat", f.heartbeat)
	mux.HandleFunc("POST /api/encode/{id}/done", f.done)
	mux.HandleFunc("POST /api/encode/{id}/fail", f.fail)
	mux.HandleFunc("POST /api/encode/{id}/release", f.release)
	return mux
}

func (f *fakeAPI) next(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pending) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	job := f.pending[0]
	f.pending = f.pending[1:]
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(job)
}

func (f *fakeAPI) heartbeat(w http.ResponseWriter, r *http.Request) {
	var beat Heartbeat
	_ = json.NewDecoder(r.Body).Decode(&beat)
	f.mu.Lock()
	f.beats = append(f.beats, beat)
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeAPI) done(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	var rep Report
	_ = json.NewDecoder(r.Body).Decode(&rep)
	f.mu.Lock()
	f.dones = append(f.dones, rep)
	f.doneIDs = append(f.doneIDs, id)
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeAPI) fail(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	var body struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	if f.fails == nil {
		f.fails = map[int64]string{}
	}
	f.fails[id] = body.Error
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeAPI) release(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.released = append(f.released, pathID(r))
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

func testWorker(t *testing.T, api *fakeAPI, dir string) (*Worker, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	w := &Worker{
		Server: apiURL(t, api),
		Map:    PathMap{From: "/data/media/anime", To: dir},
		Once:   true,
		Log:    slog.New(slog.NewTextHandler(&buf, nil)),
		probe: func(context.Context, string) (Probe, error) {
			t.Fatal("probe not set")
			return Probe{}, nil
		},
		encode: func(context.Context, string, string, EncodeOptions, time.Duration, func(Progress)) error {
			t.Fatal("encode not set")
			return nil
		},
	}
	return w, &buf
}

func apiURL(t *testing.T, api *fakeAPI) string {
	t.Helper()
	ts := httptest.NewServer(api.handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func writeSrc(t *testing.T, dir, rel string) string {
	t.Helper()
	host := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(host), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(host, []byte("source-video"), 0o644); err != nil {
		t.Fatal(err)
	}
	return host
}

func TestDryRunHoldsAndReleases(t *testing.T) {
	dir := t.TempDir()
	writeSrc(t, dir, "Demo/a.mp4")
	writeSrc(t, dir, "Demo/b.mp4")
	api := &fakeAPI{pending: []Job{
		{ID: 1, EpisodeID: 10, FilePath: "/data/media/anime/Demo/a.mp4", LeaseSeconds: 120},
		{ID: 2, EpisodeID: 11, FilePath: "/data/media/anime/Demo/b.mp4", LeaseSeconds: 120},
	}}
	w, _ := testWorker(t, api, dir)
	w.Once = false
	w.DryRun = true
	encoded := 0
	w.probe = func(context.Context, string) (Probe, error) {
		return Probe{HasVideo: true, VideoCodec: "h264", Duration: time.Minute, Size: 4 << 20, BitDepth: 8}, nil
	}
	w.encode = func(context.Context, string, string, EncodeOptions, time.Duration, func(Progress)) error {
		encoded++
		return nil
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if encoded != 0 {
		t.Fatalf("dry-run encoded %d", encoded)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.dones) != 0 || len(api.fails) != 0 {
		t.Fatalf("dry-run reported dones %d fails %d", len(api.dones), len(api.fails))
	}
	if len(api.beats) != 2 {
		t.Fatalf("heartbeats %d", len(api.beats))
	}
	if len(api.released) != 2 || len(api.pending) != 0 {
		t.Fatalf("released %v pending %d", api.released, len(api.pending))
	}
}

func TestWorkerEncodesAndLogs(t *testing.T) {
	dir := t.TempDir()
	host := writeSrc(t, dir, "Demo/Season 01/Demo - S01E01.mp4")
	api := &fakeAPI{pending: []Job{{
		ID: 7, EpisodeID: 3, FilePath: "/data/media/anime/Demo/Season 01/Demo - S01E01.mp4", LeaseSeconds: 120,
	}}}
	w, buf := testWorker(t, api, dir)
	w.Encoder = "hevc_videotoolbox"
	w.probe = func(_ context.Context, path string) (Probe, error) {
		if strings.Contains(filepath.Base(path), "animarr-encode") {
			return Probe{HasVideo: true, VideoCodec: "hevc", Duration: time.Minute, Size: 3 << 20, BitDepth: 10}, nil
		}
		return Probe{HasVideo: true, VideoCodec: "h264", Duration: time.Minute, Size: 8 << 20, BitDepth: 10}, nil
	}
	w.encode = func(_ context.Context, _, dst string, opt EncodeOptions, _ time.Duration, on func(Progress)) error {
		if opt.BitDepth != 10 || opt.Encoder != "hevc_videotoolbox" {
			t.Errorf("options %+v", opt)
		}
		if err := os.WriteFile(dst, []byte("encoded-video-bytes"), 0o644); err != nil {
			return err
		}
		on(Progress{Percent: 40, FPS: 80, Speed: 2.3})
		return nil
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(host); !os.IsNotExist(err) {
		t.Fatalf("original still present: %v", err)
	}
	final := strings.TrimSuffix(host, ".mp4") + ".mkv"
	if _, err := os.Stat(final); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(TempPath(host)); !os.IsNotExist(err) {
		t.Fatalf("temp left behind: %v", err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.dones) != 1 || api.dones[0].Skipped || !strings.HasSuffix(api.dones[0].FilePath, ".mkv") {
		t.Fatalf("done %+v", api.dones)
	}
	if api.dones[0].SrcSize != 8<<20 || api.dones[0].DstSize != 3<<20 || api.dones[0].Speed != 2.3 {
		t.Fatalf("sizes %+v", api.dones[0])
	}
	if len(api.beats) == 0 || api.beats[0].Speed != 2.3 || api.beats[0].Percent != 40 {
		t.Fatalf("beats %+v", api.beats)
	}
	log := buf.String()
	for _, want := range []string{"hevc_videotoolbox", "2.3x", "saved=62.5%", "state=done"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
}

func TestWorkerSkipsEfficientAndKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	host := writeSrc(t, dir, "Demo/a.mkv")
	api := &fakeAPI{pending: []Job{{ID: 1, FilePath: "/data/media/anime/Demo/a.mkv", LeaseSeconds: 120}}}
	w, _ := testWorker(t, api, dir)
	w.probe = func(context.Context, string) (Probe, error) {
		return Probe{HasVideo: true, VideoCodec: "hevc", Duration: time.Minute, Size: 5 << 20}, nil
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(host); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.dones) != 1 || !api.dones[0].Skipped {
		t.Fatalf("done %+v", api.dones)
	}
	if api.dones[0].FilePath != "/data/media/anime/Demo/a.mkv" {
		t.Fatalf("path %s", api.dones[0].FilePath)
	}
}

func TestWorkerNotSmallerKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	host := writeSrc(t, dir, "Demo/a.mkv")
	api := &fakeAPI{pending: []Job{{ID: 4, FilePath: "/data/media/anime/Demo/a.mkv", LeaseSeconds: 120}}}
	w, _ := testWorker(t, api, dir)
	w.probe = func(_ context.Context, path string) (Probe, error) {
		if strings.Contains(filepath.Base(path), "animarr-encode") {
			return Probe{HasVideo: true, VideoCodec: "hevc", Duration: time.Minute, Size: 9 << 20}, nil
		}
		return Probe{HasVideo: true, VideoCodec: "h264", Duration: time.Minute, Size: 4 << 20}, nil
	}
	w.encode = func(_ context.Context, _, dst string, _ EncodeOptions, _ time.Duration, on func(Progress)) error {
		if err := os.WriteFile(dst, bytes.Repeat([]byte("x"), 64), 0o644); err != nil {
			return err
		}
		on(Progress{Speed: 1.5, Percent: 100})
		return nil
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(host); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(TempPath(host)); !os.IsNotExist(err) {
		t.Fatal("temp remains")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.dones) != 1 || !api.dones[0].Skipped || len(api.fails) != 0 {
		t.Fatalf("done %+v fails %+v", api.dones, api.fails)
	}
}

func TestWorkerCancelReleasesAndDeletesTemp(t *testing.T) {
	dir := t.TempDir()
	host := writeSrc(t, dir, "Demo/a.mkv")
	api := &fakeAPI{pending: []Job{{ID: 9, FilePath: "/data/media/anime/Demo/a.mkv", LeaseSeconds: 120}}}
	w, _ := testWorker(t, api, dir)
	w.Once = false
	started := make(chan struct{})
	w.probe = func(context.Context, string) (Probe, error) {
		return Probe{HasVideo: true, VideoCodec: "h264", Duration: time.Minute, Size: 4 << 20}, nil
	}
	w.encode = func(ctx context.Context, _, dst string, _ EncodeOptions, _ time.Duration, _ func(Progress)) error {
		if err := os.WriteFile(dst, []byte("partial"), 0o644); err != nil {
			return err
		}
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("encode did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
	if _, err := os.Stat(host); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(TempPath(host)); !os.IsNotExist(err) {
		t.Fatal("temp remains after cancel")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.released) != 1 || api.released[0] != 9 || len(api.dones) != 0 {
		t.Fatalf("released %v dones %d", api.released, len(api.dones))
	}
}

func TestWorkerMissingFileFails(t *testing.T) {
	dir := t.TempDir()
	api := &fakeAPI{pending: []Job{{ID: 3, FilePath: "/data/media/anime/Demo/missing.mkv", LeaseSeconds: 120}}}
	w, _ := testWorker(t, api, dir)
	w.probe = func(context.Context, string) (Probe, error) {
		t.Fatal("probed a missing file")
		return Probe{}, nil
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if !strings.Contains(api.fails[3], "drive mounted") {
		t.Fatalf("fail %q", api.fails[3])
	}
}

func TestWorkerOnceStopsAfterOne(t *testing.T) {
	dir := t.TempDir()
	writeSrc(t, dir, "Demo/a.mkv")
	writeSrc(t, dir, "Demo/b.mkv")
	api := &fakeAPI{pending: []Job{
		{ID: 1, FilePath: "/data/media/anime/Demo/a.mkv", LeaseSeconds: 120},
		{ID: 2, FilePath: "/data/media/anime/Demo/b.mkv", LeaseSeconds: 120},
	}}
	w, _ := testWorker(t, api, dir)
	w.probe = func(context.Context, string) (Probe, error) {
		return Probe{HasVideo: true, VideoCodec: "av1", Duration: time.Minute, Size: 1 << 20}, nil
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.dones) != 1 || len(api.pending) != 1 || api.pending[0].ID != 2 {
		t.Fatalf("dones %d pending %+v", len(api.dones), api.pending)
	}
}
