package pipeline

import (
	"context"
	"strings"

	"animarr/internal/franchise"
	"animarr/internal/match"
	"animarr/internal/parser"
	"animarr/internal/store"
)

// numbering describes how release groups may count a show's episodes.
// AniList splits a long series into entries (a 2nd cour, a new season) that each
// count from 1, but many groups keep counting across them: S2 episode 6 of a show
// whose first season had 13 episodes is released as "19".
type numbering struct {
	// offset is the number of episodes in the seasons before this one.
	// It is 0 for a first season, or when an earlier season is missing or has an
	// unknown length, because the offset cannot be trusted then.
	offset int
	// prior holds the titles of those earlier seasons. Absolute-numbered releases
	// usually carry the series title, not the title of this entry.
	prior []string
}

// numberingFor derives the offset from the shows already stored for sh's franchise.
func (s *Service) numberingFor(ctx context.Context, sh store.Show) numbering {
	if sh.FranchiseID == 0 || sh.SeasonNumber <= 1 || !franchise.TV(sh.Format) {
		return numbering{}
	}
	all, err := s.Store.ListShows(ctx, "all")
	if err != nil {
		return numbering{}
	}
	return numberingOf(sh, all)
}

func numberingOf(sh store.Show, all []store.Show) numbering {
	if sh.FranchiseID == 0 || sh.SeasonNumber <= 1 || !franchise.TV(sh.Format) {
		return numbering{}
	}
	bySeason := map[int]store.Show{}
	for _, o := range all {
		if o.FranchiseID == sh.FranchiseID && franchise.TV(o.Format) && o.SeasonNumber > 0 && o.SeasonNumber < sh.SeasonNumber {
			bySeason[o.SeasonNumber] = o
		}
	}
	var nb numbering
	for n := 1; n < sh.SeasonNumber; n++ {
		o, ok := bySeason[n]
		if !ok || o.Episodes <= 0 {
			return numbering{}
		}
		nb.offset += o.Episodes
		nb.prior = append(nb.prior, o.AllTitles()...)
	}
	return nb
}

// episodeCap is the highest episode number sh can have in its own numbering, or 0 if unknown.
func episodeCap(sh store.Show) int {
	if sh.Episodes > 0 {
		return sh.Episodes
	}
	return sh.NextAiringEpisode
}

// absolute maps a release number counted across seasons to sh's own episode.
func (nb numbering) absolute(sh store.Show, n int) (int, bool) {
	if nb.offset <= 0 || n <= nb.offset {
		return 0, false
	}
	local := n - nb.offset
	if c := sh.Episodes; c > 0 && local > c {
		return 0, false
	}
	return local, true
}

// local maps the episode number in a release title to sh's own episode.
// A number that fits the show's own count is taken as it stands; one beyond it
// is read as counting on from earlier seasons.
func (nb numbering) local(sh store.Show, n int) (ep int, abs bool, ok bool) {
	if n < 1 {
		return 0, false, false
	}
	if c := episodeCap(sh); c == 0 || n <= c {
		return n, false, true
	}
	if a, ok := nb.absolute(sh, n); ok {
		return a, true, true
	}
	return 0, false, false
}

// titleOK reports whether a parsed title belongs to the show. A release whose
// number lies beyond the show's own count may use an earlier season's title.
func (nb numbering) titleOK(p parser.Parsed, sh store.Show, threshold float64) bool {
	cands := []match.Candidate{{Key: sh.ID, Titles: sh.AllTitles()}}
	if _, _, ok := match.Best(p.Title, cands, threshold); ok {
		return true
	}
	if nb.offset <= 0 || p.Episode <= nb.offset || len(nb.prior) == 0 {
		return false
	}
	_, _, ok := match.Best(p.Title, []match.Candidate{{Key: sh.ID, Titles: nb.prior}}, threshold)
	return ok
}

// resolveHit finds the show and episode a release belongs to when its title
// matched hit. A number beyond hit's own count moves to a later season of the
// same franchise that counts on from it.
func resolveHit(hit store.Show, n int, all []store.Show) (store.Show, int, bool) {
	nb := numberingOf(hit, all)
	if ep, _, ok := nb.local(hit, n); ok {
		return hit, ep, true
	}
	if hit.FranchiseID == 0 || !franchise.TV(hit.Format) {
		return store.Show{}, 0, false
	}
	for _, o := range all {
		if o.ID == hit.ID || o.FranchiseID != hit.FranchiseID || !franchise.TV(o.Format) || o.SeasonNumber <= hit.SeasonNumber {
			continue
		}
		if ep, ok := numberingOf(o, all).absolute(o, n); ok {
			return o, ep, true
		}
	}
	return store.Show{}, 0, false
}

func (nb numbering) coversEpisode(sh store.Show, p parser.Parsed, episode int) bool {
	if p.Batch {
		if spanCovers(p, episode) {
			return true
		}
		return nb.offset > 0 && spanCovers(p, episode+nb.offset)
	}
	if strings.EqualFold(sh.Format, "MOVIE") {
		return p.Episode == 0 || p.Episode == episode
	}
	if p.Episode == episode {
		return true
	}
	local, _, ok := nb.local(sh, p.Episode)
	return ok && local == episode
}
