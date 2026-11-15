package nyaa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"animarr/internal/indexer"
)

func TestParseRSS(t *testing.T) {
	b, err := os.ReadFile("testdata/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	rels, err := ParseRSS(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 2 {
		t.Fatalf("len %d", len(rels))
	}
	a := rels[0]
	if a.Title != "[SubsPlease] Title - 07 (1080p) [A1B2C3D4]" {
		t.Fatalf("title %q", a.Title)
	}
	if a.InfoHash != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("hash %q", a.InfoHash)
	}
	if a.Seeders != 42 || a.Leechers != 3 {
		t.Fatalf("peers %d %d", a.Seeders, a.Leechers)
	}
	if a.Size != 367525888 {
		t.Fatalf("size %d", a.Size)
	}
	if !strings.HasPrefix(a.Magnet, "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01") {
		t.Fatalf("magnet %s", a.Magnet)
	}
	wantDate := time.Date(2025, 11, 29, 22, 10, 36, 0, time.UTC)
	if !a.PubDate.Equal(wantDate) {
		t.Fatalf("date %s", a.PubDate)
	}
	if rels[1].Size != int64(1.5*(1<<30)) {
		t.Fatalf("gib size %d", rels[1].Size)
	}
}

func TestFallbackMirror(t *testing.T) {
	okBody, err := os.ReadFile("testdata/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Errorf("missing user agent")
		}
		if !strings.Contains(r.URL.RawQuery, "page=rss") || !strings.Contains(r.URL.RawQuery, "c=1_2") {
			t.Errorf("query %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write(okBody)
	}))
	defer good.Close()

	c := New([]string{bad.URL, good.URL}, "1_2", "animarr-test", 0)
	c.MinInterval = 0
	c.Backoff = time.Millisecond
	rels, err := c.Recent(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 2 {
		t.Fatalf("len %d", len(rels))
	}
}

func TestRetry429(t *testing.T) {
	okBody, err := os.ReadFile("testdata/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write(okBody)
	}))
	defer srv.Close()
	c := New([]string{srv.URL}, "1_2", "animarr-test", 0)
	c.MinInterval = 0
	c.Backoff = time.Millisecond
	rels, err := c.Search(context.Background(), indexer.Query{Text: "Title"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 2 || hits < 2 {
		t.Fatalf("rels %d hits %d", len(rels), hits)
	}
}
