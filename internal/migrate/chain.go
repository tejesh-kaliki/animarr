package migrate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"animarr/internal/anilist"
	"animarr/internal/franchise"
)

// seriesPlan is one Sonarr series after the AniList chain is known.
// Main is the TV/TV_SHORT entries in library order. Extras are movies and
// OVAs that share Season 00. They are not used to place Sonarr episode numbers.
type seriesPlan struct {
	Main        []chainEntry
	Extras      []chainEntry
	Media       map[int]anilist.Media
	RootTitle   string
	RootYear    int
	RootFormat  string
	FranchiseID int
	Ambiguous   string
	Rules       []rule
}

func (p seriesPlan) entry(id int) (chainEntry, bool) {
	for _, e := range p.Main {
		if e.ID == id {
			return e, true
		}
	}
	for _, e := range p.Extras {
		if e.ID == id {
			return e, true
		}
	}
	return chainEntry{}, false
}

func (p seriesPlan) describe() string {
	var b strings.Builder
	for i, e := range p.Main {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d %s %d eps season %d", e.ID, e.Title, e.Episodes, e.SeasonNumber)
	}
	if len(p.Extras) > 0 {
		b.WriteString("; specials")
		for _, e := range p.Extras {
			fmt.Fprintf(&b, " %d %s", e.ID, e.Title)
		}
	}
	return b.String()
}

func (p seriesPlan) allEntries() []chainEntry {
	out := make([]chainEntry, 0, len(p.Main)+len(p.Extras))
	out = append(out, p.Main...)
	out = append(out, p.Extras...)
	return out
}

func planSingle(m anilist.Media) seriesPlan {
	sn := 1
	if !franchise.TV(m.Format) {
		sn = 0
	}
	if !franchise.TV(m.Format) && !strings.EqualFold(m.Format, "MOVIE") {
		sn = 1
	}
	e := chainEntry{
		ID: m.ID, Title: m.DisplayTitle(), Episodes: m.EpisodeTotal(),
		Format: m.Format, SeasonNumber: sn, Status: m.Status,
	}
	return seriesPlan{
		Main:        []chainEntry{e},
		Media:       map[int]anilist.Media{m.ID: m},
		RootTitle:   m.DisplayTitle(),
		RootYear:    m.StartYear(),
		RootFormat:  m.Format,
		FranchiseID: m.ID,
	}
}

func planFromList(main, extras []anilist.Media) seriesPlan {
	p := seriesPlan{Media: map[int]anilist.Media{}}
	if len(main) == 0 {
		p.Ambiguous = "AniList chain is empty; not guessing"
		return p
	}
	root := main[0]
	p.RootTitle = root.DisplayTitle()
	p.RootYear = root.StartYear()
	p.RootFormat = root.Format
	p.FranchiseID = root.ID
	for i, m := range main {
		p.Media[m.ID] = m
		sn := i + 1
		if !franchise.TV(m.Format) {
			sn = 0
		}
		p.Main = append(p.Main, chainEntry{
			ID: m.ID, Title: m.DisplayTitle(), Episodes: m.EpisodeTotal(),
			Format: m.Format, SeasonNumber: sn, Status: m.Status,
		})
	}
	for _, m := range extras {
		if _, ok := p.Media[m.ID]; ok {
			continue
		}
		p.Media[m.ID] = m
		p.Extras = append(p.Extras, chainEntry{
			ID: m.ID, Title: m.DisplayTitle(), Episodes: m.EpisodeTotal(),
			Format: m.Format, SeasonNumber: 0, Status: m.Status,
		})
	}
	return p
}

func (r *Runner) planChain(ctx context.Context, start anilist.Media) (seriesPlan, error) {
	cache := map[int]anilist.Media{}
	full, err := r.getCached(ctx, start.ID, cache)
	if err != nil {
		return seriesPlan{}, err
	}
	if full.ID == 0 {
		full = start
	}
	if !franchise.TV(full.Format) {
		// A movie stays in its own folder. Sequels are not pulled in.
		// Any other non-TV match is one entry and is not walked into a TV chain.
		return planSingle(full), nil
	}
	chain, amb, err := r.buildTVChain(ctx, full, cache)
	if err != nil || amb != "" {
		return seriesPlan{Ambiguous: amb}, err
	}
	extras, err := r.collectExtras(ctx, chain, cache)
	if err != nil {
		return seriesPlan{}, err
	}
	return planFromList(chain, extras), nil
}

func (r *Runner) planOverride(ctx context.Context, spec overrideSpec) (seriesPlan, error) {
	if spec.Single > 0 {
		m, err := r.get(ctx, spec.Single)
		if err != nil {
			return seriesPlan{}, err
		}
		return planSingle(m), nil
	}
	var main []anilist.Media
	var extras []anilist.Media
	seen := map[int]bool{}
	cache := map[int]anilist.Media{}
	for _, rule := range spec.Rules {
		if rule.AniListID <= 0 || seen[rule.AniListID] {
			continue
		}
		m, err := r.getCached(ctx, rule.AniListID, cache)
		if err != nil {
			return seriesPlan{}, err
		}
		seen[m.ID] = true
		if franchise.TV(m.Format) {
			main = append(main, m)
		} else {
			extras = append(extras, m)
		}
	}
	if len(main) == 0 && len(extras) == 0 {
		return seriesPlan{Ambiguous: "override entries are empty; not guessing"}, nil
	}
	if len(main) == 0 {
		return planSingle(extras[0]), nil
	}
	p := planFromList(main, extras)
	p.Rules = spec.Rules
	return p, nil
}

func (r *Runner) buildTVChain(ctx context.Context, start anilist.Media, cache map[int]anilist.Media) ([]anilist.Media, string, error) {
	seenBack := map[int]bool{}
	cur := start
	for {
		nxt, ok, amb, err := r.linearNext(ctx, cur, "PREQUEL", cache)
		if err != nil || amb != "" {
			return nil, amb, err
		}
		if !ok {
			break
		}
		if nxt.ID == start.ID || seenBack[nxt.ID] {
			return nil, "prequel cycle; not guessing", nil
		}
		seenBack[nxt.ID] = true
		cur = nxt
		if len(seenBack) > 40 {
			return nil, "prequel chain is too long; not guessing", nil
		}
	}
	root := cur
	forward := []anilist.Media{root}
	seen := map[int]bool{root.ID: true}
	cur = root
	for {
		nxt, ok, amb, err := r.linearNext(ctx, cur, "SEQUEL", cache)
		if err != nil || amb != "" {
			return nil, amb, err
		}
		if !ok {
			break
		}
		if seen[nxt.ID] {
			return nil, "sequel cycle; not guessing", nil
		}
		seen[nxt.ID] = true
		forward = append(forward, nxt)
		cur = nxt
		if len(forward) > 40 {
			return nil, "sequel chain is too long; not guessing", nil
		}
	}
	if !seen[start.ID] {
		return nil, "sequel walk from the root did not include the matched entry; not guessing", nil
	}
	return forward, "", nil
}

// linearNext is the next TV entry in one direction.
// A direct TV SEQUEL or PREQUEL is that entry. Dr. STONE: STONE WARS (113936)
// has no TV sequel: its only SEQUEL is the SPECIAL "RYUSUI" (142876), and that
// special's SEQUEL is the TV series Dr. STONE New World (131518). One such
// non-TV entry is a bridge. It stays Season 00 and is not a numbered season.
// Two TV continuations, including one reached through a bridge, is a branch.
func (r *Runner) linearNext(ctx context.Context, cur anilist.Media, kind string, cache map[int]anilist.Media) (anilist.Media, bool, string, error) {
	tvs, bridges := splitDir(cur, kind)
	if len(tvs) > 1 {
		return anilist.Media{}, false, fmt.Sprintf("AniList %d has %d TV %s; not guessing", cur.ID, len(tvs), relWord(kind)), nil
	}
	var via []int
	if len(tvs) == 1 {
		via = []int{tvs[0].Node.ID}
	}
	explore := (len(tvs) == 0 && len(bridges) == 1) || (len(tvs) == 1 && len(bridges) > 0)
	if explore {
		found, amb, err := r.bridgeTargets(ctx, bridges, kind, cache, cur.ID)
		if err != nil || amb != "" {
			return anilist.Media{}, false, amb, err
		}
		for _, id := range found {
			via = appendUnique(via, id)
		}
	}
	if len(via) > 1 {
		return anilist.Media{}, false, fmt.Sprintf("AniList %d has a branching %s; not guessing", cur.ID, relSingular(kind)), nil
	}
	if len(via) == 0 {
		return anilist.Media{}, false, "", nil
	}
	nxt, err := r.getCached(ctx, via[0], cache)
	if err != nil {
		return anilist.Media{}, false, "", err
	}
	return nxt, true, "", nil
}

// bridgeTargets returns TV ids reached by walking each non-TV edge in one direction.
// Zero means that edge does not continue the numbered chain.
func (r *Runner) bridgeTargets(ctx context.Context, edges []anilist.RelationEdge, kind string, cache map[int]anilist.Media, origin int) ([]int, string, error) {
	seen := map[int]bool{origin: true}
	var out []int
	for _, e := range edges {
		id, amb, err := r.oneBridgeTV(ctx, e.Node.ID, kind, cache, seen, 0)
		if err != nil || amb != "" {
			return nil, amb, err
		}
		if id > 0 {
			out = appendUnique(out, id)
		}
	}
	return out, "", nil
}

func (r *Runner) oneBridgeTV(ctx context.Context, id int, kind string, cache map[int]anilist.Media, seen map[int]bool, depth int) (int, string, error) {
	if id <= 0 || seen[id] {
		return 0, "", nil
	}
	if depth > 5 {
		return 0, chainTooLong(kind), nil
	}
	seen[id] = true
	m, err := r.getCached(ctx, id, cache)
	if err != nil {
		return 0, "", err
	}
	tvs, bridges := splitDir(m, kind)
	if len(tvs) > 1 {
		return 0, fmt.Sprintf("AniList %d has %d TV %s; not guessing", m.ID, len(tvs), relWord(kind)), nil
	}
	if len(tvs) == 1 {
		tvID := tvs[0].Node.ID
		for _, b := range bridges {
			other, amb, err := r.oneBridgeTV(ctx, b.Node.ID, kind, cache, seen, depth+1)
			if err != nil || amb != "" {
				return 0, amb, err
			}
			if other != 0 && other != tvID {
				return 0, fmt.Sprintf("AniList %d has a branching %s; not guessing", m.ID, relSingular(kind)), nil
			}
		}
		return tvID, "", nil
	}
	if len(bridges) != 1 {
		return 0, "", nil
	}
	return r.oneBridgeTV(ctx, bridges[0].Node.ID, kind, cache, seen, depth+1)
}

func (r *Runner) collectExtras(ctx context.Context, chain []anilist.Media, cache map[int]anilist.Media) ([]anilist.Media, error) {
	inChain := map[int]bool{}
	for _, m := range chain {
		inChain[m.ID] = true
	}
	var ids []int
	seen := map[int]bool{}
	for _, m := range chain {
		for _, e := range extraRels(m) {
			if inChain[e.Node.ID] || seen[e.Node.ID] {
				continue
			}
			seen[e.Node.ID] = true
			ids = append(ids, e.Node.ID)
		}
	}
	var out []anilist.Media
	for _, id := range ids {
		m, err := r.getCached(ctx, id, cache)
		if err != nil {
			return nil, err
		}
		if franchise.TV(m.Format) {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].StartTime(), out[j].StartTime()
		if !a.Equal(b) {
			if a.IsZero() {
				return false
			}
			if b.IsZero() {
				return true
			}
			return a.Before(b)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *Runner) getCached(ctx context.Context, id int, cache map[int]anilist.Media) (anilist.Media, error) {
	if m, ok := cache[id]; ok && m.ID != 0 {
		return m, nil
	}
	m, err := r.get(ctx, id)
	if err != nil {
		return anilist.Media{}, err
	}
	cache[id] = m
	return m, nil
}

func splitDir(m anilist.Media, kind string) (tvs, bridges []anilist.RelationEdge) {
	for _, e := range m.Relations.Edges {
		if !strings.EqualFold(e.RelationType, kind) || e.Node.ID == 0 {
			continue
		}
		if e.Node.Type != "" && !strings.EqualFold(e.Node.Type, "ANIME") {
			continue
		}
		if franchise.TV(e.Node.Format) {
			tvs = append(tvs, e)
			continue
		}
		if bridgeFormat(e.Node.Format) {
			bridges = append(bridges, e)
		}
	}
	return tvs, bridges
}

// bridgeFormat is a non-TV anime a linear season chain may cross.
// An empty or unknown format is not a bridge and not a season.
func bridgeFormat(format string) bool {
	switch strings.ToUpper(strings.TrimSpace(format)) {
	case "MOVIE", "OVA", "SPECIAL", "ONA", "TV_SPECIAL", "MUSIC":
		return true
	default:
		return false
	}
}

func relWord(kind string) string {
	if strings.EqualFold(kind, "PREQUEL") {
		return "prequels"
	}
	return "sequels"
}

func relSingular(kind string) string {
	if strings.EqualFold(kind, "PREQUEL") {
		return "prequel"
	}
	return "sequel"
}

func chainTooLong(kind string) string {
	if strings.EqualFold(kind, "PREQUEL") {
		return "prequel chain is too long; not guessing"
	}
	return "sequel chain is too long; not guessing"
}

func appendUnique(dst []int, id int) []int {
	for _, v := range dst {
		if v == id {
			return dst
		}
	}
	return append(dst, id)
}

func extraRels(m anilist.Media) []anilist.RelationEdge {
	var out []anilist.RelationEdge
	for _, e := range m.Relations.Edges {
		if e.Node.ID == 0 {
			continue
		}
		if e.Node.Type != "" && !strings.EqualFold(e.Node.Type, "ANIME") {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(e.Node.Format)) {
		case "MOVIE", "OVA", "SPECIAL", "ONA", "TV_SPECIAL":
			out = append(out, e)
		}
	}
	return out
}
