package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"animarr/internal/config"
	"animarr/internal/pipeline"
	"animarr/internal/secret"
	"animarr/internal/settings"
	"animarr/internal/store"
)

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	dash, err := s.Store.Dashboard(r.Context(), now)
	if err != nil {
		s.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	v := view{Title: "Dashboard", Nav: "home", Body: "dashboard", Dash: dash, AirGroups: groupAiring(dash.Airing, now)}
	s.attachActivity(r.Context(), &v, now, "")
	s.page(w, v)
}

func (s *Server) shows(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("filter")
	switch filter {
	case "", "all", "monitored", "airing":
	default:
		filter = "all"
	}
	if filter == "" {
		filter = "all"
	}
	sortKey := r.URL.Query().Get("sort")
	switch sortKey {
	case "title", "added", "next":
	default:
		sortKey = "next"
	}
	shows, err := s.Store.ListShows(r.Context(), filter)
	if err != nil {
		s.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	cards := s.cards(r.Context(), shows)
	sortShows(cards, sortKey)
	grid := libraryGrid(cards)
	sortGrid(grid, sortKey)
	s.page(w, view{Title: "Shows", Nav: "shows", Body: "shows", Shows: shows, Cards: cards, Grid: grid, Filter: filter, Sort: sortKey})
}

func (s *Server) franchise(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		s.fail(w, r, errBadID, http.StatusBadRequest)
		return
	}
	shows, err := s.Store.ListShows(r.Context(), "all")
	if err != nil {
		s.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	var members []store.Show
	for _, sh := range shows {
		if sh.FranchiseID == id {
			members = append(members, sh)
		}
	}
	if len(members) == 0 {
		s.fail(w, r, store.ErrNotFound, http.StatusNotFound)
		return
	}
	ordered := orderByStart(s.cards(r.Context(), members))
	root := ordered[0]
	for _, c := range ordered {
		if c.AniListID == id {
			root = c
			break
		}
	}
	entries := make([]franchiseEntry, len(ordered))
	downloaded, total := 0, 0
	for i, c := range ordered {
		entries[i] = franchiseEntry{
			showCard: c,
			Label:    seasonLabel(c.Show),
			Released: releaseMonth(c.Show),
		}
		downloaded += c.Downloaded
		total += c.TotalEps
	}
	n := len(entries)
	word := "seasons"
	if n == 1 {
		word = "season"
	}
	s.page(w, view{
		Title: root.DisplayTitle(), Nav: "shows", Body: "franchise",
		Show: root.Show, Entries: entries,
		EpSummary: fmt.Sprintf("%d %s · %d/%d eps", n, word, downloaded, total),
	})
}

func (s *Server) show(w http.ResponseWriter, r *http.Request) {
	sh, eps, err := s.loadShow(r)
	if err != nil {
		s.fail(w, r, err, statusFor(err))
		return
	}
	counts, err := s.Store.ListShows(r.Context(), "all")
	if err == nil {
		for _, c := range counts {
			if c.ID == sh.ID {
				sh.Downloaded = c.Downloaded
				sh.TotalEps = c.TotalEps
				break
			}
		}
	}
	now := time.Now()
	facts := episodeFacts(eps, sh, now)
	prog := s.progressFor(r.Context(), sh.ID)
	tok := s.csrfToken(w, r)
	note := s.grabNote(r)
	rows := make([]epView, len(eps))
	for i, ep := range eps {
		p, ok := prog[ep.Number]
		rows[i] = epView{
			Show: sh, Episode: ep, HasProgress: ok, Progress: p,
			Next: facts.Next > 0 && ep.Number == facts.Next,
			CSRF: tok, GrabOff: note != "",
		}
	}
	s.page(w, view{
		Title: sh.DisplayTitle(), Nav: "shows", Body: "show",
		Show: sh, Episodes: eps, Rows: rows, EpSummary: facts.line(), JumpEp: facts.Jump,
		AirClock: airClock(sh.NextAiringAt), Preferred: s.current(r).Score.PreferredResolution,
		CSRF: tok, GrabNote: note,
	})
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	v := view{Title: "Search", Nav: "search", Body: "search", Query: q, Library: s.libraryIDs(r.Context())}
	if q != "" {
		if s.Pipe == nil || s.Pipe.AniList == nil {
			v.Error = "AniList is not configured"
		} else {
			media, err := s.Pipe.Search(r.Context(), q)
			if err != nil {
				v.Error = err.Error()
			} else {
				v.Results = media
			}
		}
	}
	s.page(w, v)
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	level := r.URL.Query().Get("level")
	switch level {
	case "problems", "grabs", "syncs", "all":
	default:
		level = "all"
	}
	v := view{Title: "Activity", Nav: "activity", Body: "activity", Level: level}
	if err := s.attachActivity(r.Context(), &v, time.Now(), level); err != nil {
		s.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	s.page(w, v)
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	flash := ""
	switch r.URL.Query().Get("ok") {
	case "saved":
		flash = "Saved. Changes apply on the next sync, RSS poll, or download poll."
	case "reset":
		flash = "Reset. The environment value or the built-in default is in use again."
	}
	s.page(w, view{
		Title: "Settings", Nav: "settings", Body: "settings", Flash: flash,
		Settings: s.settingsPage(r), CSRF: s.csrfToken(w, r),
	})
}

func (s *Server) postSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, err, http.StatusBadRequest)
		return
	}
	if err := s.checkCSRF(r); err != nil {
		s.fail(w, r, err, http.StatusForbidden)
		return
	}
	if s.Pipe == nil || s.Pipe.Settings == nil {
		s.fail(w, r, errNoPipe, http.StatusInternalServerError)
		return
	}
	if key := strings.TrimSpace(r.FormValue("reset")); key != "" {
		if err := s.Pipe.Settings.Reset(r.Context(), key); err != nil {
			s.settingsFailed(w, r, err)
			return
		}
		http.Redirect(w, r, "/settings?ok=reset", http.StatusSeeOther)
		return
	}
	if err := s.Pipe.Settings.Save(r.Context(), r.Form); err != nil {
		s.settingsFailed(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings?ok=saved", http.StatusSeeOther)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if s.Store != nil {
		if err := s.Store.Ping(r.Context()); err != nil {
			http.Error(w, "db", http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) favicon(w http.ResponseWriter, r *http.Request) {
	b, err := assets.ReadFile("static/favicon.svg")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = w.Write(b)
}

func (s *Server) postSync(w http.ResponseWriter, r *http.Request) {
	if s.Pipe == nil {
		s.fail(w, r, errNoPipe, http.StatusInternalServerError)
		return
	}
	if err := s.Pipe.SyncAniList(r.Context()); err != nil {
		s.fail(w, r, err, http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) postAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, err, http.StatusBadRequest)
		return
	}
	id, err := strconv.Atoi(r.FormValue("anilist_id"))
	if err != nil || id <= 0 {
		s.fail(w, r, errBadID, http.StatusBadRequest)
		return
	}
	if r.FormValue("all_seasons") == "1" {
		_, err = s.Pipe.AddFranchise(r.Context(), id)
	} else {
		err = s.Pipe.AddShow(r.Context(), id)
	}
	if err != nil {
		s.fail(w, r, err, http.StatusBadGateway)
		return
	}
	sh, err := s.Store.GetShowByAniList(r.Context(), id)
	if err != nil {
		http.Redirect(w, r, "/shows", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/shows/"+strconv.FormatInt(sh.ID, 10), http.StatusSeeOther)
}

func (s *Server) postMonitored(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, err, http.StatusBadRequest)
		return
	}
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err, http.StatusBadRequest)
		return
	}
	on := r.FormValue("monitored") == "1" || r.FormValue("monitored") == "true"
	if err := s.Pipe.SetMonitored(r.Context(), id, on); err != nil {
		s.fail(w, r, err, statusFor(err))
		return
	}
	next := r.FormValue("next")
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/shows/" + strconv.FormatInt(id, 10)
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) postResolution(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, err, http.StatusBadRequest)
		return
	}
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err, http.StatusBadRequest)
		return
	}
	if err := s.Pipe.SetResolution(r.Context(), id, r.FormValue("resolution")); err != nil {
		s.fail(w, r, err, http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/shows/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) postEpisode(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			s.fail(w, r, err, http.StatusBadRequest)
			return
		}
		n, err := strconv.Atoi(r.PathValue("n"))
		if err != nil || n <= 0 {
			s.fail(w, r, errBadID, http.StatusBadRequest)
			return
		}
		var actErr error
		switch action {
		case "ignore":
			actErr = s.Pipe.IgnoreEpisode(r.Context(), id, n, true)
		case "unignore":
			actErr = s.Pipe.IgnoreEpisode(r.Context(), id, n, false)
		case "search":
			actErr = s.Pipe.SearchEpisode(r.Context(), id, n)
		case "redownload":
			actErr = s.Pipe.Redownload(r.Context(), id, n)
		default:
			s.fail(w, r, errBadID, http.StatusBadRequest)
			return
		}
		flash := ""
		if errors.Is(actErr, pipeline.ErrNoRelease) {
			flash = "No matching release"
			actErr = nil
		}
		if actErr != nil {
			s.fail(w, r, actErr, http.StatusBadGateway)
			return
		}
		if hx(r) {
			sh, err := s.Store.GetShow(r.Context(), id)
			if err != nil {
				s.fail(w, r, err, statusFor(err))
				return
			}
			ep, err := s.Store.GetEpisode(r.Context(), id, n)
			if err != nil {
				s.fail(w, r, err, statusFor(err))
				return
			}
			row := s.oneRow(r.Context(), sh, ep, flash)
			row.CSRF = s.csrfToken(w, r)
			row.GrabOff = s.grabNote(r) != ""
			s.render(w, "episode-row", row)
			return
		}
		http.Redirect(w, r, "/shows/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
	}
}

func (s *Server) postInteractive(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, err, http.StatusBadRequest)
		return
	}
	if err := s.checkCSRF(r); err != nil {
		s.fail(w, r, err, http.StatusForbidden)
		return
	}
	id, n, err := showEpisode(r)
	if err != nil {
		s.fail(w, r, err, http.StatusBadRequest)
		return
	}
	if s.Pipe == nil {
		s.fail(w, r, errNoPipe, http.StatusInternalServerError)
		return
	}
	rows, err := s.Pipe.ListChoices(r.Context(), id, n)
	panel := s.releasePanel(id, n, rows, "")
	panel.CSRF = s.csrfToken(w, r)
	panel.Note = s.grabNote(r)
	panel.GrabOff = panel.Note != ""
	if err != nil {
		panel.Error = err.Error()
		panel.Rows = nil
	}
	s.render(w, "release-panel", panel)
}

func (s *Server) postGrab(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, err, http.StatusBadRequest)
		return
	}
	if err := s.checkCSRF(r); err != nil {
		s.fail(w, r, err, http.StatusForbidden)
		return
	}
	id, n, err := showEpisode(r)
	if err != nil {
		s.fail(w, r, err, http.StatusBadRequest)
		return
	}
	if s.Pipe == nil {
		s.fail(w, r, errNoPipe, http.StatusInternalServerError)
		return
	}
	if note := s.grabNote(r); note != "" {
		s.fail(w, r, errors.New(note), http.StatusBadGateway)
		return
	}
	if err := s.Pipe.GrabChoice(r.Context(), id, n, r.FormValue("infohash")); err != nil {
		s.fail(w, r, err, http.StatusBadGateway)
		return
	}
	flash := "Sent to qBittorrent"
	if !s.current(r).AutomationEnabled {
		flash = "Sent to qBittorrent. The download poll is paused while automation is off, so the file imports after you turn it on."
	}
	if n > 0 && hx(r) {
		sh, err := s.Store.GetShow(r.Context(), id)
		if err != nil {
			s.fail(w, r, err, statusFor(err))
			return
		}
		ep, err := s.Store.GetEpisode(r.Context(), id, n)
		if err != nil {
			s.fail(w, r, err, statusFor(err))
			return
		}
		row := s.oneRow(r.Context(), sh, ep, flash)
		row.CSRF = s.csrfToken(w, r)
		row.GrabOff = s.grabNote(r) != ""
		s.render(w, "episode-row", row)
		return
	}
	if hx(r) {
		s.render(w, "release-panel", releasePanel{ShowID: id, Episode: n, Flash: flash, CSRF: s.csrfToken(w, r)})
		return
	}
	http.Redirect(w, r, "/shows/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) releasePanel(showID int64, episode int, rows []pipeline.Choice, errText string) releasePanel {
	now := time.Now()
	note := ""
	out := make([]choiceView, len(rows))
	for i, row := range rows {
		group := row.Parsed.Group
		if group == "" {
			group = "—"
		}
		res := row.Parsed.Resolution
		if res == "" {
			res = "—"
		}
		codec := strings.ToUpper(row.Parsed.Codec)
		if codec == "" {
			codec = "—"
		}
		ageUnix := int64(0)
		if !row.Release.PubDate.IsZero() {
			ageUnix = row.Release.PubDate.Unix()
		}
		out[i] = choiceView{
			Title: row.Release.Title, InfoHash: row.Release.InfoHash,
			Size: formatSize(row.Release.Size), Bytes: row.Release.Size,
			Peers:   fmt.Sprintf("%d / %d", row.Release.Seeders, row.Release.Leechers),
			Seeders: row.Release.Seeders, Leechers: row.Release.Leechers,
			Age: formatAge(row.Release.PubDate, now), AgeUnix: ageUnix,
			Group: group, Resolution: res, Codec: codec,
			Score: row.Score, Reasons: strings.Join(row.Reasons, " · "),
			Rejected: row.Rejected, Reject: row.Reject,
		}
	}
	return releasePanel{
		ShowID: showID, Episode: episode,
		Rows: out, Error: errText, Note: note, GrabOff: note != "",
	}
}

func showEpisode(r *http.Request) (int64, int, error) {
	id, err := pathID(r)
	if err != nil {
		return 0, 0, err
	}
	raw := r.PathValue("n")
	if raw == "" {
		return id, 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, 0, errBadID
	}
	return id, n, nil
}

func (s *Server) grabNote(r *http.Request) string {
	cfg := s.current(r)
	if s.Pipe == nil || s.Pipe.Qbit == nil || strings.TrimSpace(cfg.QBURL) == "" {
		return "qBittorrent is not configured. Interactive search can list releases, and Grab stays off until the WebUI URL is set in Settings."
	}
	return ""
}

func (s *Server) postTest(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, err, http.StatusBadRequest)
		return
	}
	if err := s.checkCSRF(r); err != nil {
		s.fail(w, r, err, http.StatusForbidden)
		return
	}
	cfg, previewErr := s.previewConfig(r)
	name := r.PathValue("name")
	var msg string
	var err error
	if previewErr != nil {
		err = previewErr
	} else if s.Pipe == nil {
		err = errNoPipe
	} else {
		switch name {
		case "qbit", "qbittorrent":
			msg, err = pipeline.CheckQbit(r.Context(), cfg)
		case "jellyfin":
			msg, err = pipeline.CheckJellyfin(r.Context(), cfg)
		case "nyaa":
			msg, err = pipeline.CheckNyaa(r.Context(), cfg)
		default:
			err = errBadID
		}
	}
	v := view{TestResult: msg, TestName: testID(name)}
	if err != nil {
		v.Error = s.redact(cfg, r, err)
		v.TestResult = ""
	}
	if hx(r) {
		// 200 so htmx swaps the result under the group heading.
		s.render(w, "test-out", v)
		return
	}
	code := 0
	if err != nil {
		code = http.StatusBadGateway
		var ve *settings.ValidationError
		if errors.As(err, &ve) {
			code = http.StatusBadRequest
		}
	}
	full := view{
		Title: "Settings", Nav: "settings", Body: "settings",
		Settings: s.settingsPage(r).WithPosted(r.Form), CSRF: s.csrfToken(w, r),
		TestResult: v.TestResult, TestName: v.TestName, Error: v.Error,
	}
	s.pageCode(w, code, full)
}

func (s *Server) loadShow(r *http.Request) (store.Show, []store.Episode, error) {
	id, err := pathID(r)
	if err != nil {
		return store.Show{}, nil, err
	}
	sh, err := s.Store.GetShow(r.Context(), id)
	if err != nil {
		return store.Show{}, nil, err
	}
	eps, err := s.Store.ListEpisodes(r.Context(), id)
	if err != nil {
		return store.Show{}, nil, err
	}
	return sh, eps, nil
}

func (s *Server) settingsPage(r *http.Request) settings.Page {
	if s.Pipe != nil && s.Pipe.Settings != nil {
		return s.Pipe.Settings.Page(r.Context())
	}
	return settings.PageFromConfig(s.Config)
}

func (s *Server) current(r *http.Request) config.Config {
	if s.Pipe != nil && s.Pipe.Settings != nil {
		cfg, err := s.Pipe.Settings.Current(r.Context())
		if err != nil {
			s.log().Error("settings", "err", err)
		}
		return cfg
	}
	return s.Config
}

func (s *Server) previewConfig(r *http.Request) (config.Config, error) {
	if s.Pipe != nil && s.Pipe.Settings != nil {
		return s.Pipe.Settings.Preview(r.Context(), r.Form)
	}
	return s.Config, nil
}

func (s *Server) settingsFailed(w http.ResponseWriter, r *http.Request, err error) {
	code := http.StatusBadRequest
	var ve *settings.ValidationError
	if !errors.As(err, &ve) {
		code = http.StatusInternalServerError
	}
	cfg, _ := s.previewConfig(r)
	s.pageCode(w, code, view{
		Title: "Settings", Nav: "settings", Body: "settings",
		Settings: s.settingsPage(r).WithPosted(r.Form),
		CSRF:     s.csrfToken(w, r),
		Error:    s.redact(cfg, r, err),
	})
}

func (s *Server) redact(cfg config.Config, r *http.Request, err error) string {
	if err == nil {
		return ""
	}
	vals := config.SecretValues(cfg)
	if r != nil && r.Form != nil {
		for _, spec := range config.Specs() {
			if spec.Kind == config.KindSecret {
				vals = append(vals, r.FormValue(spec.Key))
			}
		}
	}
	return secret.Redact(err.Error(), vals...)
}

func (s *Server) pageCode(w http.ResponseWriter, code int, v view) {
	if v.Title == "" {
		v.Title = "animarr"
	}
	s.fill(&v)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if code > 0 {
		w.WriteHeader(code)
	}
	if err := s.tmpl.ExecuteTemplate(w, "page", v); err != nil {
		s.log().Error("template", "err", err)
	}
}

func (s *Server) attachActivity(ctx context.Context, v *view, now time.Time, level string) error {
	if s.Store == nil {
		return nil
	}
	rows, err := s.Store.ListHistory(ctx, 200)
	if err != nil {
		return err
	}
	titles := map[int64]string{}
	if shows, err := s.Store.ListShows(ctx, "all"); err == nil {
		titles = titleMap(shows)
	}
	all, problems, count, synced := collectHistory(rows, titles, now)
	v.History = rows
	v.Problems = problems
	v.ProblemCount = count
	v.SyncedAt = synced
	if level == "" {
		v.Events = all
		return nil
	}
	v.Events = filterEvents(all, level)
	v.Level = level
	return nil
}

func (s *Server) cards(ctx context.Context, shows []store.Show) []showCard {
	out := make([]showCard, len(shows))
	for i, sh := range shows {
		card := showCard{Show: sh, NextLine: nextLine(sh)}
		if s.Store != nil {
			eps, err := s.Store.ListEpisodes(ctx, sh.ID)
			if err == nil {
				for _, ep := range eps {
					if ep.State == store.StateWanted {
						card.Missing++
					}
				}
			}
		}
		out[i] = card
	}
	return out
}

func sortGrid(cards []gridCard, sortKey string) {
	switch sortKey {
	case "title":
		slices.SortStableFunc(cards, func(a, b gridCard) int {
			return strings.Compare(strings.ToLower(a.DisplayTitle()), strings.ToLower(b.DisplayTitle()))
		})
	case "added":
		slices.SortStableFunc(cards, func(a, b gridCard) int {
			return b.CreatedAt.Compare(a.CreatedAt)
		})
	default:
		slices.SortStableFunc(cards, func(a, b gridCard) int {
			at, bt := a.NextAiringAt, b.NextAiringAt
			aMiss := at == nil || at.IsZero()
			bMiss := bt == nil || bt.IsZero()
			switch {
			case aMiss && bMiss:
				return strings.Compare(strings.ToLower(a.DisplayTitle()), strings.ToLower(b.DisplayTitle()))
			case aMiss:
				return 1
			case bMiss:
				return -1
			default:
				if c := at.Compare(*bt); c != 0 {
					return c
				}
				return strings.Compare(strings.ToLower(a.DisplayTitle()), strings.ToLower(b.DisplayTitle()))
			}
		})
	}
}

func sortShows(cards []showCard, sortKey string) {
	switch sortKey {
	case "title":
		slices.SortStableFunc(cards, func(a, b showCard) int {
			return strings.Compare(strings.ToLower(a.DisplayTitle()), strings.ToLower(b.DisplayTitle()))
		})
	case "added":
		slices.SortStableFunc(cards, func(a, b showCard) int {
			return b.CreatedAt.Compare(a.CreatedAt)
		})
	default:
		slices.SortStableFunc(cards, func(a, b showCard) int {
			at, bt := a.NextAiringAt, b.NextAiringAt
			aMiss := at == nil || at.IsZero()
			bMiss := bt == nil || bt.IsZero()
			switch {
			case aMiss && bMiss:
				return strings.Compare(strings.ToLower(a.DisplayTitle()), strings.ToLower(b.DisplayTitle()))
			case aMiss:
				return 1
			case bMiss:
				return -1
			default:
				if c := at.Compare(*bt); c != 0 {
					return c
				}
				return strings.Compare(strings.ToLower(a.DisplayTitle()), strings.ToLower(b.DisplayTitle()))
			}
		})
	}
}

func (s *Server) progressFor(ctx context.Context, showID int64) map[int]int {
	out := map[int]int{}
	if s.Store == nil {
		return out
	}
	rows, err := s.Store.ListDownloadsByState(ctx, []string{store.DlDownloading})
	if err != nil {
		return out
	}
	for _, d := range rows {
		if d.ShowID == showID && d.Progress > 0 {
			out[d.Episode] = d.Progress
		}
	}
	return out
}

func (s *Server) oneRow(ctx context.Context, sh store.Show, ep store.Episode, flash string) epView {
	p, ok := s.progressFor(ctx, sh.ID)[ep.Number]
	next := sh.NextAiringEpisode > 0 && ep.Number == sh.NextAiringEpisode
	if s.Store != nil {
		if eps, err := s.Store.ListEpisodes(ctx, sh.ID); err == nil {
			facts := episodeFacts(eps, sh, time.Now())
			next = facts.Next > 0 && facts.Next == ep.Number
		}
	}
	return epView{Show: sh, Episode: ep, Flash: flash, HasProgress: ok, Progress: p, Next: next}
}

func (s *Server) libraryIDs(ctx context.Context) map[int]int64 {
	out := map[int]int64{}
	if s.Store == nil {
		return out
	}
	shows, err := s.Store.ListShows(ctx, "all")
	if err != nil {
		return out
	}
	for _, sh := range shows {
		out[sh.AniListID] = sh.ID
	}
	return out
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errBadID
	}
	return id, nil
}

func statusFor(err error) int {
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, errBadID) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

var (
	errBadID  = errors.New("invalid id")
	errNoPipe = errors.New("pipeline is not configured")
)
