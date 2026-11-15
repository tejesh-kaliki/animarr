package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"animarr/internal/anilist"
	"animarr/internal/config"
	"animarr/internal/pipeline"
	"animarr/internal/score"
	"animarr/internal/store"
)

func TestDryRunDoesNotWrite(t *testing.T) {
	var collectionHits atomic.Int32
	alSrv := anilistServer(t, &collectionHits)
	defer alSrv.Close()
	sonarrSrv := sonarrServer(t)
	defer sonarrSrv.Close()

	st := openStore(t)
	ctx := context.Background()
	al := anilist.New("test")
	al.Endpoint = alSrv.URL
	al.HTTP = alSrv.Client()
	al.Backoff = time.Millisecond
	al.Pace = 0
	cfg := config.Config{
		AniListUsername: "alice",
		MatchThreshold:  0.82,
		Score:           score.DefaultConfig(),
	}
	pipe := &pipeline.Service{Config: cfg, Store: st, AniList: al, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var buf strings.Builder
	res, err := (&Runner{
		Config:    cfg,
		Store:     st,
		AniList:   al,
		Pipe:      pipe,
		Sonarr:    &Sonarr{BaseURL: sonarrSrv.URL, APIKey: "k", HTTP: sonarrSrv.Client()},
		DryRun:    true,
		Overrides: writeOverrides(t, `{"Custom Title": 42}`),
		Out:       &buf,
	}).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	text := buf.String()
	if collectionHits.Load() != 0 {
		t.Fatalf("dry-run synced AniList %d times", collectionHits.Load())
	}
	shows, err := st.ListShows(ctx, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(shows) != 0 {
		t.Fatalf("dry-run wrote %d shows", len(shows))
	}
	for _, want := range []string{"dry-run", "Frieren", "would mark", "multi-season", "Long Runner", "Custom Title", "S00E01", "not mapped"} {
		if !strings.Contains(text, want) {
			t.Fatalf("report missing %q\n%s", want, text)
		}
	}
	if res.FilesMarked != 0 || res.ShowsWritten != 0 {
		t.Fatalf("result %+v", res)
	}
}

func TestApplyRecordsPathInPlace(t *testing.T) {
	var collectionHits atomic.Int32
	alSrv := anilistServer(t, &collectionHits)
	defer alSrv.Close()
	sonarrSrv := sonarrServer(t)
	defer sonarrSrv.Close()

	st := openStore(t)
	ctx := context.Background()
	al := anilist.New("test")
	al.Endpoint = alSrv.URL
	al.HTTP = alSrv.Client()
	al.Backoff = time.Millisecond
	al.Pace = 0
	cfg := config.Config{AniListUsername: "alice", MatchThreshold: 0.82, Score: score.DefaultConfig()}
	pipe := &pipeline.Service{Config: cfg, Store: st, AniList: al, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var buf strings.Builder
	res, err := (&Runner{
		Config:  cfg,
		Store:   st,
		AniList: al,
		Pipe:    pipe,
		Sonarr:  &Sonarr{BaseURL: sonarrSrv.URL, APIKey: "k", HTTP: sonarrSrv.Client()},
		Out:     &buf,
	}).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if collectionHits.Load() == 0 {
		t.Fatal("expected a sync before apply")
	}
	if res.FilesMarked != 1 {
		t.Fatalf("marked %d\n%s", res.FilesMarked, buf.String())
	}
	sh, err := st.GetShowByAniList(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	ep, err := st.GetEpisode(ctx, sh.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	const want = "/data/media/anime/Frieren/Season 01/Frieren - S01E01.mkv"
	if ep.State != store.StateDownloaded || ep.FilePath != want {
		t.Fatalf("episode %+v", ep)
	}
	ep2, err := st.GetEpisode(ctx, sh.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if ep2.State == store.StateDownloaded {
		t.Fatal("episode 2 should stay without a file")
	}
	if !sh.Monitored {
		t.Fatal("synced show lost monitoring")
	}
	if _, err := st.GetShowByAniList(ctx, 200); err == nil {
		t.Fatal("multi-season series was written")
	}
}

func anilistServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		raw := string(body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(raw, "MediaListCollection"):
			hits.Add(1)
			_, _ = io.WriteString(w, `{"data":{"MediaListCollection":{"lists":[{"status":"CURRENT","entries":[{"status":"CURRENT","media":{"id":100,"title":{"romaji":"Frieren","english":"Frieren"},"format":"TV","status":"FINISHED","episodes":2}}]}]}}}`)
		case strings.Contains(raw, "Page(page"):
			_, _ = io.WriteString(w, `{"data":{"Page":{"media":[
				{"id":100,"title":{"romaji":"Frieren","english":"Frieren"},"format":"TV","status":"FINISHED","episodes":2},
				{"id":42,"title":{"romaji":"Custom Title","english":"Custom Title"},"format":"TV","status":"FINISHED","episodes":1},
				{"id":200,"title":{"romaji":"Long Runner","english":"Long Runner"},"format":"TV","status":"FINISHED","episodes":12}
			]}}}`)
		default:
			var wrap struct {
				Variables struct {
					ID int `json:"id"`
				} `json:"variables"`
			}
			_ = json.Unmarshal(body, &wrap)
			id, title, eps := 100, "Frieren", 2
			switch wrap.Variables.ID {
			case 42:
				id, title, eps = 42, "Custom Title", 1
			case 200:
				id, title, eps = 200, "Long Runner", 12
			}
			fmt.Fprintf(w, `{"data":{"Media":{"id":%d,"title":{"romaji":%q,"english":%q},"format":"TV","status":"FINISHED","episodes":%d}}}`, id, title, title, eps)
		}
	}))
}

func sonarrServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "k" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v3/series"):
			_, _ = io.WriteString(w, `[
				{"id":10,"title":"Frieren","year":2023,"path":"/data/media/anime/Frieren"},
				{"id":11,"title":"Long Runner","year":2001,"path":"/data/media/anime/Long Runner"},
				{"id":12,"title":"Custom Title","year":2020,"path":"/data/media/anime/Custom"}
			]`)
		case strings.HasPrefix(r.URL.Path, "/api/v3/episodefile"):
			if r.URL.Query().Get("seriesId") == "10" {
				_, _ = io.WriteString(w, `[{"id":5,"seriesId":10,"seasonNumber":1,"path":"/data/media/anime/Frieren/Season 01/Frieren - S01E01.mkv","relativePath":"Season 01/Frieren - S01E01.mkv"}]`)
				return
			}
			_, _ = io.WriteString(w, `[]`)
		default:
			switch r.URL.Query().Get("seriesId") {
			case "10":
				_, _ = io.WriteString(w, `[
					{"id":1,"seriesId":10,"episodeFileId":5,"seasonNumber":1,"episodeNumber":1,"hasFile":true},
					{"id":2,"seriesId":10,"episodeFileId":0,"seasonNumber":1,"episodeNumber":2,"hasFile":false},
					{"id":3,"seriesId":10,"episodeFileId":9,"seasonNumber":0,"episodeNumber":1,"hasFile":true,"title":"special"}
				]`)
			case "11":
				_, _ = io.WriteString(w, `[
					{"id":4,"seriesId":11,"episodeFileId":7,"seasonNumber":1,"episodeNumber":1,"hasFile":true},
					{"id":5,"seriesId":11,"episodeFileId":8,"seasonNumber":2,"episodeNumber":1,"hasFile":true}
				]`)
			default:
				_, _ = io.WriteString(w, `[{"id":6,"seriesId":12,"episodeFileId":0,"seasonNumber":1,"episodeNumber":1,"hasFile":false}]`)
			}
		}
	}))
}

func writeOverrides(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "migrate-overrides.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
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

func TestLoadOverridesBothForms(t *testing.T) {
	path := writeOverrides(t, `{
		"Plain": 154587,
		"Ascendance of a Bookworm": {"entries": [
			{"anilist": 108268, "sonarrSeason": 1, "episodeOffset": 0},
			{"anilist": 111000, "sonarrSeason": 1, "episodeOffset": 14}
		]}
	}`)
	got, err := loadOverrides(path)
	if err != nil {
		t.Fatal(err)
	}
	plain, ok := lookupOverride(got, "plain")
	if !ok || plain.Single != 154587 || len(plain.Rules) != 0 {
		t.Fatalf("plain %+v", plain)
	}
	chain, ok := lookupOverride(got, "ascendance of a bookworm")
	if !ok || !chain.Explicit || len(chain.Rules) != 2 || chain.Rules[1].EpisodeOffset != 14 || chain.Rules[1].AniListID != 111000 {
		t.Fatalf("chain %+v", chain)
	}
}

type countRefresh struct{ n int }

func (c *countRefresh) Enabled() bool { return true }
func (c *countRefresh) Refresh(context.Context) error {
	c.n++
	return nil
}

func TestRelayoutDryRunPrintsPaths(t *testing.T) {
	alSrv := bookwormAniList(t)
	defer alSrv.Close()
	old := t.TempDir()
	lib := t.TempDir()
	src := filepath.Join(old, "Bookworm - S01E05.mkv")
	if err := os.WriteFile(src, []byte("ep5"), 0o644); err != nil {
		t.Fatal(err)
	}
	sonarrSrv := bookwormSonarr(t, old)
	defer sonarrSrv.Close()
	st := openStore(t)
	al := anilist.New("test")
	al.Endpoint = alSrv.URL
	al.HTTP = alSrv.Client()
	al.Pace = 0
	var buf strings.Builder
	jf := &countRefresh{}
	_, err := (&Runner{
		Config:   config.Config{LibraryDir: lib, MatchThreshold: 0.82, Score: score.DefaultConfig(), NFOEnabled: true},
		Store:    st,
		AniList:  al,
		Sonarr:   &Sonarr{BaseURL: sonarrSrv.URL, APIKey: "k", HTTP: sonarrSrv.Client()},
		Jellyfin: jf,
		DryRun:   true,
		Relayout: true,
		Out:      &buf,
	}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	text := buf.String()
	wantDst := filepath.Join(lib, "Ascendance of a Bookworm", "Season 02", "Ascendance of a Bookworm - S02E01.mkv")
	if !strings.Contains(text, src+" -> "+wantDst) {
		t.Fatalf("missing path rewrite\n%s", text)
	}
	for _, want := range []string{"played state", "Watch history", "dry-run", "S00E01", "not mapped"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q\n%s", want, text)
		}
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wantDst); !os.IsNotExist(err) {
		t.Fatalf("dry-run created %s (%v)", wantDst, err)
	}
	shows, err := st.ListShows(context.Background(), "all")
	if err != nil || len(shows) != 0 || jf.n != 0 {
		t.Fatalf("shows %d refresh %d err %v", len(shows), jf.n, err)
	}
}

func TestRelayoutBookworm(t *testing.T) {
	alSrv := bookwormAniList(t)
	defer alSrv.Close()
	old := t.TempDir()
	lib := t.TempDir()
	paths := map[int]string{}
	for ep := 1; ep <= 8; ep++ {
		p := filepath.Join(old, fmt.Sprintf("Bookworm - S01E%02d.mkv", ep))
		if err := os.WriteFile(p, []byte(fmt.Sprintf("ep%d", ep)), 0o644); err != nil {
			t.Fatal(err)
		}
		paths[ep] = p
	}
	seed := filepath.Join(old, "seed-ep5.mkv")
	if err := os.Link(paths[5], seed); err != nil {
		t.Fatal(err)
	}
	special := filepath.Join(old, "special.mkv")
	if err := os.WriteFile(special, []byte("special"), 0o644); err != nil {
		t.Fatal(err)
	}
	occupied := filepath.Join(lib, "Ascendance of a Bookworm", "Season 03", "Ascendance of a Bookworm - S03E01.mkv")
	if err := os.MkdirAll(filepath.Dir(occupied), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(occupied, []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}

	sonarrSrv := bookwormSonarr(t, old)
	defer sonarrSrv.Close()
	st := openStore(t)
	al := anilist.New("test")
	al.Endpoint = alSrv.URL
	al.HTTP = alSrv.Client()
	al.Pace = 0
	jf := &countRefresh{}
	var buf strings.Builder
	res, err := (&Runner{
		Config:   config.Config{LibraryDir: lib, MatchThreshold: 0.82, Score: score.DefaultConfig(), NFOEnabled: true},
		Store:    st,
		AniList:  al,
		Sonarr:   &Sonarr{BaseURL: sonarrSrv.URL, APIKey: "k", HTTP: sonarrSrv.Client()},
		Jellyfin: jf,
		Relayout: true,
		Out:      &buf,
	}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesMarked != 8 || res.FilesMoved != 7 || jf.n != 1 {
		t.Fatalf("marked %d moved %d refresh %d\n%s", res.FilesMarked, res.FilesMoved, jf.n, buf.String())
	}
	if !strings.Contains(buf.String(), "destination exists") || !strings.Contains(buf.String(), "played state") {
		t.Fatalf("report\n%s", buf.String())
	}
	ctx := context.Background()
	root, err := st.GetShowByAniList(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if root.FranchiseID != 1 || root.SeasonNumber != 1 || root.Monitored {
		t.Fatalf("root %+v", root)
	}
	part2, err := st.GetShowByAniList(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if part2.FranchiseID != 1 || part2.SeasonNumber != 2 {
		t.Fatalf("part2 %+v", part2)
	}
	ep, err := st.GetEpisode(ctx, part2.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(lib, "Ascendance of a Bookworm", "Season 02", "Ascendance of a Bookworm - S02E01.mkv")
	if ep.FilePath != want || ep.State != store.StateDownloaded {
		t.Fatalf("episode %+v", ep)
	}
	got, err := os.ReadFile(want)
	if err != nil || string(got) != "ep5" {
		t.Fatalf("moved %q err %v", got, err)
	}
	seedBody, err := os.ReadFile(seed)
	if err != nil || string(seedBody) != "ep5" {
		t.Fatalf("hardlink %q err %v", seedBody, err)
	}
	if _, err := os.Stat(paths[5]); !os.IsNotExist(err) {
		t.Fatalf("source still present: %v", err)
	}
	part3, err := st.GetShowByAniList(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := st.GetEpisode(ctx, part3.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.FilePath != paths[8] {
		t.Fatalf("conflict path %s", blocked.FilePath)
	}
	if body, err := os.ReadFile(occupied); err != nil || string(body) != "occupied" {
		t.Fatalf("dest overwritten %q err %v", body, err)
	}
	if body, err := os.ReadFile(paths[8]); err != nil || string(body) != "ep8" {
		t.Fatalf("source removed on conflict %q err %v", body, err)
	}
	if _, err := os.Stat(special); err != nil {
		t.Fatal(err)
	}
	nfoBody, err := os.ReadFile(filepath.Join(lib, "Ascendance of a Bookworm", "tvshow.nfo"))
	if err != nil || !strings.Contains(string(nfoBody), ">1</uniqueid>") {
		t.Fatalf("tvshow %s err %v", nfoBody, err)
	}
	epNFO, err := os.ReadFile(strings.TrimSuffix(want, ".mkv") + ".nfo")
	if err != nil || !strings.Contains(string(epNFO), ">2</uniqueid>") || !strings.Contains(string(epNFO), "Episode 1") {
		t.Fatalf("episode nfo %s err %v", epNFO, err)
	}
}

func TestBranchedChainIsAmbiguous(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "Page(page") {
			fmt.Fprintf(w, `{"data":{"Page":{"media":[%s]}}}`, tvMedia(1, "Split", 4, 2019, ""))
			return
		}
		rels := edge("SEQUEL", 2, "TV", 4, 2020) + "," + edge("SEQUEL", 3, "TV", 4, 2020)
		fmt.Fprintf(w, `{"data":{"Media":%s}}`, tvMedia(1, "Split", 4, 2019, rels))
	}))
	defer srv.Close()
	sonarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v3/series"):
			_, _ = io.WriteString(w, `[{"id":1,"title":"Split","path":"/tv/Split"}]`)
		case strings.HasPrefix(r.URL.Path, "/api/v3/episodefile"):
			_, _ = io.WriteString(w, `[{"id":1,"seriesId":1,"seasonNumber":1,"path":"/tv/Split/E01.mkv"}]`)
		default:
			_, _ = io.WriteString(w, `[{"id":1,"seriesId":1,"episodeFileId":1,"seasonNumber":1,"episodeNumber":1,"hasFile":true}]`)
		}
	}))
	defer sonarrSrv.Close()
	st := openStore(t)
	al := anilist.New("test")
	al.Endpoint = srv.URL
	al.HTTP = srv.Client()
	al.Pace = 0
	var buf strings.Builder
	_, err := (&Runner{
		Config:  config.Config{MatchThreshold: 0.82, Score: score.DefaultConfig()},
		Store:   st,
		AniList: al,
		Sonarr:  &Sonarr{BaseURL: sonarrSrv.URL, APIKey: "k", HTTP: sonarrSrv.Client()},
		Out:     &buf,
	}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "not guessing") || !strings.Contains(buf.String(), "Split") {
		t.Fatalf("report\n%s", buf.String())
	}
	if _, err := st.GetShowByAniList(context.Background(), 1); err == nil {
		t.Fatal("ambiguous chain was written")
	}
}

func bookwormAniList(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "Page(page") {
			fmt.Fprintf(w, `{"data":{"Page":{"media":[%s]}}}`, tvMedia(1, "Ascendance of a Bookworm", 4, 2019, edge("SEQUEL", 2, "TV", 3, 2020)))
			return
		}
		var wrap struct {
			Variables struct {
				ID int `json:"id"`
			} `json:"variables"`
		}
		_ = json.Unmarshal(body, &wrap)
		var payload string
		switch wrap.Variables.ID {
		case 1:
			payload = tvMedia(1, "Ascendance of a Bookworm", 4, 2019, edge("SEQUEL", 2, "TV", 3, 2020))
		case 2:
			payload = tvMedia(2, "Ascendance of a Bookworm Part 2", 3, 2020, edge("PREQUEL", 1, "TV", 4, 2019)+","+edge("SEQUEL", 3, "TV", 3, 2022))
		case 3:
			payload = tvMedia(3, "Ascendance of a Bookworm Part 3", 3, 2022, edge("PREQUEL", 2, "TV", 3, 2020))
		default:
			http.Error(w, "missing", http.StatusNotFound)
			return
		}
		fmt.Fprintf(w, `{"data":{"Media":%s}}`, payload)
	}))
}

func bookwormSonarr(t *testing.T, old string) *httptest.Server {
	t.Helper()
	var eps []string
	var files []string
	for ep := 1; ep <= 8; ep++ {
		p := filepath.Join(old, fmt.Sprintf("Bookworm - S01E%02d.mkv", ep))
		eps = append(eps, fmt.Sprintf(`{"id":%d,"seriesId":1,"episodeFileId":%d,"seasonNumber":1,"episodeNumber":%d,"hasFile":true}`, ep, 100+ep, ep))
		files = append(files, fmt.Sprintf(`{"id":%d,"seriesId":1,"seasonNumber":1,"path":%q}`, 100+ep, p))
	}
	special := filepath.Join(old, "special.mkv")
	eps = append(eps, `{"id":90,"seriesId":1,"episodeFileId":200,"seasonNumber":0,"episodeNumber":1,"hasFile":true,"title":"special"}`)
	files = append(files, fmt.Sprintf(`{"id":200,"seriesId":1,"seasonNumber":0,"path":%q}`, special))
	series := `[{"id":1,"title":"Ascendance of a Bookworm","year":2019,"path":"/tv/Bookworm"}]`
	epJSON := "[" + strings.Join(eps, ",") + "]"
	fileJSON := "[" + strings.Join(files, ",") + "]"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "k" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v3/series"):
			_, _ = io.WriteString(w, series)
		case strings.HasPrefix(r.URL.Path, "/api/v3/episodefile"):
			_, _ = io.WriteString(w, fileJSON)
		default:
			_, _ = io.WriteString(w, epJSON)
		}
	}))
}

func tvMedia(id int, title string, episodes, year int, rels string) string {
	if strings.TrimSpace(rels) == "" {
		rels = ""
	}
	if rels != "" {
		rels = "[" + rels + "]"
	} else {
		rels = "[]"
	}
	return fmt.Sprintf(`{"id":%d,"title":{"romaji":%q,"english":%q},"format":"TV","status":"FINISHED","episodes":%d,"description":"A girl and books","genres":["Fantasy"],"studios":{"nodes":[{"name":"Ajia-do"}]},"startDate":{"year":%d},"relations":{"edges":%s}}`, id, title, title, episodes, year, rels)
}

func edge(kind string, id int, format string, episodes, year int) string {
	return fmt.Sprintf(`{"relationType":%q,"node":{"id":%d,"type":"ANIME","format":%q,"episodes":%d,"title":{"english":"x"},"startDate":{"year":%d}}}`, kind, id, format, episodes, year)
}

func TestDrStoneChainCrossesSpecial(t *testing.T) {
	medias := loadMediaFixture(t, "testdata/drstone.json")
	srv := mediaServer(t, medias)
	defer srv.Close()
	al := anilist.New("test")
	al.Endpoint = srv.URL
	al.HTTP = srv.Client()
	al.Pace = 0
	r := &Runner{AniList: al}
	ctx := context.Background()

	wantIDs := []int{105333, 113936, 131518, 162670, 172019, 189117, 199221}
	files := []srcFile{
		{Season: 4, Episode: 34, Path: "/tv/Dr. STONE/S04E34.mkv"},
		{Season: 4, Episode: 35, Path: "/tv/Dr. STONE/S04E35.mkv"},
		{Season: 4, Episode: 36, Path: "/tv/Dr. STONE/S04E36.mkv"},
		{Season: 4, Episode: 37, Path: "/tv/Dr. STONE/S04E37.mkv"},
	}
	for _, startID := range []int{105333, 131518} {
		t.Run(fmt.Sprint(startID), func(t *testing.T) {
			plan, err := r.planChain(ctx, medias[startID])
			if err != nil {
				t.Fatal(err)
			}
			if plan.Ambiguous != "" {
				t.Fatal(plan.Ambiguous)
			}
			if len(plan.Main) != len(wantIDs) {
				t.Fatalf("main %s", plan.describe())
			}
			for i, id := range wantIDs {
				if plan.Main[i].ID != id || plan.Main[i].SeasonNumber != i+1 {
					t.Fatalf("season %d got %+v", i+1, plan.Main[i])
				}
			}
			if plan.Main[0].Episodes != 24 || plan.Main[1].Episodes != 11 || plan.Main[2].Episodes != 11 {
				t.Fatalf("counts %s", plan.describe())
			}
			for _, e := range plan.Main {
				if e.ID == 142876 || e.ID == 218099 {
					t.Fatalf("non-TV entry in main: %+v", e)
				}
			}
			var special bool
			for _, e := range plan.Extras {
				if e.ID == 142876 && e.SeasonNumber == 0 {
					special = true
				}
				if e.ID == 218099 {
					t.Fatalf("null-format sequel became an extra: %+v", e)
				}
			}
			if !special {
				t.Fatalf("RYUSUI missing from extras: %s", plan.describe())
			}
			got := mapFiles(plan.Main, files, nil)
			if got.Mode != "continuous" {
				t.Fatalf("mode %s %s", got.Mode, got.Evidence)
			}
			want := map[int][2]int{34: {113936, 10}, 35: {113936, 11}, 36: {131518, 1}, 37: {131518, 2}}
			if len(got.Files) != len(files) {
				t.Fatalf("mapped %d", len(got.Files))
			}
			for _, f := range got.Files {
				w := want[f.SonarrEpisode]
				if f.AniListID != w[0] || f.Episode != w[1] || f.LibrarySeason != plan.Main[indexOf(wantIDs, w[0])].SeasonNumber {
					t.Fatalf("S04E%02d -> %d ep %d season %d", f.SonarrEpisode, f.AniListID, f.Episode, f.LibrarySeason)
				}
			}
		})
	}
}

func TestSequelBranchThroughSpecialIsAmbiguous(t *testing.T) {
	raw := map[int]string{
		1: tvMedia(1, "Split", 12, 2019, edge("SEQUEL", 2, "TV", 12, 2020)+","+edge("SEQUEL", 9, "SPECIAL", 1, 2020)),
		9: `{"id":9,"title":{"romaji":"Bridge","english":"Bridge"},"format":"SPECIAL","status":"FINISHED","episodes":1,"relations":{"edges":[` + edge("SEQUEL", 3, "TV", 12, 2021) + `]}}`,
	}
	srv := rawMediaServer(t, raw)
	defer srv.Close()
	al := anilist.New("test")
	al.Endpoint = srv.URL
	al.HTTP = srv.Client()
	al.Pace = 0
	plan, err := (&Runner{AniList: al}).planChain(context.Background(), anilist.Media{ID: 1, Format: "TV"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ambiguous == "" || !strings.Contains(plan.Ambiguous, "not guessing") {
		t.Fatalf("ambiguous %q", plan.Ambiguous)
	}
	if len(plan.Main) != 0 {
		t.Fatalf("wrote %+v", plan.Main)
	}
}

func TestDeadEndSpecialStaysSeasonZero(t *testing.T) {
	raw := map[int]string{
		1: tvMedia(1, "Show", 12, 2019, edge("SEQUEL", 9, "SPECIAL", 1, 2019)),
		9: `{"id":9,"title":{"romaji":"Special","english":"Special"},"format":"SPECIAL","status":"FINISHED","episodes":1,"relations":{"edges":[]}}`,
	}
	srv := rawMediaServer(t, raw)
	defer srv.Close()
	al := anilist.New("test")
	al.Endpoint = srv.URL
	al.HTTP = srv.Client()
	al.Pace = 0
	plan, err := (&Runner{AniList: al}).planChain(context.Background(), anilist.Media{ID: 1, Format: "TV"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ambiguous != "" {
		t.Fatal(plan.Ambiguous)
	}
	if len(plan.Main) != 1 || plan.Main[0].ID != 1 || plan.Main[0].SeasonNumber != 1 {
		t.Fatalf("main %+v", plan.Main)
	}
	if len(plan.Extras) != 1 || plan.Extras[0].ID != 9 || plan.Extras[0].SeasonNumber != 0 {
		t.Fatalf("extras %+v", plan.Extras)
	}
}

func TestTrailingUnreleasedStatusReachesMapper(t *testing.T) {
	raw := map[int]string{
		108268: tvMedia(108268, "Ascendance of a Bookworm", 14, 2019, `{"relationType":"SEQUEL","node":{"id":217712,"type":"ANIME","format":"TV","status":"NOT_YET_RELEASED","episodes":null,"title":{"english":"Ascendance of a Bookworm Season 5","romaji":"Honzuki"}}}`),
		217712: `{"id":217712,"title":{"romaji":"Honzuki no Gekokujou: Kizoku-in no Jishou Tosho Iin"},"format":"TV","status":"NOT_YET_RELEASED","episodes":null,"relations":{"edges":[{"relationType":"PREQUEL","node":{"id":108268,"type":"ANIME","format":"TV","status":"FINISHED","episodes":14,"title":{"english":"Ascendance of a Bookworm"}}}]}}`,
	}
	srv := rawMediaServer(t, raw)
	defer srv.Close()
	al := anilist.New("test")
	al.Endpoint = srv.URL
	al.HTTP = srv.Client()
	al.Pace = 0
	plan, err := (&Runner{AniList: al}).planChain(context.Background(), anilist.Media{ID: 108268, Format: "TV"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ambiguous != "" {
		t.Fatal(plan.Ambiguous)
	}
	if len(plan.Main) != 2 || plan.Main[1].ID != 217712 || plan.Main[1].SeasonNumber != 2 || plan.Main[1].Episodes != 0 || plan.Main[1].Status != "NOT_YET_RELEASED" {
		t.Fatalf("main %+v", plan.Main)
	}
	got := mapFiles(plan.Main, filesThrough(1, 14), nil)
	if got.Mode != "per-season" {
		t.Fatalf("mode %s %s", got.Mode, got.Evidence)
	}
	if len(got.Files) != 14 {
		t.Fatalf("mapped %d", len(got.Files))
	}
}

func TestSaintessTitlesMatch(t *testing.T) {
	body, err := os.ReadFile("testdata/saintess.json")
	if err != nil {
		t.Fatal(err)
	}
	var media anilist.Media
	if err := json.Unmarshal(body, &media); err != nil {
		t.Fatal(err)
	}
	const (
		english = "The Forsaken Saintess and Her Foodie Road Trip in Another World"
		short   = "Suterare Seijo no Isekai Gohan Tabi"
		long    = "Suterare Seijo no Isekai Gohan Tabi: Kakure Skill de Camping Car wo Shoukan shimashita"
	)
	names := seriesNames(Series{
		Title:      english,
		SortTitle:  short,
		CleanTitle: "forsaken saintess foodie",
		AlternateTitles: []struct {
			Title string `json:"title"`
		}{{Title: short}, {Title: long}},
	})
	for _, want := range []string{english, short, long, "forsaken saintess foodie"} {
		if !containsFold(names, want) {
			t.Fatalf("names %q missing %s", names, want)
		}
	}
	variants := fallbackQueries(names)
	if !containsStr(variants, "The Forsaken Saintess and Her Foodie RoadTrip in Another World") {
		t.Fatalf("variants missing RoadTrip join: %q", variants)
	}
	if !containsStr(variants, "Suterare Seijo no Isekai GohanTabi") {
		t.Fatalf("variants missing GohanTabi join: %q", variants)
	}

	var searched []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqBody, _ := io.ReadAll(r.Body)
		var wrap struct {
			Variables struct {
				Search string `json:"search"`
				ID     int    `json:"id"`
			} `json:"variables"`
		}
		_ = json.Unmarshal(reqBody, &wrap)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(reqBody), "Page(page") {
			searched = append(searched, wrap.Variables.Search)
			if saintessSearchHit(wrap.Variables.Search) {
				payload, _ := json.Marshal(media)
				fmt.Fprintf(w, `{"data":{"Page":{"media":[%s]}}}`, payload)
				return
			}
			_, _ = io.WriteString(w, `{"data":{"Page":{"media":[]}}}`)
			return
		}
		payload, _ := json.Marshal(media)
		fmt.Fprintf(w, `{"data":{"Media":%s}}`, payload)
	}))
	defer srv.Close()

	cases := []Series{
		{
			ID: 1, Title: english, Path: "/tv/Saintess",
			AlternateTitles: []struct {
				Title string `json:"title"`
			}{{Title: short}, {Title: long}},
		},
		{ID: 1, Title: long, Path: "/tv/Saintess"},
		{
			ID: 1, Title: "Unrelated Sonarr Name", Path: "/tv/Saintess",
			AlternateTitles: []struct {
				Title string `json:"title"`
			}{{Title: long}},
		},
	}
	for _, sr := range cases {
		t.Run(sr.Title, func(t *testing.T) {
			searched = nil
			text := runSeries(t, srv, sr)
			if strings.Contains(text, "unmatched") || !strings.Contains(text, "209504") || !strings.Contains(text, "matched:") {
				t.Fatalf("report\n%s\nsearches %q", text, searched)
			}
		})
	}
}

func saintessSearchHit(q string) bool {
	// Live AniList search misses a space inside "Road Trip" / "Gohan Tabi"
	// and hits once that space is removed. The fixture copies that.
	folded := strings.ToLower(q)
	if strings.Contains(folded, "road trip") || strings.Contains(folded, "gohan tabi") {
		return false
	}
	squashed := strings.ReplaceAll(folded, " ", "")
	return strings.Contains(squashed, "roadtrip") || strings.Contains(squashed, "gohantabi")
}

func runSeries(t *testing.T, alSrv *httptest.Server, sr Series) string {
	t.Helper()
	payload, err := json.Marshal([]Series{sr})
	if err != nil {
		t.Fatal(err)
	}
	sonarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "k" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v3/series"):
			_, _ = w.Write(payload)
		case strings.HasPrefix(r.URL.Path, "/api/v3/episodefile"):
			_, _ = io.WriteString(w, `[{"id":1,"seriesId":1,"seasonNumber":1,"path":"/tv/Saintess/E01.mkv"}]`)
		default:
			_, _ = io.WriteString(w, `[{"id":1,"seriesId":1,"episodeFileId":1,"seasonNumber":1,"episodeNumber":1,"hasFile":true}]`)
		}
	}))
	defer sonarrSrv.Close()
	al := anilist.New("test")
	al.Endpoint = alSrv.URL
	al.HTTP = alSrv.Client()
	al.Pace = 0
	var buf strings.Builder
	_, err = (&Runner{
		Config:  config.Config{MatchThreshold: 0.82, Score: score.DefaultConfig()},
		Store:   openStore(t),
		AniList: al,
		Sonarr:  &Sonarr{BaseURL: sonarrSrv.URL, APIKey: "k", HTTP: sonarrSrv.Client()},
		DryRun:  true,
		Out:     &buf,
	}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func loadMediaFixture(t *testing.T, path string) map[int]anilist.Media {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var list []anilist.Media
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	out := make(map[int]anilist.Media, len(list))
	for _, m := range list {
		out[m.ID] = m
	}
	return out
}

func mediaServer(t *testing.T, medias map[int]anilist.Media) *httptest.Server {
	t.Helper()
	raw := map[int]string{}
	for id, m := range medias {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		raw[id] = string(b)
	}
	return rawMediaServer(t, raw)
}

func rawMediaServer(t *testing.T, raw map[int]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var wrap struct {
			Variables struct {
				ID int `json:"id"`
			} `json:"variables"`
		}
		_ = json.Unmarshal(body, &wrap)
		payload, ok := raw[wrap.Variables.ID]
		if !ok || strings.Contains(string(body), "Page(page") {
			http.Error(w, "missing "+string(body), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"Media":%s}}`, payload)
	}))
}

func indexOf(ids []int, id int) int {
	for i, v := range ids {
		if v == id {
			return i
		}
	}
	return -1
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
