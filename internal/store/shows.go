package store

import (
	"context"
	"time"

	"animarr/internal/store/db"
)

// UpsertShow inserts or updates by AniList id.
// When preserveMonitored is set, an existing monitored flag is kept.
func (s *Store) UpsertShow(ctx context.Context, sh Show, preserveMonitored bool) (Show, error) {
	existing, err := s.GetShowByAniList(ctx, sh.AniListID)
	now := time.Now().UTC()
	if errorsIsNotFound(err) {
		row, err := s.q.InsertShow(ctx, insertShowParams(sh, now))
		if err != nil {
			return Show{}, err
		}
		return fromDBShow(row), nil
	}
	if err != nil {
		return Show{}, err
	}
	if preserveMonitored {
		sh.Monitored = existing.Monitored
	}
	if sh.PreferredResolution == "" {
		sh.PreferredResolution = existing.PreferredResolution
	}
	sh.ID = existing.ID
	if err := s.q.UpdateShow(ctx, updateShowParams(sh, now)); err != nil {
		return Show{}, err
	}
	return s.GetShow(ctx, existing.ID)
}

func errorsIsNotFound(err error) bool {
	return err != nil && err == ErrNotFound
}

// GetShow loads one show.
func (s *Store) GetShow(ctx context.Context, id int64) (Show, error) {
	row, err := s.q.GetShow(ctx, id)
	if err != nil {
		return Show{}, mapErr(err)
	}
	return fromDBShow(row), nil
}

// GetShowByAniList loads by AniList id.
func (s *Store) GetShowByAniList(ctx context.Context, anilistID int) (Show, error) {
	row, err := s.q.GetShowByAniList(ctx, int64(anilistID))
	if err != nil {
		return Show{}, mapErr(err)
	}
	return fromDBShow(row), nil
}

// ListShows returns shows for a filter: all, monitored, or airing.
func (s *Store) ListShows(ctx context.Context, filter string) ([]Show, error) {
	switch filter {
	case "monitored":
		rows, err := s.q.ListShowsMonitored(ctx)
		if err != nil {
			return nil, err
		}
		return mapMonitored(rows), nil
	case "airing":
		rows, err := s.q.ListShowsAiring(ctx, db.NewNullUnixTime(ptrTime(time.Now().UTC())))
		if err != nil {
			return nil, err
		}
		return mapAiring(rows), nil
	default:
		rows, err := s.q.ListShows(ctx)
		if err != nil {
			return nil, err
		}
		return mapAll(rows), nil
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func mapAll(rows []db.ListShowsRow) []Show {
	out := make([]Show, 0, len(rows))
	for _, row := range rows {
		out = append(out, showFromList(row.ID, row.AnilistID, row.TitleRomaji, row.TitleEnglish, row.TitleNative, row.Synonyms, row.Format, row.Status, row.ListStatus, row.Episodes, row.NextAiringEpisode, row.NextAiringAt, row.CoverImage, row.Season, row.SeasonYear, row.StartYear, row.StartMonth, row.StartDay, row.Monitored, row.PreferredResolution, row.RelationsJson, row.CreatedAt, row.UpdatedAt, row.Description, row.Genres, row.Studio, row.FranchiseID, row.SeasonNumber, row.TotalEps, row.Downloaded))
	}
	return out
}

func mapMonitored(rows []db.ListShowsMonitoredRow) []Show {
	out := make([]Show, 0, len(rows))
	for _, row := range rows {
		out = append(out, showFromList(row.ID, row.AnilistID, row.TitleRomaji, row.TitleEnglish, row.TitleNative, row.Synonyms, row.Format, row.Status, row.ListStatus, row.Episodes, row.NextAiringEpisode, row.NextAiringAt, row.CoverImage, row.Season, row.SeasonYear, row.StartYear, row.StartMonth, row.StartDay, row.Monitored, row.PreferredResolution, row.RelationsJson, row.CreatedAt, row.UpdatedAt, row.Description, row.Genres, row.Studio, row.FranchiseID, row.SeasonNumber, row.TotalEps, row.Downloaded))
	}
	return out
}

func mapAiring(rows []db.ListShowsAiringRow) []Show {
	out := make([]Show, 0, len(rows))
	for _, row := range rows {
		out = append(out, showFromList(row.ID, row.AnilistID, row.TitleRomaji, row.TitleEnglish, row.TitleNative, row.Synonyms, row.Format, row.Status, row.ListStatus, row.Episodes, row.NextAiringEpisode, row.NextAiringAt, row.CoverImage, row.Season, row.SeasonYear, row.StartYear, row.StartMonth, row.StartDay, row.Monitored, row.PreferredResolution, row.RelationsJson, row.CreatedAt, row.UpdatedAt, row.Description, row.Genres, row.Studio, row.FranchiseID, row.SeasonNumber, row.TotalEps, row.Downloaded))
	}
	return out
}

// SetMonitored toggles a show.
func (s *Store) SetMonitored(ctx context.Context, id int64, on bool) error {
	n, err := s.q.SetMonitored(ctx, db.SetMonitoredParams{
		Monitored: db.Bool(on),
		UpdatedAt: db.NewUnixTime(time.Now().UTC()),
		ID:        id,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetFranchise records which root entry this show belongs to and its library season.
// Season 0 is a standalone movie or a special inside a franchise. TV entries start at 1.
func (s *Store) SetFranchise(ctx context.Context, id int64, franchiseID, seasonNumber int) error {
	n, err := s.q.SetFranchise(ctx, db.SetFranchiseParams{
		FranchiseID:  int64(franchiseID),
		SeasonNumber: int64(seasonNumber),
		UpdatedAt:    db.NewUnixTime(time.Now().UTC()),
		ID:           id,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetResolution sets a per-show resolution override. Empty uses the global default.
func (s *Store) SetResolution(ctx context.Context, id int64, res string) error {
	n, err := s.q.SetResolution(ctx, db.SetResolutionParams{
		PreferredResolution: res,
		UpdatedAt:           db.NewUnixTime(time.Now().UTC()),
		ID:                  id,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
