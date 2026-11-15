package web

import (
	"strings"
	"testing"

	"animarr/internal/store"
)

func TestLibraryGridOneCardPerFranchise(t *testing.T) {
	mk := func(id int64, anilist, fr, season int, title string) showCard {
		return showCard{Show: store.Show{
			ID: id, AniListID: anilist, FranchiseID: fr, SeasonNumber: season,
			TitleEnglish: title, CoverImage: title + ".jpg",
			Downloaded: int(id), TotalEps: int(id) * 2,
		}}
	}
	cards := []showCard{
		mk(1, 20, 10, 2, "Book Two"),
		mk(2, 10, 10, 1, "Book One"),
		mk(3, 30, 30, 1, "Solo"),
		mk(4, 40, 0, 1, "Loose"),
	}
	cards[1].CoverImage = "root.jpg"
	grid := libraryGrid(cards)
	if len(grid) != 3 {
		t.Fatalf("grid = %d %+v", len(grid), grid)
	}
	if !grid[0].Multi || grid[0].Entries != 2 || grid[0].DisplayTitle() != "Book One" || grid[0].CoverImage != "root.jpg" {
		t.Fatalf("franchise card %+v", grid[0])
	}
	if grid[0].Href != "/franchises/10" || !strings.Contains(grid[0].Badge, "2 seasons") || !strings.Contains(grid[0].Badge, "3/6 eps") {
		t.Fatalf("badge %+v", grid[0])
	}
	if grid[1].Multi || grid[1].Href != "/shows/3" || grid[1].DisplayTitle() != "Solo" {
		t.Fatalf("single franchise %+v", grid[1])
	}
	if grid[2].Multi || grid[2].DisplayTitle() != "Loose" {
		t.Fatalf("loose %+v", grid[2])
	}
}

func TestOrderFranchiseEntriesByStartDate(t *testing.T) {
	at := func(id int64, anilist, season, year, month, day int, format, title string) showCard {
		return showCard{Show: store.Show{
			ID: id, AniListID: anilist, FranchiseID: 10, SeasonNumber: season,
			StartYear: year, StartMonth: month, StartDay: day,
			Format: format, TitleEnglish: title,
		}}
	}
	// Season 2 airs first. A movie dated between the seasons must not be stuck at the end.
	// Same start date: lower season number, then AniList id.
	cards := []showCard{
		at(1, 30, 2, 2021, 7, 1, "TV", "Season Two"),
		at(2, 11, 0, 2020, 1, 10, "MOVIE", "The Movie"),
		at(3, 12, 0, 2020, 1, 10, "OVA", "The OVA"),
		at(4, 10, 1, 2019, 4, 6, "TV", "Season One"),
		at(5, 40, 0, 0, 0, 0, "SPECIAL", "Undated"),
	}
	got := orderByStart(cards)
	var titles []string
	for _, c := range got {
		titles = append(titles, c.DisplayTitle())
	}
	want := []string{"Season One", "The Movie", "The OVA", "Season Two", "Undated"}
	if strings.Join(titles, ",") != strings.Join(want, ",") {
		t.Fatalf("order %v", titles)
	}
	if seasonLabel(got[0].Show) != "Season 1" || seasonLabel(got[1].Show) != "Movie" || seasonLabel(got[2].Show) != "OVA" || seasonLabel(got[4].Show) != "Special" {
		t.Fatalf("labels %s %s %s %s", seasonLabel(got[0].Show), seasonLabel(got[1].Show), seasonLabel(got[2].Show), seasonLabel(got[4].Show))
	}
	if releaseMonth(got[0].Show) != "Apr 2019" || releaseMonth(got[1].Show) != "Jan 2020" {
		t.Fatalf("months %s %s", releaseMonth(got[0].Show), releaseMonth(got[1].Show))
	}
	yearOnly := store.Show{StartYear: 2018, SeasonYear: 2019}
	if releaseMonth(yearOnly) != "2018" {
		t.Fatalf("year only %s", releaseMonth(yearOnly))
	}
	if releaseMonth(store.Show{SeasonYear: 2017}) != "2017" {
		t.Fatal("season year fallback")
	}
}
