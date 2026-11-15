package anilist

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestListAndSearch(t *testing.T) {
	collection, err := os.ReadFile("testdata/collection.json")
	if err != nil {
		t.Fatal(err)
	}
	search, err := os.ReadFile("testdata/search.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type %s", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		raw := string(body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(raw, "MediaListCollection"):
			_, _ = w.Write(collection)
		case strings.Contains(raw, "Page(page"):
			_, _ = w.Write(search)
		case strings.Contains(raw, "Media"):
			_, _ = w.Write([]byte(`{"data":{"Media":{"id":199,"title":{"romaji":"Sen to Chihiro no Kamikakushi","english":"Spirited Away","native":""},"synonyms":[],"format":"MOVIE","status":"FINISHED","episodes":1,"coverImage":{"large":"https://example.test/c.jpg"},"season":"SUMMER","seasonYear":2001,"startDate":{"year":2001,"month":7,"day":20},"airingSchedule":{"nodes":[]},"relations":{"edges":[]}}}}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := New("animarr-test")
	c.Endpoint = srv.URL
	c.Backoff = time.Millisecond
	c.Pace = 0

	entries, err := c.ListCollection(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries %d", len(entries))
	}
	if entries[0].Media.DisplayTitle() != "Frieren: Beyond Journey's End" {
		t.Fatalf("title %s", entries[0].Media.DisplayTitle())
	}
	if entries[0].Media.CoverURL() != "https://example.test/frieren.jpg" {
		t.Fatalf("cover %s", entries[0].Media.CoverURL())
	}
	if entries[0].Media.EpisodeTotal() != 28 || entries[0].Status != "CURRENT" {
		t.Fatalf("frieren %+v", entries[0])
	}
	if entries[1].Media.ID != 199 || entries[1].Media.Format != "MOVIE" || entries[1].Status != "PLANNING" {
		t.Fatalf("movie %+v", entries[1])
	}
	if !strings.Contains(entries[0].Media.RelationsJSON(), "170942") {
		t.Fatalf("relations %s", entries[0].Media.RelationsJSON())
	}

	found, err := c.Search(context.Background(), "Spirited Away")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != 199 || found[0].Title.English != "Spirited Away" {
		t.Fatalf("search %+v", found)
	}

	one, err := c.Get(context.Background(), 199)
	if err != nil {
		t.Fatal(err)
	}
	if one.ID != 199 || one.StartYear() != 2001 {
		t.Fatalf("get %+v", one)
	}
}

func TestGraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"Not Found."}]}`))
	}))
	defer srv.Close()
	c := New("animarr-test")
	c.Endpoint = srv.URL
	c.Pace = 0
	_, err := c.ListCollection(context.Background(), "missing")
	if err == nil || !strings.Contains(err.Error(), "Not Found") {
		t.Fatalf("err %v", err)
	}
}

func TestGetRelations(t *testing.T) {
	body, err := os.ReadFile("testdata/relations.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	c := New("animarr-test")
	c.Endpoint = srv.URL
	c.Pace = 0
	m, err := c.Get(context.Background(), 108268)
	if err != nil {
		t.Fatal(err)
	}
	if m.StudioName() != "Ajia-do" || m.StartYear() != 2019 || !strings.Contains(m.Description, "books") {
		t.Fatalf("media %+v", m)
	}
	if len(m.Relations.Edges) != 2 {
		t.Fatalf("edges %d", len(m.Relations.Edges))
	}
	seq := m.Relations.Edges[0]
	if seq.RelationType != "SEQUEL" || seq.Node.ID != 111000 || seq.Node.Format != "TV" || seq.Node.Episodes == nil || *seq.Node.Episodes != 12 {
		t.Fatalf("sequel %+v", seq)
	}
	if seq.Node.StartDate.Year == nil || *seq.Node.StartDate.Year != 2020 || seq.Node.StartDate.Month == nil || *seq.Node.StartDate.Month != 4 {
		t.Fatalf("start %+v", seq.Node.StartDate)
	}
	if m.Relations.Edges[1].Node.Format != "OVA" {
		t.Fatalf("ova %+v", m.Relations.Edges[1])
	}
}

func TestEpisodeTotalMovieNull(t *testing.T) {
	m := Media{Format: "MOVIE"}
	if m.EpisodeTotal() != 1 {
		t.Fatalf("%d", m.EpisodeTotal())
	}
}

func TestLiveSpiritedAway(t *testing.T) {
	if os.Getenv("ANIMARR_LIVE") != "1" {
		t.Skip("set ANIMARR_LIVE=1 to hit anilist")
	}
	c := New("animarr/0.1 (+homelab; anilist sanity)")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	found, err := c.Search(ctx, "Spirited Away")
	if err != nil {
		t.Fatal(err)
	}
	var hit bool
	for _, m := range found {
		if m.ID == 199 && m.Title.English == "Spirited Away" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("id 199 not in %+v", found)
	}
}
