package web

import (
	"strings"
	"testing"
	"time"

	"animarr/internal/store"
)

func TestSplitDetailAndFalseFailure(t *testing.T) {
	msg := `qBittorrent add failed: qbit add: {"added_torrent_ids":["abc"],"failure_count":0,"success_count":1}`
	summary, detail := splitDetail(msg)
	if summary != "qBittorrent add failed: qbit add" || !strings.HasPrefix(detail, "{") {
		t.Fatalf("split %q %q", summary, detail)
	}
	ev := presentOne(store.History{Level: "error", Message: msg}, nil)
	if ev.Level != "info" || ev.Summary != "qBittorrent accepted the torrent" || ev.Detail == "" || !ev.Grab {
		t.Fatalf("present %+v", ev)
	}
	plain := presentOne(store.History{Level: "warn", Message: "search failed: no index"}, nil)
	if plain.Level != "warn" || plain.Summary != "search failed: no index" || plain.Detail != "" {
		t.Fatalf("plain %+v", plain)
	}
	fail := `qBittorrent add failed: qbit add: {"failure_count":1,"success_count":0}`
	ev = presentOne(store.History{Level: "error", Message: fail}, nil)
	if ev.Level != "error" || !strings.Contains(ev.Summary, "add failed") || ev.Grab {
		t.Fatalf("real failure %+v", ev)
	}
	other := `refresh ok: {"success_count":1,"failure_count":0}`
	ev = presentOne(store.History{Level: "info", Message: other}, nil)
	if ev.Summary == "qBittorrent accepted the torrent" || ev.Level != "info" {
		t.Fatalf("unrelated payload %+v", ev)
	}
	search := presentOne(store.History{Level: "warn", Message: "search failed: no index"}, nil)
	if search.Grab || search.Level != "warn" {
		t.Fatalf("search %+v", search)
	}
	imported := presentOne(store.History{Level: "info", Message: "imported (hardlink) /lib/a.mkv"}, nil)
	if !imported.Grab || imported.Sync {
		t.Fatalf("import %+v", imported)
	}
	failedImport := presentOne(store.History{Level: "error", Message: "import failed: disk"}, nil)
	if failedImport.Grab || failedImport.Level != "error" {
		t.Fatalf("import failed %+v", failedImport)
	}
	sync := presentOne(store.History{Level: "info", Message: "synced 2 shows from AniList"}, nil)
	if !sync.Sync || sync.Grab {
		t.Fatalf("sync %+v", sync)
	}
	added := presentOne(store.History{Level: "info", Message: "added Demo Show"}, nil)
	if added.Sync || added.Grab {
		t.Fatalf("add %+v", added)
	}
}

func TestStaleQbitSearchIsNotAProblem(t *testing.T) {
	now := time.Date(2026, 11, 15, 23, 9, 0, 0, time.UTC)
	ok := `{"added_torrent_ids":["abc"],"failure_count":0,"success_count":1}`
	real := `{"failure_count":1,"success_count":0}`
	rows := []store.History{
		{At: now, Level: "warn", Message: "search failed: qbit add: " + ok},
		{At: now.Add(-time.Minute), Level: "error", Message: "qBittorrent add failed: qbit add: " + ok},
		{At: now.Add(-2 * time.Minute), Level: "warn", Message: "search failed: no index"},
		{At: now.Add(-3 * time.Minute), Level: "error", Message: "qBittorrent add failed: qbit add: " + real},
		{At: now.Add(-4 * time.Minute), Level: "warn", Message: "search failed: qbit add: " + real},
		{At: now.Add(-5 * time.Minute), Level: "info", Message: `refresh ok: {"success_count":1,"failure_count":0}`},
	}
	all, problems, count, _ := collectHistory(rows, nil, now)
	for _, ev := range all {
		if strings.Contains(ev.Summary, "search failed: qbit add") && strings.Contains(ev.Detail, `"success_count":1`) {
			t.Fatalf("stale search success kept: %+v", ev)
		}
	}
	var accepted, realAdd, realSearch, plain int
	for _, ev := range all {
		switch {
		case ev.Summary == "qBittorrent accepted the torrent":
			accepted++
			if ev.Level != "info" || !ev.Grab {
				t.Fatalf("accepted %+v", ev)
			}
		case strings.Contains(ev.Summary, "add failed"):
			realAdd++
			if ev.Level != "error" {
				t.Fatalf("real add %+v", ev)
			}
		case ev.Summary == "search failed: qbit add":
			realSearch++
			if ev.Level != "warn" || !strings.Contains(ev.Detail, `"failure_count":1`) {
				t.Fatalf("real search %+v", ev)
			}
		case ev.Summary == "search failed: no index":
			plain++
			if ev.Level != "warn" {
				t.Fatalf("plain %+v", ev)
			}
		}
	}
	if accepted != 1 || realAdd != 1 || realSearch != 1 || plain != 1 {
		t.Fatalf("rows accepted=%d add=%d search=%d plain=%d all=%d", accepted, realAdd, realSearch, plain, len(all))
	}
	if count != 3 || len(problems) != 3 {
		t.Fatalf("count %d problems %+v", count, problems)
	}
	for _, ev := range problems {
		if ev.Summary == "qBittorrent accepted the torrent" || strings.Contains(ev.Detail, `"success_count":1`) {
			t.Fatalf("problem list includes a success: %+v", ev)
		}
	}
}

func TestEpisodeFactsAndDayLabel(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.Local)
	past := now.Add(-48 * time.Hour)
	next := now.Add(48 * time.Hour)
	eps := []store.Episode{
		{Number: 1, State: store.StateDownloaded, AirDate: &past},
		{Number: 2, State: store.StateWanted, AirDate: &past},
		{Number: 3, State: store.StateUpcoming, AirDate: &next},
	}
	facts := episodeFacts(eps, store.Show{Status: "FINISHED"}, now)
	if facts.Aired != 2 || facts.Downloaded != 1 || facts.Missing != 1 || facts.Next != 3 || facts.Jump != 0 {
		t.Fatalf("facts %+v", facts)
	}
	if !strings.Contains(facts.line(), "2 aired · 1 downloaded · 1 missing · next: E03") {
		t.Fatalf("line %s", facts.line())
	}
	long := make([]store.Episode, 14)
	for i := range long {
		n := i + 1
		long[i] = store.Episode{Number: n, State: store.StateUpcoming, AirDate: &next}
	}
	long[0].State = store.StateWanted
	jumped := episodeFacts(long, store.Show{Status: "RELEASING"}, now)
	if jumped.Jump == 0 {
		t.Fatal("expected a jump target")
	}
	if dayLabel(now, now) != "Today" || dayLabel(now.Add(24*time.Hour), now) != "Tomorrow" {
		t.Fatalf("days %s %s", dayLabel(now, now), dayLabel(now.Add(24*time.Hour), now))
	}
	later := time.Date(2026, 11, 17, 18, 0, 0, 0, time.Local)
	if dayLabel(later, now) != later.Format("Mon 2 Jan") {
		t.Fatalf("later %s", dayLabel(later, now))
	}
}

func TestHumanStatus(t *testing.T) {
	if humanStatus("RELEASING") != "Airing" || humanStatus("CURRENT") != "Watching" || humanStatus("PLANNING") != "Plan to watch" {
		t.Fatal(humanStatus("RELEASING"), humanStatus("CURRENT"), humanStatus("PLANNING"))
	}
	if humanFormat("MOVIE") != "Movie" || humanFormat("TV") != "TV" {
		t.Fatal(humanFormat("MOVIE"), humanFormat("TV"))
	}
}
