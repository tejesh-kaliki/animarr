package pipeline

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"animarr/internal/anilist"
	"animarr/internal/config"
	"animarr/internal/indexer"
	"animarr/internal/qbit"
	"animarr/internal/score"
	"animarr/internal/secret"
	"animarr/internal/settings"
	"animarr/internal/store"
)

type fakeIndex struct {
	recent []indexer.Release
	search []indexer.Release
}

func (f *fakeIndex) Name() string { return "fake" }
func (f *fakeIndex) Search(context.Context, indexer.Query) ([]indexer.Release, error) {
	return f.search, nil
}
func (f *fakeIndex) Recent(context.Context, string) ([]indexer.Release, error) {
	return f.recent, nil
}

type fakeQbit struct {
	added []qbit.AddRequest
	tor   qbit.Torrent
	files []qbit.File
	hash  string
}

func (f *fakeQbit) Test(context.Context) (string, error) { return "v-test", nil }
func (f *fakeQbit) Add(_ context.Context, req qbit.AddRequest) (string, error) {
	f.added = append(f.added, req)
	return f.hash, nil
}
func (f *fakeQbit) Get(_ context.Context, hash string) (qbit.Torrent, error) {
	if f.tor.Hash == "" || !strings.EqualFold(f.tor.Hash, hash) {
		return qbit.Torrent{}, qbit.ErrNotFound
	}
	return f.tor, nil
}
func (f *fakeQbit) Files(context.Context, string) ([]qbit.File, error) { return f.files, nil }

type fakeLib struct{ n int }

func (f *fakeLib) Enabled() bool                        { return true }
func (f *fakeLib) Test(context.Context) (string, error) { return "Jellyfin Test", nil }
func (f *fakeLib) Refresh(context.Context) error {
	f.n++
	return nil
}

func testCfg(root string) config.Config {
	return config.Config{
		LibraryDir:        filepath.Join(root, "lib"),
		DownloadsDir:      filepath.Join(root, "dl"),
		NyaaCategory:      "1_2",
		QBCategory:        "animarr",
		MatchThreshold:    0.82,
		AutomationEnabled: true,
		RSSWindow:         6 * time.Hour,
		SearchBackoff:     6 * time.Hour,
		BacklogPerPoll:    3,
		Score:             score.DefaultConfig(),
		Upgrade: score.Upgrade{
			Repack:   true,
			Window:   72 * time.Hour,
			MinDelta: 15,
		},
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "animarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestRSSGrabAndImport(t *testing.T) {
	root := t.TempDir()
	cfg := testCfg(root)
	st := openStore(t)
	ctx := context.Background()
	sh, err := st.UpsertShow(ctx, store.Show{
		AniListID:    7,
		TitleRomaji:  "Title",
		TitleEnglish: "Title",
		Format:       "TV",
		Status:       "FINISHED",
		Episodes:     12,
		Monitored:    true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	aired := time.Now().Add(-24 * time.Hour)
	if err := st.EnsureEpisode(ctx, sh.ID, 7, &aired, store.StateWanted); err != nil {
		t.Fatal(err)
	}

	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	idx := &fakeIndex{recent: []indexer.Release{{
		Title:    "[SubsPlease] Title - 07 (1080p) [DEADBEEF]",
		InfoHash: hash,
		Magnet:   "magnet:?xt=urn:btih:" + hash,
		Seeders:  20,
		Indexer:  "nyaa",
	}, {
		Title:    "[SubsPlease] Title - 01-12 (1080p) [BATCH]",
		InfoHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Seeders:  50,
	}}}
	qb := &fakeQbit{}
	lib := &fakeLib{}
	svc := &Service{
		Config:   cfg,
		Store:    st,
		Indexer:  idx,
		Qbit:     qb,
		Jellyfin: lib,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := svc.PollRSS(ctx); err != nil {
		t.Fatal(err)
	}
	if len(qb.added) != 1 {
		t.Fatalf("grabs: got %d %+v", len(qb.added), qb.added)
	}
	if !strings.Contains(qb.added[0].URL, hash) {
		t.Fatalf("magnet: %s", qb.added[0].URL)
	}
	if qb.added[0].Category != "animarr" || !strings.HasSuffix(qb.added[0].SavePath, "/animarr") && !strings.HasSuffix(qb.added[0].SavePath, string(filepath.Separator)+"animarr") {
		t.Fatalf("add request: %+v", qb.added[0])
	}

	save := cfg.SaveDir()
	if err := os.MkdirAll(save, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(save, "Title - 07.mkv")
	if err := os.WriteFile(src, []byte("video-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	qb.tor = qbit.Torrent{Hash: hash, State: "stalledUP", Progress: 1, SavePath: save, CompletionOn: time.Now().Unix()}
	qb.files = []qbit.File{{Name: "Title - 07.mkv", Size: 11, Progress: 1}}
	if err := svc.PollDownloads(ctx); err != nil {
		t.Fatal(err)
	}
	ep, err := st.GetEpisode(ctx, sh.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if ep.State != store.StateDownloaded {
		t.Fatalf("state %s", ep.State)
	}
	if !strings.Contains(ep.FilePath, "Season 01") || !strings.Contains(ep.FilePath, "S01E07.mkv") {
		t.Fatalf("path %s", ep.FilePath)
	}
	got, err := os.ReadFile(ep.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "video-bytes" {
		t.Fatalf("imported bytes %q", got)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("seed file removed: %v", err)
	}
	if lib.n != 1 {
		t.Fatalf("jellyfin refreshes %d", lib.n)
	}
}

func TestRSSSkipsBatchOnly(t *testing.T) {
	root := t.TempDir()
	st := openStore(t)
	ctx := context.Background()
	sh, err := st.UpsertShow(ctx, store.Show{
		AniListID: 8, TitleEnglish: "Batch Show", TitleRomaji: "Batch Show",
		Format: "TV", Episodes: 12, Monitored: true, Status: "FINISHED",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureEpisode(ctx, sh.ID, 1, nil, store.StateWanted); err != nil {
		t.Fatal(err)
	}
	qb := &fakeQbit{}
	svc := &Service{
		Config:  testCfg(root),
		Store:   st,
		Indexer: &fakeIndex{recent: []indexer.Release{{Title: "[Group] Batch Show - 01-12 [1080p]", InfoHash: "cccccccccccccccccccccccccccccccccccccccc", Seeders: 10}}},
		Qbit:    qb,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := svc.PollRSS(ctx); err != nil {
		t.Fatal(err)
	}
	if len(qb.added) != 0 {
		t.Fatalf("batch was grabbed: %+v", qb.added)
	}
}

func TestSyncPreservesMonitored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"MediaListCollection":{"lists":[
			{"status":"CURRENT","entries":[{"status":"CURRENT","media":{"id":1,"title":{"romaji":"Watching Show","english":"Watching Show"},"format":"TV","status":"FINISHED","episodes":2}}]},
			{"status":"PLANNING","entries":[{"status":"PLANNING","media":{"id":2,"title":{"romaji":"Plan Show","english":"Plan Show"},"format":"TV","status":"NOT_YET_RELEASED","episodes":1}}]}
		]}}}`)
	}))
	defer srv.Close()

	st := openStore(t)
	al := anilist.New("test")
	al.Endpoint = srv.URL
	al.HTTP = srv.Client()
	al.Backoff = time.Millisecond
	al.Pace = 0
	svc := &Service{
		Config:  config.Config{AniListUsername: "alice", MatchThreshold: 0.82, Score: score.DefaultConfig()},
		Store:   st,
		AniList: al,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ctx := context.Background()
	if err := svc.SyncAniList(ctx); err != nil {
		t.Fatal(err)
	}
	cur, err := st.GetShowByAniList(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := st.GetShowByAniList(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !cur.Monitored || !plan.Monitored {
		t.Fatalf("current %v planning %v", cur.Monitored, plan.Monitored)
	}
	eps, err := st.ListEpisodes(ctx, cur.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 2 {
		t.Fatalf("episodes %d", len(eps))
	}
	if err := st.SetMonitored(ctx, cur.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMonitored(ctx, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncAniList(ctx); err != nil {
		t.Fatal(err)
	}
	cur, err = st.GetShowByAniList(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = st.GetShowByAniList(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Monitored || plan.Monitored {
		t.Fatalf("sync re-enabled monitoring: current %v planning %v", cur.Monitored, plan.Monitored)
	}
}

func TestSearchNowNoRelease(t *testing.T) {
	root := t.TempDir()
	st := openStore(t)
	ctx := context.Background()
	sh, err := st.UpsertShow(ctx, store.Show{
		AniListID: 9, TitleEnglish: "Lonely", TitleRomaji: "Lonely",
		Format: "TV", Episodes: 1, Monitored: true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureEpisode(ctx, sh.ID, 1, nil, store.StateWanted); err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		Config:  testCfg(root),
		Store:   st,
		Indexer: &fakeIndex{},
		Qbit:    &fakeQbit{},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	err = svc.SearchEpisode(ctx, sh.ID, 1)
	if err == nil || !strings.Contains(err.Error(), "no matching") {
		t.Fatalf("got %v", err)
	}
}

func TestSyncSequelJoinsFranchise(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"MediaListCollection":{"lists":[{"status":"CURRENT","entries":[
			{"status":"CURRENT","media":{"id":10,"title":{"romaji":"Book One","english":"Book One"},"format":"TV","status":"FINISHED","episodes":4,"description":"A <i>girl</i> &amp; books","genres":["Fantasy"],"studios":{"nodes":[{"name":"Wit"}]},"startDate":{"year":2019},"relations":{"edges":[{"relationType":"SEQUEL","node":{"id":11,"type":"ANIME","format":"TV","title":{"romaji":"Book Two"}}}]}}},
			{"status":"CURRENT","media":{"id":11,"title":{"romaji":"Book Two","english":"Book Two"},"format":"TV","status":"FINISHED","episodes":3,"startDate":{"year":2020},"relations":{"edges":[{"relationType":"PREQUEL","node":{"id":10,"type":"ANIME","format":"TV","title":{"romaji":"Book One"}}}]}}}
		]}]}}}`)
	}))
	defer srv.Close()
	st := openStore(t)
	al := anilist.New("test")
	al.Endpoint = srv.URL
	al.HTTP = srv.Client()
	al.Pace = 0
	cfg := testCfg(t.TempDir())
	cfg.AniListUsername = "alice"
	cfg.LibraryDir = "/library"
	svc := &Service{Config: cfg, Store: st, AniList: al, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx := context.Background()
	if err := svc.SyncAniList(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := st.GetShowByAniList(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.GetShowByAniList(ctx, 11)
	if err != nil {
		t.Fatal(err)
	}
	if first.FranchiseID != 10 || first.SeasonNumber != 1 || second.FranchiseID != 10 || second.SeasonNumber != 2 {
		t.Fatalf("first %+v second %+v", first, second)
	}
	if first.Studio != "Wit" || len(first.Genres) != 1 || first.Genres[0] != "Fantasy" || !strings.Contains(first.Description, "girl") {
		t.Fatalf("metadata %+v", first)
	}
	dest, err := svc.libraryDest(ctx, cfg, second, 1, ".mkv")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/library", "Book One", "Season 02", "Book One - S02E01.mkv")
	if dest != want {
		t.Fatalf("dest %s", dest)
	}
}

func TestImportWritesNFO(t *testing.T) {
	root := t.TempDir()
	cfg := testCfg(root)
	cfg.NFOEnabled = true
	st := openStore(t)
	ctx := context.Background()
	sh, err := st.UpsertShow(ctx, store.Show{
		AniListID: 7, TitleRomaji: "Title", TitleEnglish: "Title", Format: "TV",
		Status: "FINISHED", Episodes: 12, Monitored: true, Description: "Plot line",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetFranchise(ctx, sh.ID, 7, 1); err != nil {
		t.Fatal(err)
	}
	aired := time.Now().Add(-24 * time.Hour).UTC()
	if err := st.EnsureEpisode(ctx, sh.ID, 7, &aired, store.StateWanted); err != nil {
		t.Fatal(err)
	}
	const hash = "dddddddddddddddddddddddddddddddddddddddd"
	qb := &fakeQbit{}
	svc := &Service{
		Config: cfg, Store: st, Qbit: qb, Jellyfin: &fakeLib{},
		Indexer: &fakeIndex{recent: []indexer.Release{{
			Title: "[SubsPlease] Title - 07 (1080p)", InfoHash: hash, Magnet: "magnet:?xt=urn:btih:" + hash, Seeders: 5,
		}}},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := svc.PollRSS(ctx); err != nil {
		t.Fatal(err)
	}
	save := cfg.SaveDir()
	if err := os.MkdirAll(save, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(save, "Title - 07.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	qb.tor = qbit.Torrent{Hash: hash, State: "stalledUP", Progress: 1, SavePath: save, CompletionOn: time.Now().Unix()}
	qb.files = []qbit.File{{Name: "Title - 07.mkv", Size: 5, Progress: 1}}
	if err := svc.PollDownloads(ctx); err != nil {
		t.Fatal(err)
	}
	ep, err := st.GetEpisode(ctx, sh.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	series := filepath.Join(cfg.LibraryDir, "Title", "tvshow.nfo")
	body, err := os.ReadFile(series)
	if err != nil || !strings.Contains(string(body), ">7</uniqueid>") || !strings.Contains(string(body), "Plot line") {
		t.Fatalf("tvshow %s err %v", body, err)
	}
	epNFO := strings.TrimSuffix(ep.FilePath, ".mkv") + ".nfo"
	body, err = os.ReadFile(epNFO)
	if err != nil || !strings.Contains(string(body), "Episode 7") || !strings.Contains(string(body), aired.Format("2006-01-02")) {
		t.Fatalf("episode nfo %s err %v path %s", body, err, ep.FilePath)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(ep.FilePath), "season.nfo")); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListEncodes(ctx, 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("encode queued while ENCODE_ENABLED is off: %+v %v", rows, err)
	}
}

func TestImportQueuesEncodeWhenEnabled(t *testing.T) {
	root := t.TempDir()
	cfg := testCfg(root)
	cfg.EncodeEnabled = true
	cfg.NFOEnabled = false
	st := openStore(t)
	ctx := context.Background()
	sh, err := st.UpsertShow(ctx, store.Show{
		AniListID: 8, TitleRomaji: "Encode Show", TitleEnglish: "Encode Show", Format: "TV",
		Status: "FINISHED", Episodes: 1, Monitored: true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetFranchise(ctx, sh.ID, 8, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureEpisode(ctx, sh.ID, 1, nil, store.StateWanted); err != nil {
		t.Fatal(err)
	}
	const hash = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	qb := &fakeQbit{}
	lib := &fakeLib{}
	svc := &Service{
		Config: cfg, Store: st, Qbit: qb, Jellyfin: lib,
		Indexer: &fakeIndex{recent: []indexer.Release{{
			Title: "[SubsPlease] Encode Show - 01 (1080p)", InfoHash: hash, Magnet: "magnet:?xt=urn:btih:" + hash, Seeders: 5,
		}}},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := svc.PollRSS(ctx); err != nil {
		t.Fatal(err)
	}
	save := cfg.SaveDir()
	if err := os.MkdirAll(save, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(save, "Encode Show - 01.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	qb.tor = qbit.Torrent{Hash: hash, State: "stalledUP", Progress: 1, SavePath: save, CompletionOn: time.Now().Unix()}
	qb.files = []qbit.File{{Name: "Encode Show - 01.mkv", Size: 5, Progress: 1}}
	if err := svc.PollDownloads(ctx); err != nil {
		t.Fatal(err)
	}
	if lib.n != 1 {
		t.Fatalf("jellyfin refreshes %d", lib.n)
	}
	ep, err := st.GetEpisode(ctx, sh.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListEncodes(ctx, 10)
	if err != nil || len(rows) != 1 || rows[0].State != store.EncodePending || rows[0].FilePath != ep.FilePath {
		t.Fatalf("queue %+v err %v path %s", rows, err, ep.FilePath)
	}
	again, err := st.EnqueueEncode(ctx, ep.ID, ep.FilePath, time.Now())
	if err != nil || again {
		t.Fatalf("second enqueue %v %v", again, err)
	}
}

func TestGrabUsesHashFromClient(t *testing.T) {
	root := t.TempDir()
	st := openStore(t)
	ctx := context.Background()
	sh := mustShow(t, st, 21, "Hash Show")
	if err := st.EnsureEpisode(ctx, sh.ID, 1, nil, store.StateWanted); err != nil {
		t.Fatal(err)
	}
	const releaseHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const clientHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	qb := &fakeQbit{hash: clientHash}
	svc := &Service{
		Config: testCfg(root), Store: st, Qbit: qb,
		Indexer: &fakeIndex{recent: []indexer.Release{{
			Title: "[SubsPlease] Hash Show - 01 (1080p)", InfoHash: releaseHash,
			Magnet: "magnet:?xt=urn:btih:" + releaseHash, Seeders: 8,
		}}},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := svc.PollRSS(ctx); err != nil {
		t.Fatal(err)
	}
	ep, err := st.GetEpisode(ctx, sh.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ep.InfoHash != clientHash {
		t.Fatalf("infohash %s", ep.InfoHash)
	}
}

func TestSkipGrabWhenFileRecordedOrTorrentExists(t *testing.T) {
	root := t.TempDir()
	st := openStore(t)
	ctx := context.Background()
	sh := mustShow(t, st, 22, "Owned Show")
	if err := st.EnsureEpisode(ctx, sh.ID, 3, nil, store.StateWanted); err != nil {
		t.Fatal(err)
	}
	ep, err := st.GetEpisode(ctx, sh.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	ep.FilePath = filepath.Join(root, "already.mkv")
	ep.State = store.StateWanted
	if err := st.UpdateEpisode(ctx, ep); err != nil {
		t.Fatal(err)
	}
	const hash = "cccccccccccccccccccccccccccccccccccccccc"
	qb := &fakeQbit{}
	svc := &Service{
		Config: testCfg(root), Store: st, Qbit: qb,
		Indexer: &fakeIndex{recent: []indexer.Release{{
			Title: "[SubsPlease] Owned Show - 03 (1080p)", InfoHash: hash,
			Magnet: "magnet:?xt=urn:btih:" + hash, Seeders: 4,
		}}},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := svc.PollRSS(ctx); err != nil {
		t.Fatal(err)
	}
	if len(qb.added) != 0 {
		t.Fatalf("grabbed an episode that already has a file: %+v", qb.added)
	}

	ep.FilePath = ""
	if err := st.UpdateEpisode(ctx, ep); err != nil {
		t.Fatal(err)
	}
	qb.tor = qbit.Torrent{Hash: hash, State: "downloading", Progress: 0.2}
	if err := svc.PollRSS(ctx); err != nil {
		t.Fatal(err)
	}
	if len(qb.added) != 0 {
		t.Fatalf("grabbed an infohash qBittorrent already has: %+v", qb.added)
	}
	got, err := st.GetEpisode(ctx, sh.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.StateWanted {
		t.Fatalf("state %s", got.State)
	}
}

func TestAutomationOffBlocksSideEffectsUntilEnabled(t *testing.T) {
	root := t.TempDir()
	cfg := testCfg(root)
	cfg.DataPath = filepath.Join(root, "config")
	cfg.AutomationEnabled = false
	st := openStore(t)
	ctx := context.Background()
	sh := mustShow(t, st, 23, "Guarded Show")
	if err := st.EnsureEpisode(ctx, sh.ID, 7, nil, store.StateDownloading); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureEpisode(ctx, sh.ID, 8, nil, store.StateWanted); err != nil {
		t.Fatal(err)
	}
	const importHash = "dddddddddddddddddddddddddddddddddddddddd"
	const grabHash = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if _, err := st.AddDownload(ctx, store.Download{
		ShowID: sh.ID, Episode: 7, InfoHash: importHash, Title: "Guarded Show - 07",
		SavePath: cfg.SaveDir(), State: store.DlDownloading,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.SaveDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.SaveDir(), "Guarded Show - 07.mkv"), []byte("video-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	innerQ := &fakeQbit{}
	innerI := &fakeIndex{recent: []indexer.Release{{
		Title: "[SubsPlease] Guarded Show - 08 (1080p)", InfoHash: grabHash,
		Magnet: "magnet:?xt=urn:btih:" + grabHash, Seeders: 9,
	}}}
	qb := &trapQbit{t: t, inner: innerQ}
	idx := &trapIndex{t: t, inner: innerI}
	lib := &trapLib{t: t}
	prov := settings.New(cfg, nil, st, secret.Loaded{Box: secret.NewBox([32]byte{9}), Source: "test"})
	svc := &Service{
		Config: cfg, Settings: prov, Store: st,
		Indexer: idx, Qbit: qb, Jellyfin: lib,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := svc.PollRSS(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.PollDownloads(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.SearchEpisode(ctx, sh.ID, 8); err == nil || err.Error() != ErrAutomationOff.Error() {
		t.Fatalf("search: %v", err)
	}
	if err := svc.Redownload(ctx, sh.ID, 8); err == nil || err.Error() != "Automation is off: enable it in Settings" {
		t.Fatalf("redownload: %v", err)
	}
	assertNoLibraryFiles(t, cfg.LibraryDir)
	if len(innerQ.added) != 0 || lib.n != 0 || idx.hits != 0 {
		t.Fatalf("side effects while off: adds %d jellyfin %d nyaa %d", len(innerQ.added), lib.n, idx.hits)
	}

	if err := prov.Save(ctx, url.Values{config.KeyAutomationEnabled: {"true"}}); err != nil {
		t.Fatal(err)
	}
	cur, err := prov.Current(ctx)
	if err != nil || !cur.AutomationEnabled {
		t.Fatalf("enable %+v %v", cur.AutomationEnabled, err)
	}
	qb.allow, idx.allow, lib.allow = true, true, true
	if err := svc.PollRSS(ctx); err != nil {
		t.Fatal(err)
	}
	if len(innerQ.added) != 1 {
		t.Fatalf("next rss cycle adds %d", len(innerQ.added))
	}
	if idx.hits == 0 {
		t.Fatal("next rss cycle did not search")
	}
	innerQ.tor = qbit.Torrent{Hash: importHash, State: "stalledUP", Progress: 1, SavePath: cfg.SaveDir(), CompletionOn: time.Now().Unix()}
	innerQ.files = []qbit.File{{Name: "Guarded Show - 07.mkv", Size: 11, Progress: 1}}
	if err := svc.PollDownloads(ctx); err != nil {
		t.Fatal(err)
	}
	if lib.n != 1 {
		t.Fatalf("jellyfin refreshes %d", lib.n)
	}
	ep, err := st.GetEpisode(ctx, sh.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(ep.FilePath)
	if err != nil || string(body) != "video-bytes" {
		t.Fatalf("import %q %v path %s", body, err, ep.FilePath)
	}
}

type trapQbit struct {
	t     *testing.T
	allow bool
	inner *fakeQbit
}

func (q *trapQbit) refuse(op string) {
	if !q.allow {
		q.t.Fatalf("qBittorrent %s called while automation is off", op)
	}
}
func (q *trapQbit) Test(ctx context.Context) (string, error) {
	q.refuse("Test")
	return q.inner.Test(ctx)
}
func (q *trapQbit) Add(ctx context.Context, req qbit.AddRequest) (string, error) {
	q.refuse("Add")
	return q.inner.Add(ctx, req)
}
func (q *trapQbit) Get(ctx context.Context, hash string) (qbit.Torrent, error) {
	q.refuse("Get")
	return q.inner.Get(ctx, hash)
}
func (q *trapQbit) Files(ctx context.Context, hash string) ([]qbit.File, error) {
	q.refuse("Files")
	return q.inner.Files(ctx, hash)
}

type trapIndex struct {
	t     *testing.T
	allow bool
	hits  int
	inner *fakeIndex
}

func (f *trapIndex) Name() string { return "trap" }
func (f *trapIndex) Search(ctx context.Context, q indexer.Query) ([]indexer.Release, error) {
	if !f.allow {
		f.t.Fatal("Nyaa search called while automation is off")
	}
	f.hits++
	return f.inner.Search(ctx, q)
}
func (f *trapIndex) Recent(ctx context.Context, category string) ([]indexer.Release, error) {
	if !f.allow {
		f.t.Fatal("Nyaa RSS called while automation is off")
	}
	f.hits++
	return f.inner.Recent(ctx, category)
}

type trapLib struct {
	t     *testing.T
	allow bool
	n     int
}

func (f *trapLib) Enabled() bool { return true }
func (f *trapLib) Test(context.Context) (string, error) {
	if !f.allow {
		f.t.Fatal("Jellyfin test called while automation is off")
	}
	return "ok", nil
}
func (f *trapLib) Refresh(context.Context) error {
	if !f.allow {
		f.t.Fatal("Jellyfin refresh called while automation is off")
	}
	f.n++
	return nil
}

func mustShow(t *testing.T, st *store.Store, id int, title string) store.Show {
	t.Helper()
	sh, err := st.UpsertShow(context.Background(), store.Show{
		AniListID: id, TitleEnglish: title, TitleRomaji: title,
		Format: "TV", Status: "FINISHED", Episodes: 12, Monitored: true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	return sh
}

func assertNoLibraryFiles(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if path != dir && !d.IsDir() {
			t.Fatalf("library file written while automation is off: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
