package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"animarr/internal/store/db"
)

func mapErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func fromDBShow(row db.Show) Show {
	sh := Show{
		ID:                  row.ID,
		AniListID:           int(row.AnilistID),
		TitleRomaji:         row.TitleRomaji,
		TitleEnglish:        row.TitleEnglish,
		TitleNative:         row.TitleNative,
		Format:              row.Format,
		Status:              row.Status,
		ListStatus:          row.ListStatus,
		Episodes:            int(row.Episodes),
		NextAiringEpisode:   int(row.NextAiringEpisode),
		NextAiringAt:        row.NextAiringAt.Ptr(),
		CoverImage:          row.CoverImage,
		Season:              row.Season,
		SeasonYear:          int(row.SeasonYear),
		StartYear:           int(row.StartYear),
		Monitored:           bool(row.Monitored),
		PreferredResolution: row.PreferredResolution,
		RelationsJSON:       row.RelationsJson,
		Description:         row.Description,
		Studio:              row.Studio,
		FranchiseID:         int(row.FranchiseID),
		SeasonNumber:        int(row.SeasonNumber),
		CreatedAt:           row.CreatedAt.Time(),
		UpdatedAt:           row.UpdatedAt.Time(),
	}
	_ = json.Unmarshal([]byte(row.Synonyms), &sh.Synonyms)
	_ = json.Unmarshal([]byte(row.Genres), &sh.Genres)
	return sh
}

func showFromList(id int64, anilistID int64, romaji, english, native, synonyms, format, status, listStatus string, episodes, nextEp int64, next db.NullUnixTime, cover, season string, seasonYear, startYear int64, monitored db.Bool, resolution, relations string, created, updated db.UnixTime, description, genres, studio string, franchiseID, seasonNumber, total, downloaded int64) Show {
	sh := fromDBShow(db.Show{
		ID: id, AnilistID: anilistID, TitleRomaji: romaji, TitleEnglish: english, TitleNative: native,
		Synonyms: synonyms, Format: format, Status: status, ListStatus: listStatus,
		Episodes: episodes, NextAiringEpisode: nextEp, NextAiringAt: next, CoverImage: cover,
		Season: season, SeasonYear: seasonYear, StartYear: startYear, Monitored: monitored,
		PreferredResolution: resolution, RelationsJson: relations, CreatedAt: created, UpdatedAt: updated,
		Description: description, Genres: genres, Studio: studio, FranchiseID: franchiseID, SeasonNumber: seasonNumber,
	})
	sh.TotalEps = int(total)
	sh.Downloaded = int(downloaded)
	return sh
}

func insertShowParams(sh Show, now time.Time) db.InsertShowParams {
	return db.InsertShowParams{
		AnilistID:           int64(sh.AniListID),
		TitleRomaji:         sh.TitleRomaji,
		TitleEnglish:        sh.TitleEnglish,
		TitleNative:         sh.TitleNative,
		Synonyms:            marshalStrings(sh.Synonyms),
		Format:              sh.Format,
		Status:              sh.Status,
		ListStatus:          sh.ListStatus,
		Episodes:            int64(sh.Episodes),
		NextAiringEpisode:   int64(sh.NextAiringEpisode),
		NextAiringAt:        db.NewNullUnixTime(sh.NextAiringAt),
		CoverImage:          sh.CoverImage,
		Season:              sh.Season,
		SeasonYear:          int64(sh.SeasonYear),
		StartYear:           int64(sh.StartYear),
		Monitored:           db.Bool(sh.Monitored),
		PreferredResolution: sh.PreferredResolution,
		RelationsJson:       emptyJSON(sh.RelationsJSON),
		CreatedAt:           db.NewUnixTime(now),
		UpdatedAt:           db.NewUnixTime(now),
		Description:         sh.Description,
		Genres:              marshalStrings(sh.Genres),
		Studio:              sh.Studio,
	}
}

func updateShowParams(sh Show, now time.Time) db.UpdateShowParams {
	return db.UpdateShowParams{
		TitleRomaji:         sh.TitleRomaji,
		TitleEnglish:        sh.TitleEnglish,
		TitleNative:         sh.TitleNative,
		Synonyms:            marshalStrings(sh.Synonyms),
		Format:              sh.Format,
		Status:              sh.Status,
		ListStatus:          sh.ListStatus,
		Episodes:            int64(sh.Episodes),
		NextAiringEpisode:   int64(sh.NextAiringEpisode),
		NextAiringAt:        db.NewNullUnixTime(sh.NextAiringAt),
		CoverImage:          sh.CoverImage,
		Season:              sh.Season,
		SeasonYear:          int64(sh.SeasonYear),
		StartYear:           int64(sh.StartYear),
		Monitored:           db.Bool(sh.Monitored),
		PreferredResolution: sh.PreferredResolution,
		RelationsJson:       emptyJSON(sh.RelationsJSON),
		Description:         sh.Description,
		Genres:              marshalStrings(sh.Genres),
		Studio:              sh.Studio,
		UpdatedAt:           db.NewUnixTime(now),
		ID:                  sh.ID,
	}
}

func fromDBEpisode(row db.Episode) Episode {
	return Episode{
		ID:           row.ID,
		ShowID:       row.ShowID,
		Number:       int(row.Number),
		AirDate:      row.AirDate.Ptr(),
		State:        row.State,
		ReleaseTitle: row.ReleaseTitle,
		InfoHash:     row.InfoHash,
		FilePath:     row.FilePath,
		Score:        int(row.Score),
		Version:      int(row.Version),
		DownloadedAt: row.DownloadedAt.Ptr(),
		LastSearchAt: row.LastSearchAt.Ptr(),
	}
}

func marshalStrings(s []string) string {
	if s == nil {
		s = []string{}
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func emptyJSON(s string) string {
	if strings.TrimSpace(s) == "" {
		return "[]"
	}
	return s
}

func intPtrFrom64(p *int64) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

func int64Ptr(p *int) *int64 {
	if p == nil {
		return nil
	}
	v := int64(*p)
	return &v
}
