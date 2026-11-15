package match

import (
	"testing"

	"animarr/internal/parser"
)

func TestScoreTitles(t *testing.T) {
	tests := []struct {
		name    string
		release string
		titles  []string
		wantMin float64
		wantMax float64
	}{
		{
			name:    "exact synonym",
			release: "Frieren",
			titles:  []string{"Sousou no Frieren", "Frieren: Beyond Journey's End", "Frieren"},
			wantMin: 1,
			wantMax: 1,
		},
		{
			name:    "punctuation and apostrophe",
			release: "Frieren: Beyond Journey's End",
			titles:  []string{"Frieren Beyond Journeys End"},
			wantMin: 1,
			wantMax: 1,
		},
		{
			name:    "romaji exact",
			release: "Sousou no Frieren",
			titles:  []string{"Sousou no Frieren"},
			wantMin: 1,
			wantMax: 1,
		},
		{
			name:    "different cour",
			release: "Mushoku Tensei",
			titles:  []string{"Mushoku Tensei II: Isekai Ittara Honki Dasu"},
			wantMin: 0,
			wantMax: 0.82,
		},
		{
			name:    "same cour",
			release: "Mushoku Tensei",
			titles:  []string{"Mushoku Tensei"},
			wantMin: 1,
			wantMax: 1,
		},
		{
			name:    "one piece is not one punch man",
			release: "One Piece",
			titles:  []string{"One Punch Man"},
			wantMin: 0,
			wantMax: 0.82,
		},
		{
			name:    "spirited away english",
			release: "Spirited Away",
			titles:  []string{"Sen to Chihiro no Kamikakushi", "Spirited Away"},
			wantMin: 1,
			wantMax: 1,
		},
		{
			name:    "movie suffix",
			release: "Spirited Away Movie",
			titles:  []string{"Spirited Away"},
			wantMin: 0.9,
			wantMax: 1,
		},
		{
			name:    "film is a different work",
			release: "One Piece Film Red",
			titles:  []string{"One Piece"},
			wantMin: 0,
			wantMax: 0.82,
		},
		{
			name:    "leading the",
			release: "The Apothecary Diaries",
			titles:  []string{"Apothecary Diaries"},
			wantMin: 1,
			wantMax: 1,
		},
		{
			name:    "road trip spacing",
			release: "The Forsaken Saintess and Her Foodie Road Trip in Another World",
			titles:  []string{"Honzuki", "The Forsaken Saintess and Her Foodie Roadtrip in Another World"},
			wantMin: 1,
			wantMax: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScoreTitles(tt.release, tt.titles)
			if got < tt.wantMin || got > tt.wantMax {
				t.Fatalf("score %v, want [%v, %v]", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestBestPicksShow(t *testing.T) {
	cands := []Candidate{
		{Key: 1, Titles: []string{"One Piece"}},
		{Key: 2, Titles: []string{"One Punch Man"}},
	}
	got, score, ok := Best("One Piece", cands, 0.82)
	if !ok || got.Key != 1 || score < 1 {
		t.Fatalf("got key %d score %v ok %v", got.Key, score, ok)
	}
}

func TestBestAmbiguous(t *testing.T) {
	cands := []Candidate{
		{Key: 1, Titles: []string{"Title"}},
		{Key: 2, Titles: []string{"Title"}},
	}
	_, _, ok := Best("Title", cands, 0.82)
	if ok {
		t.Fatal("expected ambiguous match to be refused")
	}
}

func TestSampleReleasesMatchTitle(t *testing.T) {
	samples := []string{
		"[ToonsHub] Title S01E07 1080p BILI WEB-DL AAC2.0 H.265 (Romaji alt title, Multi-Subs)",
		"[Erai-raws] Title - 07 [1080p][Multiple Subtitle]",
		"[ASW] Title - 07 [1080p HEVC x265 10Bit][AAC]",
		"[SubsPlease] Title - 07 (1080p) [A1B2C3D4]",
		"Title.S01E07.1080p.WEB-DL.AAC2.0.H.265-GROUP",
		"[Group] Title - 07v2",
	}
	show := []Candidate{{Key: 7, Titles: []string{"Title", "Title no Hon"}}}
	other := []Candidate{{Key: 8, Titles: []string{"Completely Different Show"}}}
	for _, raw := range samples {
		p := parser.Parse(raw)
		if p.Batch {
			t.Fatalf("%s unexpectedly batch", raw)
		}
		got, _, ok := Best(p.Title, show, 0.82)
		if !ok || got.Key != 7 {
			t.Fatalf("%s parsed title %q did not match", raw, p.Title)
		}
		if _, _, ok := Best(p.Title, other, 0.82); ok {
			t.Fatalf("%s matched the wrong show", raw)
		}
	}
}
