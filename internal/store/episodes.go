package store

import (
	"context"
	"time"

	"animarr/internal/store/db"
)

// EnsureEpisode inserts a row or refreshes a non-terminal one.
// downloaded, downloading, and ignored rows keep their state. Air dates update when known.
func (s *Store) EnsureEpisode(ctx context.Context, showID int64, number int, air *time.Time, desired string) error {
	existing, err := s.GetEpisode(ctx, showID, number)
	if errorsIsNotFound(err) {
		return s.q.InsertEpisode(ctx, db.InsertEpisodeParams{
			ShowID:  showID,
			Number:  int64(number),
			AirDate: db.NewNullUnixTime(air),
			State:   desired,
		})
	}
	if err != nil {
		return err
	}
	state := existing.State
	switch state {
	case StateDownloaded, StateDownloading, StateIgnored:
	default:
		state = desired
	}
	if air == nil {
		air = existing.AirDate
	}
	return s.q.UpdateEpisodeStateAndAir(ctx, db.UpdateEpisodeStateAndAirParams{
		State:   state,
		AirDate: db.NewNullUnixTime(air),
		ShowID:  showID,
		Number:  int64(number),
	})
}

// ListEpisodes returns episodes in order.
func (s *Store) ListEpisodes(ctx context.Context, showID int64) ([]Episode, error) {
	rows, err := s.q.ListEpisodesByShow(ctx, showID)
	if err != nil {
		return nil, err
	}
	out := make([]Episode, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromDBEpisode(row))
	}
	return out, nil
}

// GetEpisode loads one episode.
func (s *Store) GetEpisode(ctx context.Context, showID int64, number int) (Episode, error) {
	row, err := s.q.GetEpisode(ctx, db.GetEpisodeParams{ShowID: showID, Number: int64(number)})
	if err != nil {
		return Episode{}, mapErr(err)
	}
	return fromDBEpisode(row), nil
}

// UpdateEpisode writes the mutable grab fields.
func (s *Store) UpdateEpisode(ctx context.Context, ep Episode) error {
	return s.q.UpdateEpisode(ctx, db.UpdateEpisodeParams{
		State:        ep.State,
		ReleaseTitle: ep.ReleaseTitle,
		InfoHash:     ep.InfoHash,
		FilePath:     ep.FilePath,
		Score:        int64(ep.Score),
		Version:      int64(ep.Version),
		DownloadedAt: db.NewNullUnixTime(ep.DownloadedAt),
		LastSearchAt: db.NewNullUnixTime(ep.LastSearchAt),
		ShowID:       ep.ShowID,
		Number:       int64(ep.Number),
	})
}

// SetEpisodeState changes only the state.
func (s *Store) SetEpisodeState(ctx context.Context, showID int64, number int, state string) error {
	n, err := s.q.SetEpisodeState(ctx, db.SetEpisodeStateParams{
		State:  state,
		ShowID: showID,
		Number: int64(number),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchSearch records a search attempt.
func (s *Store) TouchSearch(ctx context.Context, showID int64, number int, at time.Time) error {
	return s.q.TouchEpisodeSearch(ctx, db.TouchEpisodeSearchParams{
		LastSearchAt: db.NewNullUnixTime(&at),
		ShowID:       showID,
		Number:       int64(number),
	})
}

// WantedBacklog lists monitored wanted episodes that are old enough to search directly.
func (s *Store) WantedBacklog(ctx context.Context, airedBefore, searchedBefore time.Time, limit int) ([]EpisodeWithShow, error) {
	rows, err := s.q.WantedBacklog(ctx, db.WantedBacklogParams{
		State:        StateWanted,
		AirDate:      db.NewNullUnixTime(&airedBefore),
		LastSearchAt: db.NewNullUnixTime(&searchedBefore),
		Limit:        int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]EpisodeWithShow, 0, len(rows))
	for _, row := range rows {
		ep := fromDBEpisode(db.Episode{
			ID: row.ID, ShowID: row.ShowID, Number: row.Number, AirDate: row.AirDate, State: row.State,
			ReleaseTitle: row.ReleaseTitle, InfoHash: row.InfoHash, FilePath: row.FilePath,
			Score: row.Score, Version: row.Version, DownloadedAt: row.DownloadedAt, LastSearchAt: row.LastSearchAt,
		})
		sh := showFromList(row.ShowPk, row.AnilistID, row.TitleRomaji, row.TitleEnglish, row.TitleNative, row.Synonyms, row.Format, row.Status, row.ListStatus, row.Episodes, row.NextAiringEpisode, row.NextAiringAt, row.CoverImage, row.Season, row.SeasonYear, row.StartYear, row.Monitored, row.PreferredResolution, row.RelationsJson, row.CreatedAt, row.UpdatedAt, row.Description, row.Genres, row.Studio, row.FranchiseID, row.SeasonNumber, 0, 0)
		out = append(out, EpisodeWithShow{Episode: ep, Show: sh})
	}
	return out, nil
}

// PromoteAired moves upcoming episodes whose air time has passed to wanted.
func (s *Store) PromoteAired(ctx context.Context, now time.Time) (int, error) {
	n, err := s.q.PromoteAired(ctx, db.PromoteAiredParams{
		State:   StateWanted,
		State_2: StateUpcoming,
		AirDate: db.NewNullUnixTime(&now),
	})
	if err != nil {
		return 0, err
	}
	return int(n), nil
}
