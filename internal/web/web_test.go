package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"animarr/internal/config"
	"animarr/internal/importer"
	"animarr/internal/indexer"
	"animarr/internal/pipeline"
	"animarr/internal/score"
	"animarr/internal/store"
)

type fakeIndex struct{}

func (fakeIndex) Name() string { return "fake" }
func (fakeIndex) Search(context.Context, indexer.Query) ([]indexer.Release, error) {
	return nil, nil
}
func (fakeIndex) Recent(context.Context, string) ([]indexer.Release, error) { return nil, nil }

func TestPagesAndActions(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "animarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	sh, err := st.UpsertShow(ctx, store.Show{
		AniListID: 1, TitleEnglish: "Demo Show", TitleRomaji: "Demo Show",
		Format: "TV", Status: "FINISHED", Episodes: 1, Monitored: true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureEpisode(ctx, sh.ID, 1, nil, store.StateWanted); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		ListenAddr: ":8990", DataPath: dir,
		LibraryDir: filepath.Join(dir, "lib"), DownloadsDir: filepath.Join(dir, "dl"),
		MatchThreshold: 0.82, Score: score.DefaultConfig(), NyaaCategory: "1_2", QBCategory: "animarr",
		QBURL: "http://127.0.0.1:8080", JellyfinURL: "http://jellyfin:8096",
		AutomationEnabled: true,
	}
	pipe := &pipeline.Service{
		Config: cfg, Store: st, Indexer: fakeIndex{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	srv, err := New(pipe, st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := get(t, ts, "/")
	if !strings.Contains(body, "Dashboard") || !strings.Contains(body, ">Missing<") || !strings.Contains(body, `href="/shows?filter=monitored"`) {
		t.Fatalf("dashboard: %s", body)
	}
	if strings.Contains(body, "every 10s") {
		t.Fatalf("dashboard polls while nothing is downloading: %s", body)
	}
	if strings.Contains(body, "Automation is off") || strings.Contains(body, "automation-card") || strings.Contains(body, ">Wanted<") {
		t.Fatalf("dashboard automation state: %s", body)
	}
	body = get(t, ts, "/shows")
	if !strings.Contains(body, "Demo Show") || !strings.Contains(body, "0/1") || !strings.Contains(body, "1 missing") || !strings.Contains(body, "Finished") {
		t.Fatalf("shows: %s", body)
	}
	if strings.Contains(body, "FINISHED") {
		t.Fatalf("raw status on shows: %s", body)
	}
	body = get(t, ts, "/shows/"+strconv.FormatInt(sh.ID, 10))
	if !strings.Contains(body, "Search") || !strings.Contains(body, "wanted") || !strings.Contains(body, "1 aired") || strings.Contains(body, "<th>Release</th>") {
		t.Fatalf("detail: %s", body)
	}
	if !strings.Contains(body, "Finished") || !strings.Contains(body, "/static/icons.svg#circle") || strings.Contains(body, "FINISHED") || strings.Contains(body, ">Save<") {
		t.Fatalf("detail header: %s", body)
	}
	searchPage := get(t, ts, "/search")
	if !strings.Contains(searchPage, `for="q"`) || !strings.Contains(searchPage, "Search by English or romaji title") || strings.Contains(searchPage, "No matches.") {
		t.Fatalf("search empty: %s", searchPage)
	}
	icons := get(t, ts, "/static/icons.svg")
	if !strings.Contains(icons, `id="check"`) || !strings.Contains(icons, `id="info"`) || !strings.Contains(icons, `id="triangle"`) {
		t.Fatalf("icons: %s", icons)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/shows/"+strconv.FormatInt(sh.ID, 10)+"/episodes/1/search", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(b), "No matching release") {
		t.Fatalf("search %d %s", res.StatusCode, b)
	}

	res, err = client.PostForm(ts.URL+"/shows/"+strconv.FormatInt(sh.ID, 10)+"/monitored", url.Values{"monitored": {"0"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("monitor status %d", res.StatusCode)
	}
	got, err := st.GetShow(ctx, sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Monitored {
		t.Fatal("still monitored")
	}

	health, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := io.ReadAll(health.Body)
	health.Body.Close()
	if health.StatusCode != 200 || !strings.Contains(string(hb), "ok") {
		t.Fatalf("health %d %s", health.StatusCode, hb)
	}
	settings := get(t, ts, "/settings")
	if !strings.Contains(settings, "qBittorrent") || !strings.Contains(settings, "Not set") || !strings.Contains(settings, `role="switch"`) {
		t.Fatalf("settings: %s", settings)
	}
	if strings.Contains(settings, "Save empty") || strings.Contains(settings, ">Reset<") || strings.Contains(settings, "Clear it") || strings.Contains(settings, "Revert to") {
		t.Fatalf("settings still has the old reset row: %s", settings)
	}
	for _, want := range []string{`id="g-automation"`, `class="savebar"`, `Test connection`, `hx-target="#test-qbit"`, `name="SCORE_GROUP_ORDER"`, `rows="2"`, `placeholder="Enter a new value"`, `aria-label="Jump to section"`, `value="#g-startup"`} {
		if !strings.Contains(settings, want) {
			t.Fatalf("settings missing %s", want)
		}
	}
}

func TestActivityPlainSummary(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "animarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	sh, err := st.UpsertShow(ctx, store.Show{
		AniListID: 9, TitleEnglish: "Demo Show", TitleRomaji: "Demo Show",
		Format: "TV", Status: "FINISHED", Episodes: 1, Monitored: true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	epn := 1
	rows := []store.History{
		{At: now.Add(-3 * time.Minute), Level: "error", Message: `qBittorrent add failed: qbit add: {"failure_count":1,"success_count":0}`},
		{At: now.Add(-2 * time.Minute), Level: "info", Message: "synced 1 shows from AniList"},
		{At: now.Add(-time.Minute), Level: "warn", Message: "search failed: no index"},
		{At: now, Level: "error", ShowID: &sh.ID, Episode: &epn, Message: `qBittorrent add failed: qbit add: {"added_torrent_ids":["abc"],"failure_count":0,"success_count":1}`},
		{At: now.Add(time.Second), Level: "warn", ShowID: &sh.ID, Episode: &epn, Message: `search failed: qbit add: {"added_torrent_ids":["abc"],"failure_count":0,"success_count":1}`},
	}
	for _, row := range rows {
		if err := st.AddHistory(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{
		ListenAddr: ":8990", DataPath: dir,
		LibraryDir: filepath.Join(dir, "lib"), DownloadsDir: filepath.Join(dir, "dl"),
		MatchThreshold: 0.82, Score: score.DefaultConfig(), AutomationEnabled: true,
	}
	pipe := &pipeline.Service{
		Config: cfg, Store: st, Indexer: fakeIndex{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	srv, err := New(pipe, st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := get(t, ts, "/activity")
	if !strings.Contains(body, "qBittorrent accepted the torrent") || !strings.Contains(body, "<details") || !strings.Contains(body, "success_count") {
		t.Fatalf("activity: %s", body)
	}
	if !strings.Contains(body, "Demo Show") || !strings.Contains(body, "search failed: no index") || !strings.Contains(body, `datetime="`) {
		t.Fatalf("activity links: %s", body)
	}
	if strings.Contains(body, "search failed: qbit add") {
		t.Fatalf("stale search success still listed: %s", body)
	}
	idx := strings.Index(body, "qBittorrent accepted the torrent")
	details := strings.Index(body, "<details")
	if idx < 0 || details < 0 || idx > details {
		t.Fatalf("summary should precede the payload: %s", body)
	}
	problems := get(t, ts, "/activity?level=problems")
	if strings.Contains(problems, "qBittorrent accepted the torrent") || strings.Contains(problems, "synced 1 shows") || !strings.Contains(problems, "search failed") || !strings.Contains(problems, "failure_count") {
		t.Fatalf("problems: %s", problems)
	}
	grabs := get(t, ts, "/activity?level=grabs")
	if !strings.Contains(grabs, "qBittorrent accepted the torrent") || strings.Contains(grabs, "search failed") || strings.Contains(grabs, "add failed") {
		t.Fatalf("grabs: %s", grabs)
	}
	syncs := get(t, ts, "/activity?level=syncs")
	if !strings.Contains(syncs, "synced 1 shows from AniList") || strings.Contains(syncs, "search failed") || strings.Contains(syncs, "accepted the torrent") {
		t.Fatalf("syncs: %s", syncs)
	}
	dash := get(t, ts, "/")
	if !strings.Contains(dash, "Synced ") || !strings.Contains(dash, "search failed: no index") || strings.Contains(dash, "qBittorrent accepted the torrent") || strings.Contains(dash, "success_count") || strings.Contains(dash, "search failed: qbit add") {
		t.Fatalf("dashboard problems: %s", dash)
	}
	if !strings.Contains(dash, ">2</strong><span>Problems (24h)</span>") {
		t.Fatalf("problem count: %s", dash)
	}
}

func TestAutomationBannerAndCopyNote(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "animarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	sh, err := st.UpsertShow(ctx, store.Show{
		AniListID: 2, TitleEnglish: "Quiet Show", TitleRomaji: "Quiet Show",
		Format: "TV", Status: "FINISHED", Episodes: 1, Monitored: true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureEpisode(ctx, sh.ID, 1, nil, store.StateWanted); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		ListenAddr: ":8990", DataPath: dir, LibraryDir: filepath.Join(dir, "lib"),
		MatchThreshold: 0.82, Score: score.DefaultConfig(),
	}
	pipe := &pipeline.Service{
		Config: cfg, Store: st, Indexer: fakeIndex{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	srv, err := New(pipe, st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	for _, path := range []string{"/", "/shows", "/search", "/activity"} {
		body := get(t, ts, path)
		if !strings.Contains(body, "Automation is off") || !strings.Contains(body, `Turn on in Settings`) {
			t.Fatalf("%s missing banner: %s", path, body)
		}
	}
	settingsBanner := get(t, ts, "/settings")
	if !strings.Contains(settingsBanner, "Automation is off") || !strings.Contains(settingsBanner, `Turn it on below`) || !strings.Contains(settingsBanner, ">Off<") {
		t.Fatalf("settings banner: %s", settingsBanner)
	}
	dash := get(t, ts, "/")
	if strings.Contains(dash, "automation-card") || !strings.Contains(dash, ">Missing<") {
		t.Fatalf("dashboard: %s", dash)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/shows/"+strconv.FormatInt(sh.ID, 10)+"/episodes/1/search", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(string(b), "Automation is off: enable it in Settings") {
		t.Fatalf("search %d %s", res.StatusCode, b)
	}

	importer.NoteImportMethod("copy")
	t.Cleanup(func() { importer.NoteImportMethod("hardlink") })
	settings := get(t, ts, "/settings")
	if !strings.Contains(settings, "copied the file into the library instead of hardlinking") {
		t.Fatalf("settings note: %s", settings)
	}
}

func get(t *testing.T, ts *httptest.Server, path string) string {
	t.Helper()
	if !strings.HasPrefix(path, "http") {
		path = ts.URL + path
	}
	res, err := http.Get(path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s %d %s", path, res.StatusCode, b)
	}
	return string(b)
}
