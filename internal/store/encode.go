package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"animarr/internal/store/db"
)

const (
	EncodePending = "pending"
	EncodeRunning = "running"
	EncodeDone    = "done"
	EncodeFailed  = "failed"
	EncodeSkipped = "skipped"

	// EncodeLease is how long a claim stays running without a heartbeat.
	EncodeLease = 2 * time.Minute
)

// EncodeJob is one episode's encode row, with the show title filled on lists.
type EncodeJob struct {
	ID         int64
	EpisodeID  int64
	ShowID     int64
	Episode    int
	Title      string
	FilePath   string
	State      string
	Codec      string
	Encoder    string
	SrcSize    int64
	DstSize    int64
	Error      string
	Progress   float64
	FPS        float64
	Speed      float64
	LeaseUntil *time.Time
	StartedAt  *time.Time
	UpdatedAt  time.Time
}

// EncodeFinish is the worker's report for a claimed job.
type EncodeFinish struct {
	State    string
	FilePath string
	Codec    string
	Encoder  string
	SrcSize  int64
	DstSize  int64
	Error    string
	Progress float64
	Speed    float64
}

// EncodeProgress is a heartbeat from the worker.
type EncodeProgress struct {
	Encoder  string
	Progress float64
	FPS      float64
	Speed    float64
}

// EnqueueEncode queues an episode file. A running job is left alone.
// A done job for the same path is left alone. Other states go back to pending.
// added is true when a new pending row was created or an old one was reset.
func (s *Store) EnqueueEncode(ctx context.Context, episodeID int64, path string, now time.Time) (bool, error) {
	path = strings.TrimSpace(path)
	if episodeID <= 0 || path == "" {
		return false, nil
	}
	now = now.UTC()
	existing, err := s.q.GetEncodeByEpisode(ctx, episodeID)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.q.InsertEncode(ctx, db.InsertEncodeParams{
			EpisodeID: episodeID,
			FilePath:  path,
			CreatedAt: db.NewUnixTime(now),
			UpdatedAt: db.NewUnixTime(now),
		})
		return err == nil, err
	}
	if err != nil {
		return false, err
	}
	if existing.State == EncodeRunning {
		return false, nil
	}
	if existing.FilePath == path && (existing.State == EncodeDone || existing.State == EncodePending || existing.State == EncodeSkipped) {
		return false, nil
	}
	n, err := s.q.ResetEncodePending(ctx, db.ResetEncodePendingParams{
		FilePath:   path,
		UpdatedAt:  db.NewUnixTime(now),
		EpisodeID:  episodeID,
		FilePath_2: path,
	})
	return n > 0, err
}

// EnqueueDownloaded queues every downloaded episode that has a file.
func (s *Store) EnqueueDownloaded(ctx context.Context, now time.Time) (int, error) {
	shows, err := s.ListShows(ctx, "all")
	if err != nil {
		return 0, err
	}
	var n int
	for _, sh := range shows {
		eps, err := s.ListEpisodes(ctx, sh.ID)
		if err != nil {
			return n, err
		}
		for _, ep := range eps {
			if ep.State != StateDownloaded || strings.TrimSpace(ep.FilePath) == "" {
				continue
			}
			added, err := s.EnqueueEncode(ctx, ep.ID, ep.FilePath, now)
			if err != nil {
				return n, err
			}
			if added {
				n++
			}
		}
	}
	return n, nil
}

// ClaimEncode marks one pending job running, or reclaims a running job whose
// lease has expired. ok is false when the queue has nothing to claim.
func (s *Store) ClaimEncode(ctx context.Context, now time.Time, lease time.Duration) (EncodeJob, bool, error) {
	if lease <= 0 {
		lease = EncodeLease
	}
	now = now.UTC()
	var last error
	for i := 0; i < 3; i++ {
		job, ok, retry, err := s.claimOnce(ctx, now, lease)
		if err != nil {
			return EncodeJob{}, false, err
		}
		if ok || !retry {
			return job, ok, nil
		}
		last = err
	}
	if last != nil {
		return EncodeJob{}, false, last
	}
	return EncodeJob{}, false, nil
}

func (s *Store) claimOnce(ctx context.Context, now time.Time, lease time.Duration) (EncodeJob, bool, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EncodeJob{}, false, false, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.q.WithTx(tx)
	id, err := q.SelectClaimableEncodeID(ctx, db.NewNullUnixTime(&now))
	if errors.Is(err, sql.ErrNoRows) {
		return EncodeJob{}, false, false, nil
	}
	if err != nil {
		return EncodeJob{}, false, false, err
	}
	until := now.Add(lease)
	n, err := q.MarkEncodeRunning(ctx, db.MarkEncodeRunningParams{
		LeaseUntil:   db.NewNullUnixTime(&until),
		StartedAt:    db.NewNullUnixTime(&now),
		UpdatedAt:    db.NewUnixTime(now),
		ID:           id,
		LeaseUntil_2: db.NewNullUnixTime(&now),
	})
	if err != nil {
		return EncodeJob{}, false, false, err
	}
	if n == 0 {
		return EncodeJob{}, false, true, nil
	}
	row, err := q.GetEncode(ctx, id)
	if err != nil {
		return EncodeJob{}, false, false, err
	}
	if err := tx.Commit(); err != nil {
		return EncodeJob{}, false, false, err
	}
	return fromDBEncode(row), true, false, nil
}

// HeartbeatEncode extends the lease and stores the latest ffmpeg progress.
// ok is false when the job is not running.
func (s *Store) HeartbeatEncode(ctx context.Context, id int64, p EncodeProgress, now time.Time, lease time.Duration) (bool, error) {
	if lease <= 0 {
		lease = EncodeLease
	}
	now = now.UTC()
	until := now.Add(lease)
	n, err := s.q.HeartbeatEncode(ctx, db.HeartbeatEncodeParams{
		LeaseUntil: db.NewNullUnixTime(&until),
		UpdatedAt:  db.NewUnixTime(now),
		Encoder:    p.Encoder,
		Progress:   p.Progress,
		Fps:        p.FPS,
		Speed:      p.Speed,
		ID:         id,
	})
	return n > 0, err
}

// FinishEncode records done, skipped, or failed. A done report with a path
// updates the episode file. The original path is kept for skipped and failed.
func (s *Store) FinishEncode(ctx context.Context, id int64, res EncodeFinish, now time.Time) error {
	switch res.State {
	case EncodeDone, EncodeSkipped, EncodeFailed:
	default:
		return errors.New("encode state must be done, skipped, or failed")
	}
	now = now.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.q.WithTx(tx)
	path := res.FilePath
	if res.State != EncodeDone {
		row, err := q.GetEncode(ctx, id)
		if err != nil {
			return mapErr(err)
		}
		path = row.FilePath
	}
	progress := res.Progress
	if res.State == EncodeDone || res.State == EncodeSkipped {
		progress = 100
	}
	n, err := q.FinishEncode(ctx, db.FinishEncodeParams{
		State:     res.State,
		FilePath:  path,
		Codec:     res.Codec,
		Encoder:   res.Encoder,
		SrcSize:   res.SrcSize,
		DstSize:   res.DstSize,
		Error:     res.Error,
		Progress:  progress,
		Speed:     res.Speed,
		UpdatedAt: db.NewUnixTime(now),
		ID:        id,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if res.State == EncodeDone && path != "" {
		row, err := q.GetEncode(ctx, id)
		if err != nil {
			return err
		}
		if err := q.SetEpisodeFilePath(ctx, db.SetEpisodeFilePathParams{FilePath: path, ID: row.EpisodeID}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReleaseEncode returns a running job to pending so a stopped worker does not hold it.
func (s *Store) ReleaseEncode(ctx context.Context, id int64, now time.Time) (bool, error) {
	n, err := s.q.ReleaseEncode(ctx, db.ReleaseEncodeParams{
		UpdatedAt: db.NewUnixTime(now.UTC()),
		ID:        id,
	})
	return n > 0, err
}

// RequeueFailedEncodes moves failed rows back to pending.
func (s *Store) RequeueFailedEncodes(ctx context.Context, now time.Time) (int, error) {
	n, err := s.q.RequeueFailedEncodes(ctx, db.NewUnixTime(now.UTC()))
	return int(n), err
}

// ListEncodes returns the newest jobs, running first.
func (s *Store) ListEncodes(ctx context.Context, limit int) ([]EncodeJob, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.q.ListEncodes(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	out := make([]EncodeJob, 0, len(rows))
	for _, row := range rows {
		job := fromDBEncode(db.Encode{
			ID: row.ID, EpisodeID: row.EpisodeID, FilePath: row.FilePath, State: row.State,
			Codec: row.Codec, Encoder: row.Encoder, SrcSize: row.SrcSize, DstSize: row.DstSize,
			Error: row.Error, Progress: row.Progress, Fps: row.Fps, Speed: row.Speed,
			LeaseUntil: row.LeaseUntil, StartedAt: row.StartedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
		job.ShowID = row.ShowID
		job.Episode = int(row.EpisodeNumber)
		job.Title = displayTitle(row.TitleEnglish, row.TitleRomaji, row.TitleNative)
		out = append(out, job)
	}
	return out, nil
}

// CountEncodes groups jobs by state.
func (s *Store) CountEncodes(ctx context.Context) (map[string]int, error) {
	rows, err := s.q.CountEncodesByState(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, row := range rows {
		out[row.State] = int(row.N)
	}
	return out, nil
}

// GetEncode loads one job.
func (s *Store) GetEncode(ctx context.Context, id int64) (EncodeJob, error) {
	row, err := s.q.GetEncode(ctx, id)
	if err != nil {
		return EncodeJob{}, mapErr(err)
	}
	return fromDBEncode(row), nil
}

func fromDBEncode(row db.Encode) EncodeJob {
	return EncodeJob{
		ID: row.ID, EpisodeID: row.EpisodeID, FilePath: row.FilePath, State: row.State,
		Codec: row.Codec, Encoder: row.Encoder, SrcSize: row.SrcSize, DstSize: row.DstSize,
		Error: row.Error, Progress: row.Progress, FPS: row.Fps, Speed: row.Speed,
		LeaseUntil: row.LeaseUntil.Ptr(), StartedAt: row.StartedAt.Ptr(), UpdatedAt: row.UpdatedAt.Time(),
	}
}

func displayTitle(english, romaji, native string) string {
	if strings.TrimSpace(english) != "" {
		return english
	}
	if strings.TrimSpace(romaji) != "" {
		return romaji
	}
	if strings.TrimSpace(native) != "" {
		return native
	}
	return "Unknown"
}
