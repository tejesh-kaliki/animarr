package store

import (
	"context"
	"strings"
	"time"

	"animarr/internal/store/db"
)

// AddDownload records a grab.
func (s *Store) AddDownload(ctx context.Context, d Download) (int64, error) {
	if d.AddedAt.IsZero() {
		d.AddedAt = time.Now().UTC()
	}
	return s.q.InsertDownload(ctx, db.InsertDownloadParams{
		ShowID:   d.ShowID,
		Episode:  int64(d.Episode),
		InfoHash: strings.ToLower(d.InfoHash),
		Title:    d.Title,
		SavePath: d.SavePath,
		State:    d.State,
		Progress: int64(d.Progress),
		Score:    int64(d.Score),
		AddedAt:  db.NewUnixTime(d.AddedAt),
		Error:    d.Error,
	})
}

// ListDownloadsByState returns matching download rows, newest first.
func (s *Store) ListDownloadsByState(ctx context.Context, states []string) ([]Download, error) {
	if len(states) == 0 {
		return nil, nil
	}
	rows, err := s.q.ListDownloadsByState(ctx, states)
	if err != nil {
		return nil, err
	}
	out := make([]Download, 0, len(rows))
	for _, row := range rows {
		out = append(out, Download{
			ID:        row.ID,
			ShowID:    row.ShowID,
			Episode:   int(row.Episode),
			InfoHash:  row.InfoHash,
			Title:     row.Title,
			SavePath:  row.SavePath,
			State:     row.State,
			Progress:  int(row.Progress),
			Score:     int(row.Score),
			AddedAt:   row.AddedAt.Time(),
			Error:     row.Error,
			ShowTitle: row.ShowTitle,
		})
	}
	return out, nil
}

// UpdateDownload sets progress and state.
func (s *Store) UpdateDownload(ctx context.Context, id int64, state string, progress int, errMsg string) error {
	return s.q.UpdateDownload(ctx, db.UpdateDownloadParams{
		State:    state,
		Progress: int64(progress),
		Error:    errMsg,
		ID:       id,
	})
}

// AddHistory appends an activity line.
func (s *Store) AddHistory(ctx context.Context, h History) error {
	if h.At.IsZero() {
		h.At = time.Now().UTC()
	}
	return s.q.InsertHistory(ctx, db.InsertHistoryParams{
		At:      db.NewUnixTime(h.At),
		Level:   h.Level,
		Message: h.Message,
		ShowID:  h.ShowID,
		Episode: int64Ptr(h.Episode),
	})
}

// ListHistory returns the newest lines.
func (s *Store) ListHistory(ctx context.Context, limit int) ([]History, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.q.ListHistory(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	out := make([]History, 0, len(rows))
	for _, row := range rows {
		out = append(out, History{
			ID:      row.ID,
			At:      row.At.Time(),
			Level:   row.Level,
			Message: row.Message,
			ShowID:  row.ShowID,
			Episode: intPtrFrom64(row.Episode),
		})
	}
	return out, nil
}

// Dashboard collects the home page.
func (s *Store) Dashboard(ctx context.Context, now time.Time) (Dash, error) {
	var d Dash
	until := now.Add(7 * 24 * time.Hour)
	rows, err := s.q.ListAiring(ctx, db.ListAiringParams{
		State:   StateUpcoming,
		AirDate: db.NewNullUnixTime(&until),
	})
	if err != nil {
		return d, err
	}
	for _, row := range rows {
		at := row.At.Ptr()
		if at == nil {
			continue
		}
		d.Airing = append(d.Airing, Airing{
			ShowID:  row.ShowID,
			Title:   row.Title,
			Cover:   row.Cover,
			Episode: int(row.Episode),
			At:      *at,
		})
	}
	d.Active, err = s.ListDownloadsByState(ctx, []string{DlDownloading})
	if err != nil {
		return d, err
	}
	if len(d.Active) > 8 {
		d.Active = d.Active[:8]
	}
	d.Recent, err = s.ListDownloadsByState(ctx, []string{DlImported})
	if err != nil {
		return d, err
	}
	if len(d.Recent) > 8 {
		d.Recent = d.Recent[:8]
	}
	shows, err := s.q.CountShows(ctx)
	if err != nil {
		return d, err
	}
	d.Shows = int(shows)
	wanted, err := s.q.CountWantedMonitored(ctx, StateWanted)
	if err != nil {
		return d, err
	}
	d.Wanted = int(wanted)
	return d, nil
}
