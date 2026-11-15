package web

import (
	"testing"

	"animarr/internal/store"
)

func TestGroupFranchises(t *testing.T) {
	mk := func(id int64, anilist, fr, season int) showCard {
		return showCard{Show: store.Show{ID: id, AniListID: anilist, FranchiseID: fr, SeasonNumber: season}}
	}
	cards := []showCard{mk(1, 20, 10, 2), mk(2, 10, 10, 1), mk(3, 30, 30, 1), mk(4, 40, 0, 1)}
	groups, single := groupFranchises(cards)
	if len(groups) != 1 || len(groups[0].Cards) != 2 {
		t.Fatalf("groups = %+v", groups)
	}
	if groups[0].Cards[0].ID != 2 {
		t.Fatalf("seasons not ordered: %+v", groups[0].Cards)
	}
	if len(single) != 2 {
		t.Fatalf("single = %d", len(single))
	}
}
