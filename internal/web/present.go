package web

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"animarr/internal/store"
)

// histView is one activity row after the raw message is split for display.
type histView struct {
	At         time.Time
	Level      string
	Summary    string
	Detail     string
	ShowID     int64
	ShowTitle  string
	Episode    int
	HasEpisode bool
	Grab       bool
	Sync       bool
}

type airGroup struct {
	Label string
	Items []airItem
}

type airItem struct {
	ShowID  int64
	Title   string
	Episode int
	Clock   string
	At      time.Time
}

type showCard struct {
	store.Show
	Missing  int
	NextLine string
}

// franchiseGroup is one franchise on the Shows page.
type franchiseGroup struct {
	Title      string
	Cards      []showCard
	Downloaded int
	TotalEps   int
}

// groupFranchises splits cards into franchises with more than one entry and
// standalone shows. Order follows the incoming sort. Seasons inside a group
// are ordered by season number.
func groupFranchises(cards []showCard) (groups []franchiseGroup, single []showCard) {
	byID := map[int]int{}
	var members [][]showCard
	for _, c := range cards {
		if c.FranchiseID == 0 {
			single = append(single, c)
			continue
		}
		i, ok := byID[c.FranchiseID]
		if !ok {
			i = len(members)
			byID[c.FranchiseID] = i
			members = append(members, nil)
		}
		members[i] = append(members[i], c)
	}
	for _, m := range members {
		if len(m) == 1 {
			single = append(single, m[0])
			continue
		}
		slices.SortStableFunc(m, func(a, b showCard) int {
			if a.SeasonNumber != b.SeasonNumber {
				return int(a.SeasonNumber - b.SeasonNumber)
			}
			return int(a.ID - b.ID)
		})
		g := franchiseGroup{Cards: m, Title: m[0].DisplayTitle()}
		for _, c := range m {
			if c.AniListID == c.FranchiseID {
				g.Title = c.DisplayTitle()
			}
			g.Downloaded += int(c.Downloaded)
			g.TotalEps += int(c.TotalEps)
		}
		groups = append(groups, g)
	}
	return groups, single
}

type epFacts struct {
	Aired, Downloaded, Missing int
	Next                       int
	NextAt                     *time.Time
	Jump                       int
}

func (f epFacts) line() string {
	text := fmt.Sprintf("%d aired · %d downloaded · %d missing", f.Aired, f.Downloaded, f.Missing)
	if f.Next <= 0 {
		return text
	}
	text += fmt.Sprintf(" · next: E%02d", f.Next)
	if f.NextAt != nil && !f.NextAt.IsZero() {
		text += " " + f.NextAt.Local().Format("Mon 2 Jan 15:04")
	}
	return text
}

func humanStatus(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "RELEASING":
		return "Airing"
	case "FINISHED":
		return "Finished"
	case "NOT_YET_RELEASED":
		return "Not yet aired"
	case "CANCELLED", "CANCELED":
		return "Cancelled"
	case "HIATUS":
		return "On hiatus"
	case "CURRENT":
		return "Watching"
	case "PLANNING":
		return "Plan to watch"
	case "COMPLETED":
		return "Completed"
	case "PAUSED":
		return "Paused"
	case "DROPPED":
		return "Dropped"
	case "":
		return ""
	default:
		return soften(s)
	}
}

func humanFormat(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "TV", "OVA", "ONA":
		return strings.ToUpper(strings.TrimSpace(s))
	case "TV_SHORT":
		return "TV short"
	case "MOVIE":
		return "Movie"
	case "SPECIAL":
		return "Special"
	case "MUSIC":
		return "Music"
	case "":
		return ""
	default:
		return humanStatus(s)
	}
}

func soften(s string) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "_", " ")), " ")
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	return strings.ToUpper(lower[:1]) + lower[1:]
}

func stateIcon(state string) string {
	switch state {
	case "downloaded":
		return "check"
	case "downloading":
		return "down"
	case "wanted":
		return "circle"
	case "upcoming":
		return "clock"
	case "ignored":
		return "slash"
	case "warn":
		return "triangle"
	case "error":
		return "triangle-fill"
	case "info":
		return "info"
	default:
		return "circle"
	}
}

func formatAirDay(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "—"
	}
	loc := t.Local()
	if loc.Year() == time.Now().Year() {
		return strings.TrimSpace(loc.Format("2 Jan"))
	}
	return loc.Format("2 Jan 2006")
}

func formatClock(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("15:04")
}

func formatRFC(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func airClock(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Local().Format("Mon 15:04")
}

func dayLabel(t, now time.Time) string {
	t = t.Local()
	now = now.Local()
	td := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	nd := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	switch {
	case td.Equal(nd):
		return "Today"
	case td.Equal(nd.AddDate(0, 0, 1)):
		return "Tomorrow"
	default:
		return t.Format("Mon 2 Jan")
	}
}

func groupAiring(items []store.Airing, now time.Time) []airGroup {
	var out []airGroup
	for _, it := range items {
		label := dayLabel(it.At, now)
		if len(out) == 0 || out[len(out)-1].Label != label {
			out = append(out, airGroup{Label: label})
		}
		g := &out[len(out)-1]
		g.Items = append(g.Items, airItem{
			ShowID: it.ShowID, Title: it.Title, Episode: it.Episode,
			Clock: it.At.Local().Format("15:04"), At: it.At,
		})
	}
	return out
}

func nextLine(sh store.Show) string {
	if sh.NextAiringEpisode > 0 && sh.NextAiringAt != nil && !sh.NextAiringAt.IsZero() {
		return fmt.Sprintf("Next E%02d · %s", sh.NextAiringEpisode, sh.NextAiringAt.Local().Format("Mon"))
	}
	if strings.EqualFold(sh.Status, "RELEASING") && sh.NextAiringEpisode > 0 {
		return fmt.Sprintf("Next E%02d", sh.NextAiringEpisode)
	}
	if strings.EqualFold(sh.Status, "FINISHED") || sh.Status == "" {
		return "Finished"
	}
	return humanStatus(sh.Status)
}

func episodeFacts(eps []store.Episode, sh store.Show, now time.Time) epFacts {
	var f epFacts
	var next *store.Episode
	for i := range eps {
		ep := eps[i]
		future := ep.State == store.StateUpcoming && (ep.AirDate == nil || ep.AirDate.After(now))
		if !future {
			f.Aired++
		}
		if ep.State == store.StateDownloaded {
			f.Downloaded++
		}
		if ep.State == store.StateWanted {
			f.Missing++
		}
		if future && (next == nil || ep.Number < next.Number) {
			cp := ep
			next = &cp
		}
	}
	if next != nil {
		f.Next = next.Number
		f.NextAt = next.AirDate
	} else if sh.NextAiringEpisode > 0 {
		f.Next = sh.NextAiringEpisode
		f.NextAt = sh.NextAiringAt
	}
	if strings.EqualFold(sh.Status, "RELEASING") && len(eps) > 13 && f.Next > 0 {
		f.Jump = f.Next
	}
	return f
}

func titleMap(shows []store.Show) map[int64]string {
	out := make(map[int64]string, len(shows))
	for _, sh := range shows {
		out[sh.ID] = sh.DisplayTitle()
	}
	return out
}

// splitDetail separates a plain sentence from a trailing JSON object or array.
func splitDetail(msg string) (string, string) {
	cut := -1
	for _, sep := range []string{": {", ": ["} {
		if i := strings.Index(msg, sep); i >= 0 && (cut < 0 || i < cut) {
			cut = i
		}
	}
	if cut < 0 {
		return strings.TrimSpace(msg), ""
	}
	return strings.TrimSpace(msg[:cut]), strings.TrimSpace(msg[cut+2:])
}

// addSucceeded reports a qBittorrent v5 add body that actually succeeded.
// Older history rows stored that body on an error line.
func addSucceeded(detail string) bool {
	detail = strings.TrimSpace(detail)
	if detail == "" || (detail[0] != '{' && detail[0] != '[') {
		return false
	}
	var body struct {
		Success int `json:"success_count"`
		Failure int `json:"failure_count"`
	}
	if json.Unmarshal([]byte(detail), &body) != nil {
		return false
	}
	return body.Success > 0 && body.Failure == 0
}

// falseQbitSuccess is an old error line whose payload accepted the torrent.
// The backlog logger also stored that payload as "search failed: qbit add …".
func falseQbitSuccess(message, detail string) bool {
	low := strings.ToLower(message)
	if !strings.Contains(low, "qbit add") && !(strings.Contains(low, "qbittorrent") && strings.Contains(low, "add failed")) {
		return false
	}
	return addSucceeded(detail)
}

// staleSearchSuccess is the backlog copy of an add that already succeeded.
// The paired "add failed" row is shown as accepted, so this one is dropped.
func staleSearchSuccess(message, detail string) bool {
	if !strings.Contains(strings.ToLower(message), "search failed") {
		return false
	}
	return falseQbitSuccess(message, detail)
}

func isGrab(msg string) bool {
	low := strings.ToLower(msg)
	return strings.Contains(low, "grabbed") ||
		strings.Contains(low, "imported") ||
		strings.Contains(low, "accepted the torrent")
}

func isSync(msg string) bool {
	low := strings.ToLower(msg)
	return strings.Contains(low, "synced") && strings.Contains(low, "anilist")
}

func presentOne(h store.History, titles map[int64]string) histView {
	summary, detail := splitDetail(h.Message)
	level := h.Level
	if falseQbitSuccess(h.Message, detail) && !staleSearchSuccess(h.Message, detail) {
		summary = "qBittorrent accepted the torrent"
		level = "info"
	}
	ev := histView{
		At: h.At, Level: level, Summary: summary, Detail: detail,
		Grab: isGrab(h.Message) || isGrab(summary),
		Sync: isSync(h.Message) || isSync(summary),
	}
	if h.ShowID != nil && *h.ShowID > 0 {
		ev.ShowID = *h.ShowID
		ev.ShowTitle = titles[ev.ShowID]
		if ev.ShowTitle == "" {
			ev.ShowTitle = "Show"
		}
	}
	if h.Episode != nil && *h.Episode > 0 {
		ev.Episode = *h.Episode
		ev.HasEpisode = true
	}
	return ev
}

func collectHistory(rows []store.History, titles map[int64]string, now time.Time) (all, problems []histView, count int, synced time.Time) {
	cutoff := now.Add(-24 * time.Hour)
	for _, row := range rows {
		_, detail := splitDetail(row.Message)
		if staleSearchSuccess(row.Message, detail) {
			continue
		}
		ev := presentOne(row, titles)
		all = append(all, ev)
		if ev.Level == "error" || ev.Level == "warn" {
			if !row.At.Before(cutoff) {
				count++
			}
			if len(problems) < 3 {
				problems = append(problems, ev)
			}
		}
		if synced.IsZero() && isSync(row.Message) {
			synced = row.At
		}
	}
	return all, problems, count, synced
}

func filterEvents(all []histView, level string) []histView {
	switch level {
	case "problems", "grabs", "syncs":
	default:
		return all
	}
	out := make([]histView, 0, len(all))
	for _, ev := range all {
		switch level {
		case "problems":
			if ev.Level == "error" || ev.Level == "warn" {
				out = append(out, ev)
			}
		case "grabs":
			if ev.Grab {
				out = append(out, ev)
			}
		case "syncs":
			if ev.Sync {
				out = append(out, ev)
			}
		}
	}
	return out
}

func srcTitle(source string) string {
	switch source {
	case "saved":
		return "Saved in this app. It overrides the environment, CONFIG_FILE, and the built-in default."
	case "env":
		return "From the environment. It overrides CONFIG_FILE and the built-in default."
	case "file":
		return "From CONFIG_FILE. It overrides the built-in default."
	default:
		return "Built-in default. A saved value or the environment replaces it."
	}
}

func revertLabel(fallback string) string {
	switch fallback {
	case "env":
		return "Revert to env"
	case "file":
		return "Revert to file"
	default:
		return "Revert to default"
	}
}

func testID(name string) string {
	switch name {
	case "qbittorrent", "qbit":
		return "qbit"
	default:
		return name
	}
}
