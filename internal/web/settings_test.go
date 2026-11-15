package web

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"animarr/internal/config"
	"animarr/internal/pipeline"
	"animarr/internal/score"
	"animarr/internal/secret"
	"animarr/internal/settings"
	"animarr/internal/store"
)

func TestSettingsSaveCSRFAndSecretRedaction(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "animarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	const jelly = "jellyfin-secret-value-zz"
	base := config.Config{
		ListenAddr: ":8990", DataPath: dir,
		LibraryDir: "/library", DownloadsDir: "/downloads",
		NyaaURLs: []string{"https://nyaa.si"}, NyaaCategory: "1_2", UserAgent: "animarr-test",
		QBURL: "http://127.0.0.1:8080", QBCategory: "animarr",
		JellyfinURL:  "http://jellyfin:8096",
		SyncInterval: 30 * time.Minute, RSSInterval: 15 * time.Minute, QBPollInterval: time.Minute,
		RSSWindow: 6 * time.Hour, SearchBackoff: 6 * time.Hour, RequestInterval: 2 * time.Second,
		BacklogPerPoll: 3, Score: score.DefaultConfig(),
		Upgrade:        score.Upgrade{Repack: true, Window: 72 * time.Hour, MinDelta: 15},
		MatchThreshold: 0.82, NFOEnabled: true, LogLevel: "info",
	}
	prov := settings.New(base, map[string]config.Source{
		config.KeyLibraryDir: config.SourceDefault,
	}, st, secret.Loaded{Box: secret.NewBox(testKey(1)), Source: "file"})
	pipe := &pipeline.Service{Config: base, Settings: prov, Store: st}
	srv, err := New(pipe, st, base)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	body := getAuth(t, client, ts.URL+"/settings")
	if !strings.Contains(body, "qBittorrent") || !strings.Contains(body, "Not set") || strings.Contains(body, "Save empty") {
		t.Fatalf("settings page: %s", body)
	}
	tok := csrfOf(t, body)

	bad := postForm(t, client, ts, "/settings", tok, url.Values{
		config.KeyLibraryDir: {"relative/nope"},
	}, ts.URL)
	if bad.StatusCode != http.StatusBadRequest || !strings.Contains(bad.Body, "absolute") {
		t.Fatalf("validation %d %s", bad.StatusCode, bad.Body)
	}

	evil := postForm(t, client, ts, "/settings", tok, url.Values{
		config.KeyLibraryDir: {"/data/media/anime"},
	}, "https://evil.example")
	if evil.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf origin %d %s", evil.StatusCode, evil.Body)
	}

	noTok := postForm(t, client, ts, "/settings", "not-the-token", url.Values{
		config.KeyLibraryDir: {"/data/media/anime"},
	}, ts.URL)
	if noTok.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf token %d %s", noTok.StatusCode, noTok.Body)
	}
	if rows, err := st.ListSettings(context.Background()); err != nil || len(rows) != 0 {
		t.Fatalf("rejected posts wrote %+v %v", rows, err)
	}

	ok := postForm(t, client, ts, "/settings", tok, url.Values{
		config.KeyLibraryDir:     {"/data/media/anime"},
		config.KeyJellyfinAPIKey: {jelly},
	}, ts.URL)
	if ok.StatusCode != http.StatusSeeOther {
		t.Fatalf("save %d %s", ok.StatusCode, ok.Body)
	}
	cur, err := prov.Current(context.Background())
	if err != nil || cur.LibraryDir != "/data/media/anime" || cur.JellyfinAPIKey != jelly {
		t.Fatalf("saved %+v %v", cur.LibraryDir, err)
	}
	page := getAuth(t, client, ts.URL+"/settings")
	if strings.Contains(page, jelly) {
		t.Fatal("settings HTML contains the jellyfin key")
	}
	row, err := st.GetSetting(context.Background(), config.KeyJellyfinAPIKey)
	if err != nil || strings.Contains(page, row.Value) {
		t.Fatal("settings HTML contains ciphertext")
	}
	if !strings.Contains(page, ">saved<") && !strings.Contains(page, "saved") {
		t.Fatalf("expected saved source: %s", page)
	}

	reset := postForm(t, client, ts, "/settings", csrfOf(t, page), url.Values{
		"reset": {config.KeyLibraryDir},
	}, ts.URL)
	if reset.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset %d %s", reset.StatusCode, reset.Body)
	}
	cur, _ = prov.Current(context.Background())
	if cur.LibraryDir != "/library" {
		t.Fatalf("library after reset %s", cur.LibraryDir)
	}
}

type posted struct {
	StatusCode int
	Body       string
}

func postForm(t *testing.T, client *http.Client, ts *httptest.Server, path, tok string, extra url.Values, origin string) posted {
	t.Helper()
	form := url.Values{"csrf": {tok}}
	for k, vs := range extra {
		for _, v := range vs {
			form.Add(k, v)
		}
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", origin)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return posted{StatusCode: res.StatusCode, Body: string(b)}
}

func getAuth(t *testing.T, client *http.Client, raw string) string {
	t.Helper()
	res, err := client.Get(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s %d %s", raw, res.StatusCode, b)
	}
	return string(b)
}

func csrfOf(t *testing.T, body string) string {
	t.Helper()
	const marker = `name="csrf" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no csrf token in %s", body)
	}
	rest := body[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatal("csrf token not closed")
	}
	return rest[:j]
}

func testKey(b byte) [32]byte {
	var key [32]byte
	for i := range key {
		key[i] = b
	}
	return key
}
