// Package franchise groups AniList entries that belong to one series folder.
// TV seasons are Season 01, Season 02, ... in chain order. Related movies and
// OVAs share Season 00. A movie with no TV relation keeps its own folder.
package franchise

import (
	"encoding/json"
	"sort"
	"strings"
)

// Show is the subset of a stored show needed to place it.
type Show struct {
	AniListID     int
	Title         string
	Format        string
	StartYear     int
	Season        string
	Episodes      int
	RelationsJSON string
}

// Placement is where the show sits in the library.
// SeasonNumber 0 and a different franchise id means Season 00 inside that series.
// SeasonNumber 0 and the show's own id means a standalone movie folder.
type Placement struct {
	FranchiseID  int
	SeasonNumber int
}

// TV reports AniList formats that take a numbered season folder.
func TV(format string) bool {
	switch strings.ToUpper(strings.TrimSpace(format)) {
	case "TV", "TV_SHORT":
		return true
	default:
		return false
	}
}

type relation struct {
	Type   string `json:"type"`
	ID     int    `json:"id"`
	Format string `json:"format"`
}

type edge struct{ from, to int }

// Place assigns franchise ids and season numbers.
// A single SEQUEL/PREQUEL path keeps relation order. A branch or a cycle falls
// back to start year, then the AniList season name, then id. That fallback only
// numbers folders.
func Place(shows []Show) map[int]Placement {
	byID := make(map[int]Show, len(shows))
	for _, sh := range shows {
		if sh.AniListID > 0 {
			byID[sh.AniListID] = sh
		}
	}
	parent := map[int]int{}
	var find func(int) int
	find = func(id int) int {
		if parent[id] == 0 || parent[id] == id {
			parent[id] = id
			return id
		}
		parent[id] = find(parent[id])
		return parent[id]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}

	var edges []edge
	seenEdge := map[edge]bool{}
	add := func(from, to int) {
		if from <= 0 || to <= 0 || from == to {
			return
		}
		if _, ok := byID[from]; !ok {
			return
		}
		if _, ok := byID[to]; !ok {
			return
		}
		if !TV(byID[from].Format) || !TV(byID[to].Format) {
			return
		}
		e := edge{from, to}
		if seenEdge[e] {
			return
		}
		seenEdge[e] = true
		edges = append(edges, e)
		union(from, to)
	}
	for _, sh := range shows {
		if !TV(sh.Format) {
			continue
		}
		for _, rel := range parseRels(sh.RelationsJSON) {
			switch strings.ToUpper(rel.Type) {
			case "SEQUEL":
				add(sh.AniListID, rel.ID)
			case "PREQUEL":
				add(rel.ID, sh.AniListID)
			}
		}
	}

	// A special or OVA that sits between two TV entries (Dr. STONE: Stone Wars,
	// Ryuusui, New World) bridges them: the TV entries are consecutive seasons.
	prevOf := map[int][]int{}
	nextOf := map[int][]int{}
	for _, sh := range shows {
		for _, rel := range parseRels(sh.RelationsJSON) {
			switch strings.ToUpper(rel.Type) {
			case "SEQUEL":
				nextOf[sh.AniListID] = append(nextOf[sh.AniListID], rel.ID)
				prevOf[rel.ID] = append(prevOf[rel.ID], sh.AniListID)
			case "PREQUEL":
				prevOf[sh.AniListID] = append(prevOf[sh.AniListID], rel.ID)
				nextOf[rel.ID] = append(nextOf[rel.ID], sh.AniListID)
			}
		}
	}
	for _, mid := range shows {
		if TV(mid.Format) || mid.AniListID <= 0 {
			continue
		}
		for _, p := range prevOf[mid.AniListID] {
			for _, n := range nextOf[mid.AniListID] {
				add(p, n)
			}
		}
	}

	groups := map[int][]Show{}
	for _, sh := range shows {
		if !TV(sh.Format) || sh.AniListID <= 0 {
			continue
		}
		groups[find(sh.AniListID)] = append(groups[find(sh.AniListID)], sh)
	}
	out := map[int]Placement{}
	roots := map[int]int{}
	for _, members := range groups {
		order := orderGroup(members, edges)
		if len(order) == 0 {
			continue
		}
		rootID := order[0].AniListID
		for i, sh := range order {
			out[sh.AniListID] = Placement{FranchiseID: rootID, SeasonNumber: i + 1}
			roots[sh.AniListID] = rootID
		}
	}

	for _, sh := range shows {
		if sh.AniListID <= 0 || TV(sh.Format) {
			continue
		}
		fr := attachedFranchise(sh, shows, roots)
		if len(fr) == 1 {
			out[sh.AniListID] = Placement{FranchiseID: fr[0], SeasonNumber: 0}
			continue
		}
		if strings.EqualFold(sh.Format, "MOVIE") {
			out[sh.AniListID] = Placement{FranchiseID: sh.AniListID, SeasonNumber: 0}
			continue
		}
		out[sh.AniListID] = Placement{FranchiseID: sh.AniListID, SeasonNumber: 1}
	}
	return out
}

func attachedFranchise(sh Show, all []Show, roots map[int]int) []int {
	seen := map[int]bool{}
	var ids []int
	add := func(id int) {
		root, ok := roots[id]
		if !ok || seen[root] {
			return
		}
		seen[root] = true
		ids = append(ids, root)
	}
	for _, rel := range parseRels(sh.RelationsJSON) {
		add(rel.ID)
	}
	for _, other := range all {
		if other.AniListID == sh.AniListID || !TV(other.Format) {
			continue
		}
		for _, rel := range parseRels(other.RelationsJSON) {
			if rel.ID == sh.AniListID {
				add(other.AniListID)
			}
		}
	}
	sort.Ints(ids)
	return ids
}

func orderGroup(members []Show, edges []edge) []Show {
	if len(members) == 0 {
		return nil
	}
	ids := map[int]Show{}
	for _, m := range members {
		ids[m.AniListID] = m
	}
	out := map[int][]int{}
	in := map[int]int{}
	for id := range ids {
		in[id] = 0
	}
	for _, e := range edges {
		if _, ok := ids[e.from]; !ok {
			continue
		}
		if _, ok := ids[e.to]; !ok {
			continue
		}
		out[e.from] = append(out[e.from], e.to)
		in[e.to]++
	}
	linear := true
	root := 0
	roots := 0
	for id := range ids {
		if len(out[id]) > 1 || in[id] > 1 {
			linear = false
		}
		if in[id] == 0 {
			roots++
			root = id
		}
	}
	if roots != 1 {
		linear = false
	}
	var ordered []Show
	if linear {
		seen := map[int]bool{}
		cur := root
		for cur != 0 && !seen[cur] {
			ordered = append(ordered, ids[cur])
			seen[cur] = true
			next := 0
			if len(out[cur]) == 1 {
				next = out[cur][0]
			}
			cur = next
		}
		if len(ordered) != len(members) {
			linear = false
			ordered = nil
		}
	}
	if !linear {
		ordered = append([]Show(nil), members...)
		sort.Slice(ordered, func(i, j int) bool {
			a, b := ordered[i], ordered[j]
			if a.StartYear != b.StartYear {
				if a.StartYear == 0 {
					return false
				}
				if b.StartYear == 0 {
					return true
				}
				return a.StartYear < b.StartYear
			}
			if seasonRank(a.Season) != seasonRank(b.Season) {
				return seasonRank(a.Season) < seasonRank(b.Season)
			}
			return a.AniListID < b.AniListID
		})
	}
	return ordered
}

func seasonRank(s string) int {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "WINTER":
		return 1
	case "SPRING":
		return 2
	case "SUMMER":
		return 3
	case "FALL":
		return 4
	default:
		return 9
	}
}

func parseRels(raw string) []relation {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var list []relation
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil
	}
	return list
}

// Row is one show when building a file path.
type Row struct {
	AniListID    int
	Title        string
	Year         int
	Format       string
	FranchiseID  int
	SeasonNumber int
	Episodes     int
	StartYear    int
}

// FileLayout picks the folder title, library season, and filename episode.
// Standalone movies stay in `<Title> (Year)/`. Specials in a TV franchise share
// Season 00 and number episodes in start-year order so two one-episode OVAs
// do not both become S00E01.
func FileLayout(self, root Row, localEp int, peers []Row) (folder string, year int, format string, season int, special bool, fileEp int) {
	if localEp < 1 {
		localEp = 1
	}
	ownFranchise := self.FranchiseID == 0 || self.FranchiseID == self.AniListID
	if strings.EqualFold(self.Format, "MOVIE") && ownFranchise {
		return self.Title, self.Year, "MOVIE", 0, false, localEp
	}
	folder = root.Title
	if strings.TrimSpace(folder) == "" {
		folder = self.Title
	}
	if self.SeasonNumber <= 0 && !TV(self.Format) {
		return folder, self.Year, self.Format, 0, true, specialEpisode(self, localEp, peers)
	}
	sn := self.SeasonNumber
	if sn <= 0 {
		sn = 1
	}
	y := root.Year
	if y == 0 {
		y = self.Year
	}
	return folder, y, self.Format, sn, false, localEp
}

func specialEpisode(self Row, local int, peers []Row) int {
	var specs []Row
	seen := map[int]bool{}
	add := func(p Row) {
		if p.AniListID == 0 || seen[p.AniListID] || TV(p.Format) {
			return
		}
		if p.FranchiseID != self.FranchiseID || p.SeasonNumber > 0 {
			return
		}
		if p.FranchiseID == p.AniListID && strings.EqualFold(p.Format, "MOVIE") {
			return
		}
		seen[p.AniListID] = true
		specs = append(specs, p)
	}
	for _, p := range peers {
		add(p)
	}
	add(self)
	sort.Slice(specs, func(i, j int) bool {
		if specs[i].StartYear != specs[j].StartYear {
			if specs[i].StartYear == 0 {
				return false
			}
			if specs[j].StartYear == 0 {
				return true
			}
			return specs[i].StartYear < specs[j].StartYear
		}
		return specs[i].AniListID < specs[j].AniListID
	})
	base := 0
	for _, p := range specs {
		if p.AniListID == self.AniListID {
			break
		}
		n := p.Episodes
		if n < 1 {
			n = 1
		}
		base += n
	}
	return base + local
}
