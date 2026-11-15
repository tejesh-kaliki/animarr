package migrate

import (
	"fmt"
	"sort"
	"strings"

	"animarr/internal/franchise"
)

// chainEntry is one AniList entry in library order.
// Status is the AniList media status. A trailing NOT_YET_RELEASED entry may
// have Episodes 0 and still keep its season number.
type chainEntry struct {
	ID           int
	Title        string
	Episodes     int
	Format       string
	SeasonNumber int
	Status       string
}

// srcFile is one Sonarr file. Season 0 is a special and is never mapped.
type srcFile struct {
	Season  int
	Episode int
	Path    string
}

// placed is one file after mapping. Episode restarts at 1 on the AniList entry.
type placed struct {
	AniListID     int
	LibrarySeason int
	Episode       int
	SonarrSeason  int
	SonarrEpisode int
	Path          string
}

// mapResult is the plan for one Sonarr series.
// Mode is per-season, continuous, override, or ambiguous.
type mapResult struct {
	Mode     string
	Files    []placed
	Notes    []string
	Evidence string
}

type fileKey struct{ season, episode int }

// mapFiles chooses per-season or continuous numbering.
// An override with rules replaces both. Specials are listed and left unmapped.
// When the two heuristics disagree, or a file does not fit the chain, the
// result is ambiguous and the caller must not write it.
func mapFiles(entries []chainEntry, files []srcFile, rules []rule) mapResult {
	var notes []string
	var regular []srcFile
	for _, f := range files {
		if f.Season == 0 {
			notes = append(notes, fmt.Sprintf("special: S00E%02d not mapped (%s)", f.Episode, f.Path))
			continue
		}
		if f.Season < 1 || f.Episode < 1 {
			notes = append(notes, fmt.Sprintf("skip: S%02dE%02d has no usable episode number", f.Season, f.Episode))
			continue
		}
		regular = append(regular, f)
	}
	if len(rules) > 0 {
		return applyRules(entries, regular, rules, notes)
	}
	if len(entries) == 0 {
		return mapResult{Mode: "ambiguous", Notes: notes, Evidence: "AniList chain is empty; not guessing"}
	}
	if evidence := unknownCount(entries); evidence != "" {
		return mapResult{Mode: "ambiguous", Notes: notes, Evidence: evidence}
	}
	ps, psMissing := perSeason(entries, regular)
	triggered := continuousTriggered(entries, regular)
	if !triggered && len(psMissing) == 0 {
		return mapResult{Mode: "per-season", Files: values(ps), Notes: notes}
	}
	if !triggered {
		return mapResult{Mode: "ambiguous", Notes: notes, Evidence: explain(entries, regular, psMissing, "per-season cannot place every file")}
	}
	cm, cmMissing := continuous(entries, regular)
	if len(cmMissing) > 0 {
		return mapResult{Mode: "ambiguous", Notes: notes, Evidence: explain(entries, regular, cmMissing, "continuous numbering does not fit the chain")}
	}
	if dup, ok := collision(cm); ok {
		return mapResult{Mode: "ambiguous", Notes: notes, Evidence: explain(entries, regular, nil, "heuristics disagree: "+dup)}
	}
	if diff := disagree(ps, cm); diff != "" {
		return mapResult{Mode: "ambiguous", Notes: notes, Evidence: explain(entries, regular, nil, "heuristics disagree: "+diff)}
	}
	return mapResult{Mode: "continuous", Files: values(cm), Notes: notes}
}

// unknownCount rejects a missing episode count that sits before a later
// entry which has a count. A suffix of NOT_YET_RELEASED entries with no
// count stays in the chain (so the season number is kept) and adds nothing
// to the arithmetic: a zero total does not absorb episode numbers.
func unknownCount(entries []chainEntry) string {
	lastKnown := -1
	for i, e := range entries {
		if e.Episodes >= 1 {
			lastKnown = i
		}
	}
	for i, e := range entries {
		if e.Episodes >= 1 {
			continue
		}
		if i > lastKnown && strings.EqualFold(e.Status, "NOT_YET_RELEASED") {
			continue
		}
		return fmt.Sprintf("AniList %d %q has no episode count; not guessing", e.ID, e.Title)
	}
	return ""
}

func perSeason(entries []chainEntry, files []srcFile) (map[fileKey]placed, []srcFile) {
	out := map[fileKey]placed{}
	var missing []srcFile
	for _, f := range files {
		idx := f.Season - 1
		if idx < 0 || idx >= len(entries) {
			missing = append(missing, f)
			continue
		}
		e := entries[idx]
		if f.Episode > e.Episodes {
			missing = append(missing, f)
			continue
		}
		out[fileKey{f.Season, f.Episode}] = place(e, idx, f, f.Episode)
	}
	return out, missing
}

func continuousTriggered(entries []chainEntry, files []srcFile) bool {
	maxEp := map[int]int{}
	for _, f := range files {
		if f.Episode > maxEp[f.Season] {
			maxEp[f.Season] = f.Episode
		}
	}
	for season, max := range maxEp {
		idx := season - 1
		if idx < 0 || idx >= len(entries) {
			continue
		}
		if max > entries[idx].Episodes {
			return true
		}
	}
	return false
}

func continuous(entries []chainEntry, files []srcFile) (map[fileKey]placed, []srcFile) {
	overflow := map[int]bool{}
	maxEp := map[int]int{}
	for _, f := range files {
		if f.Episode > maxEp[f.Season] {
			maxEp[f.Season] = f.Episode
		}
	}
	for season, max := range maxEp {
		idx := season - 1
		if idx >= 0 && idx < len(entries) && max > entries[idx].Episodes {
			overflow[season] = true
		}
	}
	out := map[fileKey]placed{}
	var missing []srcFile
	for _, f := range files {
		if !overflow[f.Season] {
			idx := f.Season - 1
			if idx < 0 || idx >= len(entries) || f.Episode > entries[idx].Episodes {
				missing = append(missing, f)
				continue
			}
			out[fileKey{f.Season, f.Episode}] = place(entries[idx], idx, f, f.Episode)
			continue
		}
		e, local, idx, ok := placeAbsolute(f.Episode, entries)
		if !ok {
			missing = append(missing, f)
			continue
		}
		out[fileKey{f.Season, f.Episode}] = place(e, idx, f, local)
	}
	return out, missing
}

func placeAbsolute(ep int, entries []chainEntry) (chainEntry, int, int, bool) {
	if ep < 1 {
		return chainEntry{}, 0, 0, false
	}
	used := 0
	for i, e := range entries {
		if ep <= used+e.Episodes {
			return e, ep - used, i, true
		}
		used += e.Episodes
	}
	return chainEntry{}, 0, 0, false
}

func place(e chainEntry, index int, f srcFile, local int) placed {
	sn := e.SeasonNumber
	if sn <= 0 {
		if franchise.TV(e.Format) {
			sn = index + 1
		}
	}
	return placed{
		AniListID: e.ID, LibrarySeason: sn, Episode: local,
		SonarrSeason: f.Season, SonarrEpisode: f.Episode, Path: f.Path,
	}
}

func collision(files map[fileKey]placed) (string, bool) {
	seen := map[[2]int]fileKey{}
	for k, f := range files {
		key := [2]int{f.AniListID, f.Episode}
		if prev, ok := seen[key]; ok {
			return fmt.Sprintf("S%02dE%02d and S%02dE%02d both want AniList %d episode %d", prev.season, prev.episode, k.season, k.episode, f.AniListID, f.Episode), true
		}
		seen[key] = k
	}
	return "", false
}

func disagree(ps, cm map[fileKey]placed) string {
	for k, a := range ps {
		b, ok := cm[k]
		if !ok {
			continue
		}
		if a.AniListID != b.AniListID || a.Episode != b.Episode {
			return fmt.Sprintf("S%02dE%02d would be AniList %d episode %d by season and AniList %d episode %d by absolute count", k.season, k.episode, a.AniListID, a.Episode, b.AniListID, b.Episode)
		}
	}
	return ""
}

func values(m map[fileKey]placed) []placed {
	out := make([]placed, 0, len(m))
	for _, f := range m {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AniListID != out[j].AniListID {
			return out[i].AniListID < out[j].AniListID
		}
		if out[i].Episode != out[j].Episode {
			return out[i].Episode < out[j].Episode
		}
		if out[i].SonarrSeason != out[j].SonarrSeason {
			return out[i].SonarrSeason < out[j].SonarrSeason
		}
		return out[i].SonarrEpisode < out[j].SonarrEpisode
	})
	return out
}

func explain(entries []chainEntry, files, unmapped []srcFile, why string) string {
	seasons := map[int]bool{}
	for _, f := range files {
		seasons[f.Season] = true
	}
	var ss []int
	for s := range seasons {
		ss = append(ss, s)
	}
	sort.Ints(ss)
	var b strings.Builder
	if len(ss) >= 2 {
		fmt.Fprintf(&b, "multi-season: Sonarr seasons %s; ", joinInts(ss))
	}
	fmt.Fprintf(&b, "chain has %d entries (", len(entries))
	for i, e := range entries {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d %s %d eps", e.ID, e.Title, e.Episodes)
	}
	b.WriteString(")")
	if len(unmapped) > 0 {
		b.WriteString("; unmapped")
		sort.Slice(unmapped, func(i, j int) bool {
			if unmapped[i].Season != unmapped[j].Season {
				return unmapped[i].Season < unmapped[j].Season
			}
			return unmapped[i].Episode < unmapped[j].Episode
		})
		for _, f := range unmapped {
			fmt.Fprintf(&b, " S%02dE%02d", f.Season, f.Episode)
		}
	}
	if why != "" {
		b.WriteString("; ")
		b.WriteString(why)
	}
	b.WriteString("; not guessing")
	return b.String()
}

type rule struct {
	AniListID     int
	SonarrSeason  int
	EpisodeOffset int
}

func applyRules(entries []chainEntry, files []srcFile, rules []rule, notes []string) mapResult {
	seen := map[[2]int]int{}
	for _, r := range rules {
		k := [2]int{r.SonarrSeason, r.EpisodeOffset}
		if prev, ok := seen[k]; ok && prev != r.AniListID {
			return mapResult{Mode: "ambiguous", Notes: notes, Evidence: fmt.Sprintf("override has two entries for Sonarr season %d offset %d; not guessing", r.SonarrSeason, r.EpisodeOffset)}
		}
		seen[k] = r.AniListID
	}
	byID := map[int]chainEntry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	var out []placed
	for _, f := range files {
		var best *rule
		for i := range rules {
			r := &rules[i]
			if r.SonarrSeason != f.Season {
				continue
			}
			if f.Episode-r.EpisodeOffset < 1 {
				continue
			}
			if best == nil || r.EpisodeOffset > best.EpisodeOffset {
				best = r
			}
		}
		if best == nil {
			notes = append(notes, fmt.Sprintf("skip: S%02dE%02d matches no override rule", f.Season, f.Episode))
			continue
		}
		local := f.Episode - best.EpisodeOffset
		e, ok := byID[best.AniListID]
		if ok && e.Episodes > 0 && local > e.Episodes {
			notes = append(notes, fmt.Sprintf("skip: S%02dE%02d is episode %d of AniList %d which has %d episodes", f.Season, f.Episode, local, best.AniListID, e.Episodes))
			continue
		}
		if !ok {
			e = chainEntry{ID: best.AniListID, Episodes: local, Format: "TV", SeasonNumber: 1}
		}
		out = append(out, place(e, e.SeasonNumber-1, f, local))
	}
	seenFile := map[[2]int]placed{}
	for _, f := range out {
		key := [2]int{f.AniListID, f.Episode}
		if prev, ok := seenFile[key]; ok {
			return mapResult{Mode: "ambiguous", Notes: notes, Evidence: fmt.Sprintf("S%02dE%02d and S%02dE%02d both want AniList %d episode %d; not guessing", prev.SonarrSeason, prev.SonarrEpisode, f.SonarrSeason, f.SonarrEpisode, f.AniListID, f.Episode)}
		}
		seenFile[key] = f
	}
	return mapResult{Mode: "override", Files: out, Notes: notes}
}
