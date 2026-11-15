package franchise

import "testing"

func TestPlaceSequelChain(t *testing.T) {
	shows := []Show{
		{AniListID: 2, Format: "TV", StartYear: 2020, RelationsJSON: `[{"type":"PREQUEL","id":1,"format":"TV"},{"type":"SEQUEL","id":3,"format":"TV"}]`},
		{AniListID: 1, Format: "TV", StartYear: 2019, Title: "Bookworm", RelationsJSON: `[{"type":"SEQUEL","id":2,"format":"TV"}]`},
		{AniListID: 3, Format: "TV", StartYear: 2022, RelationsJSON: `[{"type":"PREQUEL","id":2,"format":"TV"}]`},
		{AniListID: 9, Format: "OVA", StartYear: 2020, Episodes: 1, RelationsJSON: `[{"type":"SIDE_STORY","id":1,"format":"TV"}]`},
		{AniListID: 199, Format: "MOVIE", StartYear: 2001, Title: "Spirited Away"},
	}
	got := Place(shows)
	if got[1].FranchiseID != 1 || got[1].SeasonNumber != 1 {
		t.Fatalf("root %+v", got[1])
	}
	if got[2].FranchiseID != 1 || got[2].SeasonNumber != 2 {
		t.Fatalf("sequel %+v", got[2])
	}
	if got[3].FranchiseID != 1 || got[3].SeasonNumber != 3 {
		t.Fatalf("third %+v", got[3])
	}
	if got[9].FranchiseID != 1 || got[9].SeasonNumber != 0 {
		t.Fatalf("ova %+v", got[9])
	}
	if got[199].FranchiseID != 199 || got[199].SeasonNumber != 0 {
		t.Fatalf("movie %+v", got[199])
	}
}

func TestPlaceBranchAndMissingPrequel(t *testing.T) {
	shows := []Show{
		{AniListID: 5, Format: "TV", StartYear: 2021, Season: "FALL", RelationsJSON: `[{"type":"PREQUEL","id":4,"format":"TV"},{"type":"SEQUEL","id":6,"format":"TV"}]`},
		{AniListID: 4, Format: "TV", StartYear: 2020, Season: "SPRING", RelationsJSON: `[{"type":"SEQUEL","id":5,"format":"TV"},{"type":"SEQUEL","id":7,"format":"TV"}]`},
		{AniListID: 7, Format: "TV", StartYear: 2020, Season: "WINTER", RelationsJSON: `[{"type":"PREQUEL","id":4,"format":"TV"}]`},
		{AniListID: 30, Format: "TV", StartYear: 2024, RelationsJSON: `[{"type":"PREQUEL","id":29,"format":"TV"}]`},
	}
	got := Place(shows)
	if got[7].FranchiseID != got[4].FranchiseID || got[7].SeasonNumber != 1 || got[4].SeasonNumber != 2 || got[5].SeasonNumber != 3 {
		t.Fatalf("branch %+v", got)
	}
	if got[30].FranchiseID != 30 || got[30].SeasonNumber != 1 {
		t.Fatalf("missing prequel %+v", got[30])
	}
}

func TestFileLayoutBookworm(t *testing.T) {
	root := Row{AniListID: 1, Title: "Ascendance of a Bookworm", Year: 2019, Format: "TV", FranchiseID: 1, SeasonNumber: 1, Episodes: 4, StartYear: 2019}
	second := Row{AniListID: 2, Title: "Bookworm 2", Year: 2020, Format: "TV", FranchiseID: 1, SeasonNumber: 2, Episodes: 3, StartYear: 2020}
	folder, _, format, season, special, ep := FileLayout(second, root, 1, nil)
	if folder != root.Title || format != "TV" || season != 2 || special || ep != 1 {
		t.Fatalf("folder %s format %s season %d special %v ep %d", folder, format, season, special, ep)
	}
	ova := Row{AniListID: 9, Title: "OVA", Format: "OVA", FranchiseID: 1, SeasonNumber: 0, Episodes: 1, StartYear: 2020}
	_, _, _, season, special, ep = FileLayout(ova, root, 1, []Row{ova})
	if season != 0 || !special || ep != 1 {
		t.Fatalf("ova season %d special %v ep %d", season, special, ep)
	}
}

func TestPlaceBridgesThroughSpecial(t *testing.T) {
	shows := []Show{
		{AniListID: 1, Format: "TV", RelationsJSON: `[{"type":"SEQUEL","id":2,"format":"SPECIAL"}]`},
		{AniListID: 2, Format: "SPECIAL", RelationsJSON: `[{"type":"PREQUEL","id":1,"format":"TV"},{"type":"SEQUEL","id":3,"format":"TV"}]`},
		{AniListID: 3, Format: "TV", RelationsJSON: `[{"type":"PREQUEL","id":2,"format":"SPECIAL"}]`},
	}
	got := Place(shows)
	if got[1].FranchiseID != 1 || got[3].FranchiseID != 1 || got[1].SeasonNumber != 1 || got[3].SeasonNumber != 2 {
		t.Fatalf("placement = %+v", got)
	}
}
