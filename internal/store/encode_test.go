package store

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestEncodeClaimLeaseAndFinish(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	sh := seedShow(t, s)
	ep := seedFile(t, s, sh.ID, 1, "/data/media/anime/Demo/Season 01/Demo - S01E01.mkv")
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

	added, err := s.EnqueueEncode(ctx, ep.ID, ep.FilePath, now)
	if err != nil || !added {
		t.Fatalf("enqueue %v %v", added, err)
	}
	again, err := s.EnqueueEncode(ctx, ep.ID, ep.FilePath, now)
	if err != nil || again {
		t.Fatalf("second enqueue should be a no-op: %v %v", again, err)
	}

	job, ok, err := s.ClaimEncode(ctx, now, time.Minute)
	if err != nil || !ok || job.EpisodeID != ep.ID || job.State != EncodeRunning {
		t.Fatalf("claim %+v ok %v err %v", job, ok, err)
	}
	if job.LeaseUntil == nil || !job.LeaseUntil.Equal(now.Add(time.Minute)) {
		t.Fatalf("lease %+v", job.LeaseUntil)
	}
	_, ok, err = s.ClaimEncode(ctx, now.Add(30*time.Second), time.Minute)
	if err != nil || ok {
		t.Fatalf("second claim while leased: ok %v err %v", ok, err)
	}

	beat, err := s.HeartbeatEncode(ctx, job.ID, EncodeProgress{Encoder: "hevc_videotoolbox", Progress: 40, FPS: 90, Speed: 2.3}, now.Add(20*time.Second), time.Minute)
	if err != nil || !beat {
		t.Fatalf("heartbeat %v %v", beat, err)
	}
	got, err := s.GetEncode(ctx, job.ID)
	if err != nil || got.Progress != 40 || got.FPS != 90 || got.Speed != 2.3 || got.Encoder != "hevc_videotoolbox" {
		t.Fatalf("progress %+v %v", got, err)
	}
	if got.LeaseUntil == nil || !got.LeaseUntil.After(now.Add(time.Minute)) {
		t.Fatalf("lease was not extended: %+v", got.LeaseUntil)
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE encodes SET lease_until = ? WHERE id = ?`, now.Add(-time.Second).Unix(), job.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, ok, err := s.ClaimEncode(ctx, now.Add(2*time.Minute), time.Minute)
	if err != nil || !ok || reclaimed.ID != job.ID {
		t.Fatalf("expired lease reclaim %+v ok %v err %v", reclaimed, ok, err)
	}

	newPath := "/data/media/anime/Demo/Season 01/Demo - S01E01.mkv"
	if err := s.FinishEncode(ctx, job.ID, EncodeFinish{
		State: EncodeDone, FilePath: newPath, Codec: "hevc", Encoder: "hevc_videotoolbox",
		SrcSize: 1000, DstSize: 400, Speed: 2.1,
	}, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	ep, err = s.GetEpisode(ctx, sh.ID, 1)
	if err != nil || ep.FilePath != newPath {
		t.Fatalf("episode path %q %v", ep.FilePath, err)
	}
	done, err := s.GetEncode(ctx, job.ID)
	if err != nil || done.State != EncodeDone || done.DstSize != 400 {
		t.Fatalf("done %+v %v", done, err)
	}
	if _, ok, err = s.ClaimEncode(ctx, now.Add(4*time.Minute), time.Minute); err != nil || ok {
		t.Fatalf("done job was claimed again: ok %v err %v", ok, err)
	}
}

func TestEncodeReleaseAndRequeue(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	sh := seedShow(t, s)
	ep := seedFile(t, s, sh.ID, 3, "/data/media/anime/Demo/Season 01/Demo - S01E03.mp4")
	now := time.Now().UTC()
	if _, err := s.EnqueueEncode(ctx, ep.ID, ep.FilePath, now); err != nil {
		t.Fatal(err)
	}
	job, ok, err := s.ClaimEncode(ctx, now, time.Minute)
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	released, err := s.ReleaseEncode(ctx, job.ID, now)
	if err != nil || !released {
		t.Fatalf("release %v %v", released, err)
	}
	again, ok, err := s.ClaimEncode(ctx, now, time.Minute)
	if err != nil || !ok || again.ID != job.ID || again.State != EncodeRunning {
		t.Fatalf("reclaim after release %+v %v %v", again, ok, err)
	}
	if err := s.FinishEncode(ctx, job.ID, EncodeFinish{State: EncodeFailed, Error: "ffmpeg exited"}, now); err != nil {
		t.Fatal(err)
	}
	n, err := s.RequeueFailedEncodes(ctx, now)
	if err != nil || n != 1 {
		t.Fatalf("requeue %d %v", n, err)
	}
	queued, err := s.GetEncode(ctx, job.ID)
	if err != nil || queued.State != EncodePending || queued.Error != "" {
		t.Fatalf("requeued %+v %v", queued, err)
	}
}

func TestEncodeClaimIsSingleWinner(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	sh := seedShow(t, s)
	ep := seedFile(t, s, sh.ID, 2, "/data/media/anime/Demo/Season 01/Demo - S01E02.mkv")
	now := time.Now().UTC()
	if _, err := s.EnqueueEncode(ctx, ep.ID, ep.FilePath, now); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins int
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, ok, err := s.ClaimEncode(ctx, now, time.Minute)
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if wins != 1 {
		t.Fatalf("wins %d", wins)
	}
}

func TestEnqueueDownloadedSkipsDone(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	sh := seedShow(t, s)
	ep := seedFile(t, s, sh.ID, 4, "/data/media/anime/Demo/Season 01/Demo - S01E04.mkv")
	now := time.Now().UTC()
	n, err := s.EnqueueDownloaded(ctx, now)
	if err != nil || n != 1 {
		t.Fatalf("library enqueue %d %v", n, err)
	}
	job, ok, err := s.ClaimEncode(ctx, now, time.Minute)
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	if err := s.FinishEncode(ctx, job.ID, EncodeFinish{State: EncodeDone, FilePath: ep.FilePath, Codec: "hevc"}, now); err != nil {
		t.Fatal(err)
	}
	n, err = s.EnqueueDownloaded(ctx, now.Add(time.Minute))
	if err != nil || n != 0 {
		t.Fatalf("done file was queued again: %d %v", n, err)
	}
}

func seedShow(t *testing.T, s *Store) Show {
	t.Helper()
	sh, err := s.UpsertShow(context.Background(), Show{
		AniListID: 42, TitleEnglish: "Demo", TitleRomaji: "Demo",
		Format: "TV", Status: "FINISHED", Episodes: 4, Monitored: true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	return sh
}

func seedFile(t *testing.T, s *Store, showID int64, number int, path string) Episode {
	t.Helper()
	ctx := context.Background()
	if err := s.EnsureEpisode(ctx, showID, number, nil, StateDownloaded); err != nil {
		t.Fatal(err)
	}
	ep, err := s.GetEpisode(ctx, showID, number)
	if err != nil {
		t.Fatal(err)
	}
	ep.State = StateDownloaded
	ep.FilePath = path
	if err := s.UpdateEpisode(ctx, ep); err != nil {
		t.Fatal(err)
	}
	return ep
}
