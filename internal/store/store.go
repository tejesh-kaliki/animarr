// Package store is the SQLite database. It uses modernc.org/sqlite so the binary stays CGO-free.
// SQL lives in sql/queries and is generated into internal/store/db. Store is the facade the rest of the app calls.
package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"animarr/internal/store/db"

	_ "modernc.org/sqlite"
)

const (
	StateUpcoming    = "upcoming"
	StateWanted      = "wanted"
	StateDownloading = "downloading"
	StateDownloaded  = "downloaded"
	StateIgnored     = "ignored"

	DlDownloading = "downloading"
	DlImported    = "imported"
	DlError       = "error"
	DlSuperseded  = "superseded"
)

// ErrNotFound is a missing show or episode.
var ErrNotFound = errors.New("not found")

// Show is one AniList entry. Downloaded and TotalEps are filled by list queries.
type Show struct {
	ID                  int64
	AniListID           int
	TitleRomaji         string
	TitleEnglish        string
	TitleNative         string
	Synonyms            []string
	Format              string
	Status              string
	ListStatus          string
	Episodes            int
	NextAiringEpisode   int
	NextAiringAt        *time.Time
	CoverImage          string
	Season              string
	SeasonYear          int
	StartYear           int
	Monitored           bool
	PreferredResolution string
	RelationsJSON       string
	Description         string
	Genres              []string
	Studio              string
	FranchiseID         int
	SeasonNumber        int
	Downloaded          int
	TotalEps            int
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// DisplayTitle prefers English, then romaji, then native.
func (s Show) DisplayTitle() string {
	if strings.TrimSpace(s.TitleEnglish) != "" {
		return s.TitleEnglish
	}
	if strings.TrimSpace(s.TitleRomaji) != "" {
		return s.TitleRomaji
	}
	if strings.TrimSpace(s.TitleNative) != "" {
		return s.TitleNative
	}
	return "Unknown"
}

// AllTitles is every name the matcher should see.
func (s Show) AllTitles() []string {
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		for _, e := range out {
			if strings.EqualFold(e, v) {
				return
			}
		}
		out = append(out, v)
	}
	add(s.TitleRomaji)
	add(s.TitleEnglish)
	add(s.TitleNative)
	for _, v := range s.Synonyms {
		add(v)
	}
	return out
}

// ProgressPct is downloaded / total, or 0 when there are no episodes.
func (s Show) ProgressPct() int {
	if s.TotalEps <= 0 {
		return 0
	}
	return s.Downloaded * 100 / s.TotalEps
}

// Episode is one episode row.
type Episode struct {
	ID           int64
	ShowID       int64
	Number       int
	AirDate      *time.Time
	State        string
	ReleaseTitle string
	InfoHash     string
	FilePath     string
	Score        int
	Version      int
	DownloadedAt *time.Time
	LastSearchAt *time.Time
}

// EpisodeWithShow joins an episode to its show.
type EpisodeWithShow struct {
	Episode Episode
	Show    Show
}

// Download is one qBittorrent grab.
type Download struct {
	ID        int64
	ShowID    int64
	ShowTitle string
	Episode   int
	InfoHash  string
	Title     string
	SavePath  string
	State     string
	Progress  int
	Score     int
	AddedAt   time.Time
	Error     string
}

// History is one activity line.
type History struct {
	ID      int64
	At      time.Time
	Level   string
	Message string
	ShowID  *int64
	Episode *int
}

// Airing is an upcoming episode on the dashboard.
type Airing struct {
	ShowID  int64
	Title   string
	Cover   string
	Episode int
	At      time.Time
}

// Dash is the dashboard payload.
type Dash struct {
	Airing []Airing
	Active []Download
	Recent []Download
	Shows  int
	Wanted int
}

// Store is one database connection pool.
type Store struct {
	db *sql.DB
	q  *db.Queries
}

// Open creates the file and applies numbered SQL migrations.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(2)
	s := &Store{db: sqlDB, q: db.New(sqlDB)}
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks the connection.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
