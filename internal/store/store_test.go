package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestShowEpisodeRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	air := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	sh, err := s.UpsertShow(ctx, Show{
		AniListID: 10, TitleRomaji: "Title", TitleEnglish: "English Title", Synonyms: []string{"Alt"},
		Format: "TV", Status: "RELEASING", ListStatus: "CURRENT", Episodes: 2, Monitored: true,
		NextAiringAt: &air, CoverImage: "http://img", Season: "WINTER", SeasonYear: 2024, StartYear: 2024, StartMonth: 1, StartDay: 5,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if sh.ID == 0 || !sh.Monitored || sh.DisplayTitle() != "English Title" || sh.StartMonth != 1 || sh.StartDay != 5 {
		t.Fatalf("%+v", sh)
	}
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	if err := s.EnsureEpisode(ctx, sh.ID, 1, &past, StateWanted); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureEpisode(ctx, sh.ID, 2, &future, StateUpcoming); err != nil {
		t.Fatal(err)
	}
	ep, err := s.GetEpisode(ctx, sh.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	ep.State = StateDownloaded
	ep.FilePath = "/data/media/anime/English Title/Season 01/English Title - S01E01.mkv"
	ep.InfoHash = "abc"
	now := time.Now().UTC()
	ep.DownloadedAt = &now
	if err := s.UpdateEpisode(ctx, ep); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureEpisode(ctx, sh.ID, 1, &past, StateWanted); err != nil {
		t.Fatal(err)
	}
	ep, err = s.GetEpisode(ctx, sh.ID, 1)
	if err != nil || ep.State != StateDownloaded || ep.FilePath == "" {
		t.Fatalf("state was overwritten: %+v %v", ep, err)
	}

	again, err := s.UpsertShow(ctx, Show{AniListID: 10, TitleRomaji: "Title", TitleEnglish: "English Title", Monitored: false, ListStatus: "CURRENT"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Monitored {
		t.Fatal("monitored flag was not preserved")
	}

	shows, err := s.ListShows(ctx, "monitored")
	if err != nil || len(shows) != 1 || shows[0].Downloaded != 1 || shows[0].TotalEps != 2 {
		t.Fatalf("%+v %v", shows, err)
	}
	if shows[0].ProgressPct() != 50 {
		t.Fatalf("pct %d", shows[0].ProgressPct())
	}

	n, err := s.PromoteAired(ctx, time.Now().Add(2*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("promoted %d %v", n, err)
	}
	ep2, err := s.GetEpisode(ctx, sh.ID, 2)
	if err != nil || ep2.State != StateWanted {
		t.Fatalf("%+v", ep2)
	}

	back, err := s.WantedBacklog(ctx, time.Now().Add(time.Hour), time.Now(), 10)
	if err != nil || len(back) != 1 || back[0].Episode.Number != 2 {
		t.Fatalf("%+v %v", back, err)
	}

	id, err := s.AddDownload(ctx, Download{ShowID: sh.ID, Episode: 2, InfoHash: "abc", Title: "rel", State: DlDownloading, SavePath: "/data"})
	if err != nil || id == 0 {
		t.Fatal(err)
	}
	if err := s.UpdateDownload(ctx, id, DlImported, 100, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.AddHistory(ctx, History{Level: "info", Message: "imported", ShowID: &sh.ID, Episode: &ep2.Number}); err != nil {
		t.Fatal(err)
	}
	dash, err := s.Dashboard(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if dash.Shows != 1 || len(dash.Recent) != 1 || dash.Recent[0].ShowTitle != "English Title" {
		t.Fatalf("%+v", dash)
	}
	hist, err := s.ListHistory(ctx, 10)
	if err != nil || len(hist) != 1 || hist[0].Message != "imported" {
		t.Fatalf("%+v %v", hist, err)
	}
}

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "animarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
