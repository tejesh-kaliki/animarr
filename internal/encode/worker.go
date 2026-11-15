package encode

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultEncoder is the macOS hardware encoder.
	DefaultEncoder = "hevc_videotoolbox"
	defaultIdle    = 15 * time.Second
	progressLogGap = 15 * time.Second
	heartbeatGap   = 2 * time.Second
)

// Worker claims one job at a time and encodes it on this machine.
// Tests may set probe and encode. Otherwise the worker shells out to ffprobe and ffmpeg.
type Worker struct {
	Server  string
	Map     PathMap
	FFmpeg  string
	FFprobe string
	Encoder string
	Quality int
	Threads int
	Once    bool
	DryRun  bool
	Idle    time.Duration
	Log     *slog.Logger

	client *Client
	probe  func(context.Context, string) (Probe, error)
	encode func(context.Context, string, string, EncodeOptions, time.Duration, func(Progress)) error
}

func (w *Worker) encoder() string {
	if w.Encoder == "" {
		return DefaultEncoder
	}
	return w.Encoder
}

func (w *Worker) idle() time.Duration {
	if w.Idle <= 0 {
		return defaultIdle
	}
	return w.Idle
}

func (w *Worker) log() *slog.Logger {
	if w != nil && w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

// Run claims jobs until the context is cancelled.
// Once exits after one job. DryRun probes and releases every claim without encoding.
// Cancel removes a temp file and returns the current job to pending.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.checkTools(ctx); err != nil {
		return err
	}
	c := w.api()
	w.log().Info("encode worker",
		"server", w.Server,
		"encoder", w.encoder(),
		"quality", w.Quality,
		"threads", w.Threads,
		"dry_run", w.DryRun,
		"once", w.Once,
		"from", w.Map.From,
		"to", w.Map.To,
	)
	held := map[int64]struct{}{}
	defer w.releaseHeld(held, c)
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		job, ok, err := c.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.log().Warn("encode next", "err", err)
			if w.Once || w.DryRun {
				return err
			}
			if err := w.wait(ctx); err != nil {
				return nil
			}
			continue
		}
		if !ok {
			if w.DryRun || w.Once {
				return nil
			}
			if err := w.wait(ctx); err != nil {
				return nil
			}
			continue
		}
		if _, seen := held[job.ID]; seen {
			w.log().Warn("encode dry-run stopped", "id", job.ID, "reason", "the same job was claimed again")
			return nil
		}
		stop, err := w.handle(ctx, c, job, held)
		if err != nil {
			w.log().Error("encode job", "id", job.ID, "err", err)
			if w.Once || w.DryRun {
				return err
			}
		}
		if stop || w.Once {
			return nil
		}
	}
}

func (w *Worker) api() *Client {
	if w.client != nil {
		return w.client
	}
	c := &Client{Base: w.Server}
	w.client = c
	return c
}

func (w *Worker) checkTools(ctx context.Context) error {
	enc := w.encoder()
	if _, err := EncodeArgs("in.mkv", "out.mkv", EncodeOptions{Encoder: enc, Quality: w.Quality, Threads: w.Threads, BitDepth: 10}); err != nil {
		return err
	}
	if w.Map.From == "" || w.Map.To == "" {
		return fmt.Errorf("--path-map is required")
	}
	if w.probe == nil {
		bin := w.FFprobe
		if bin == "" {
			bin = "ffprobe"
		}
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("ffprobe not found: %w", err)
		}
	}
	if w.encode == nil {
		bin := w.FFmpeg
		if bin == "" {
			bin = "ffmpeg"
		}
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("ffmpeg not found: %w", err)
		}
		if err := EncoderAvailable(ctx, bin, enc); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) wait(ctx context.Context) error {
	t := time.NewTimer(w.idle())
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (w *Worker) handle(ctx context.Context, c *Client, job Job, held map[int64]struct{}) (bool, error) {
	started := time.Now()
	host, err := w.Map.ToHost(job.FilePath)
	if err != nil {
		if w.DryRun {
			return w.hold(ctx, c, job, held, "outside path map", err.Error())
		}
		return false, w.fail(c, job.ID, err.Error())
	}
	if _, err := os.Stat(host); err != nil {
		msg := err.Error()
		if os.IsNotExist(err) {
			msg = "file not found (is the drive mounted?)"
		}
		if w.DryRun {
			return w.hold(ctx, c, job, held, "missing", msg)
		}
		return false, w.fail(c, job.ID, msg)
	}
	src, err := w.probeFn(ctx, host)
	if err != nil {
		if w.DryRun {
			return w.hold(ctx, c, job, held, "probe failed", err.Error())
		}
		return false, w.fail(c, job.ID, err.Error())
	}
	if AlreadyEfficient(src) {
		if w.DryRun {
			return w.hold(ctx, c, job, held, "skip", "already "+src.VideoCodec)
		}
		w.logResult(job.ID, w.encoder(), time.Since(started), 0, src.Size, 0, "skipped", "already "+src.VideoCodec)
		return false, w.done(c, job.ID, Report{
			FilePath: job.FilePath,
			Codec:    src.VideoCodec,
			Encoder:  w.encoder(),
			SrcSize:  src.Size,
			Skipped:  true,
			Error:    "already " + src.VideoCodec,
		})
	}
	if w.DryRun {
		return w.hold(ctx, c, job, held, "encode", src.VideoCodec)
	}
	return w.encodeOne(ctx, c, job, host, src, started)
}

func (w *Worker) encodeOne(ctx context.Context, c *Client, job Job, host string, src Probe, started time.Time) (bool, error) {
	tmp := TempPath(host)
	defer func() { _ = os.Remove(tmp) }()

	var progress atomic.Value
	progress.Store(Progress{})
	stopBeat := make(chan struct{})
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { close(stopBeat) }) }
	defer stop()
	go func() {
		t := time.NewTicker(progressLogGap)
		defer t.Stop()
		for {
			select {
			case <-stopBeat:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				p, _ := progress.Load().(Progress)
				w.beat(c, job.ID, p)
			}
		}
	}()

	var lastBeat, lastLog time.Time
	on := func(p Progress) {
		progress.Store(p)
		now := time.Now()
		if now.Sub(lastBeat) >= heartbeatGap {
			lastBeat = now
			w.beat(c, job.ID, p)
		}
		if now.Sub(lastLog) >= progressLogGap {
			lastLog = now
			w.log().Info("encode progress",
				"id", job.ID,
				"percent", fmt.Sprintf("%.1f%%", p.Percent),
				"fps", fmt.Sprintf("%.1f", p.FPS),
				"speed", FormatSpeed(p.Speed),
			)
		}
	}
	opt := EncodeOptions{Encoder: w.encoder(), Quality: w.Quality, Threads: w.Threads, BitDepth: src.BitDepth}
	if err := w.encodeFn(ctx, host, tmp, opt, src.Duration, on); err != nil {
		_ = os.Remove(tmp)
		stop()
		if ctx.Err() != nil {
			w.releaseOne(job.ID, c)
			return true, nil
		}
		w.logResult(job.ID, w.encoder(), time.Since(started), 0, src.Size, 0, "failed", err.Error())
		return false, w.fail(c, job.ID, err.Error())
	}
	stop()
	last, _ := progress.Load().(Progress)
	dst, err := w.probeFn(ctx, tmp)
	if err != nil {
		_ = os.Remove(tmp)
		w.logResult(job.ID, w.encoder(), time.Since(started), last.Speed, src.Size, 0, "failed", err.Error())
		return false, w.fail(c, job.ID, "verification failed: "+err.Error())
	}
	if dst.Size == 0 {
		if info, statErr := os.Stat(tmp); statErr == nil {
			dst.Size = info.Size()
		}
	}
	state, reason := Decide(src, dst)
	if state != "done" {
		_ = os.Remove(tmp)
		w.logResult(job.ID, w.encoder(), time.Since(started), last.Speed, src.Size, dst.Size, state, reason)
		if state == "skipped" {
			return false, w.done(c, job.ID, Report{
				FilePath: job.FilePath,
				Codec:    dst.VideoCodec,
				Encoder:  w.encoder(),
				SrcSize:  src.Size,
				DstSize:  dst.Size,
				Skipped:  true,
				Speed:    last.Speed,
				Error:    reason,
			})
		}
		return false, w.fail(c, job.ID, "verification failed: "+reason)
	}
	final, err := ReplaceVideo(host, tmp)
	if err != nil && final == "" {
		_ = os.Remove(tmp)
		w.logResult(job.ID, w.encoder(), time.Since(started), last.Speed, src.Size, dst.Size, "failed", err.Error())
		return false, w.fail(c, job.ID, err.Error())
	}
	note := ""
	if err != nil {
		note = err.Error()
		w.log().Warn("encode replace", "id", job.ID, "err", err, "file", final)
	}
	container, mapErr := w.Map.ToContainer(final)
	if mapErr != nil {
		container = containerFallback(job.FilePath, final)
		if note == "" {
			note = mapErr.Error()
		}
	}
	w.logResult(job.ID, w.encoder(), time.Since(started), last.Speed, src.Size, dst.Size, "done", note)
	return false, w.done(c, job.ID, Report{
		FilePath: container,
		Codec:    orCodec(dst.VideoCodec),
		Encoder:  w.encoder(),
		SrcSize:  src.Size,
		DstSize:  dst.Size,
		Speed:    last.Speed,
		Error:    note,
	})
}

func orCodec(codec string) string {
	if codec == "" {
		return "hevc"
	}
	return codec
}

func containerFallback(containerFile, hostFinal string) string {
	dir := path.Dir(filepath.ToSlash(containerFile))
	if dir == "." || dir == "/" {
		return filepath.Base(hostFinal)
	}
	return dir + "/" + filepath.Base(hostFinal)
}

func (w *Worker) hold(ctx context.Context, c *Client, job Job, held map[int64]struct{}, action, detail string) (bool, error) {
	w.log().Info("encode dry-run", "id", job.ID, "action", action, "file", job.FilePath, "detail", detail)
	held[job.ID] = struct{}{}
	beatCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.Heartbeat(beatCtx, job.ID, Heartbeat{Encoder: w.encoder()}); err != nil && ctx.Err() == nil {
		w.log().Warn("encode heartbeat", "id", job.ID, "err", err)
	}
	return false, nil
}

func (w *Worker) beat(c *Client, id int64, p Progress) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Heartbeat(ctx, id, Heartbeat{Encoder: w.encoder(), Percent: p.Percent, FPS: p.FPS, Speed: p.Speed}); err != nil {
		w.log().Warn("encode heartbeat", "id", id, "err", err)
	}
}

// done reports success. A report that does not land is returned so the caller can retry later via the lease.
func (w *Worker) done(c *Client, id int64, rep Report) error {
	if err := w.report(func(ctx context.Context) error { return c.Done(ctx, id, rep) }); err != nil {
		return fmt.Errorf("report done: %w", err)
	}
	return nil
}

// fail tells the server and returns nil when the report lands.
// The returned error is only the failure to report, so a bad file is not a worker crash.
func (w *Worker) fail(c *Client, id int64, reason string) error {
	w.log().Warn("encode failed", "id", id, "err", reason)
	if err := w.report(func(ctx context.Context) error { return c.Fail(ctx, id, reason) }); err != nil {
		return fmt.Errorf("%s (report: %v)", reason, err)
	}
	return nil
}

func (w *Worker) report(fn func(context.Context) error) error {
	var err error
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = fn(ctx)
		cancel()
		if err == nil {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 200 * time.Millisecond)
	}
	return err
}

func (w *Worker) releaseOne(id int64, c *Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Release(ctx, id); err != nil {
		w.log().Warn("encode release", "id", id, "err", err)
	}
}

func (w *Worker) releaseHeld(held map[int64]struct{}, c *Client) {
	if len(held) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for id := range held {
		if err := c.Release(ctx, id); err != nil {
			w.log().Warn("encode release", "id", id, "err", err)
		}
	}
}

func (w *Worker) probeFn(ctx context.Context, file string) (Probe, error) {
	if w.probe != nil {
		return w.probe(ctx, file)
	}
	return ProbeFile(ctx, w.FFprobe, file)
}

func (w *Worker) encodeFn(ctx context.Context, src, dst string, opt EncodeOptions, total time.Duration, on func(Progress)) error {
	if w.encode != nil {
		return w.encode(ctx, src, dst, opt, total, on)
	}
	args, err := EncodeArgs(src, dst, opt)
	if err != nil {
		return err
	}
	return RunFFmpeg(ctx, w.FFmpeg, args, total, on)
}

func (w *Worker) logResult(id int64, encoder string, elapsed time.Duration, speed float64, src, dst int64, state, detail string) {
	args := []any{
		"id", id,
		"state", state,
		"encoder", encoder,
		"elapsed", elapsed.Round(time.Millisecond).String(),
		"speed", FormatSpeed(speed),
	}
	if src > 0 {
		args = append(args, "src", src)
	}
	if dst > 0 {
		args = append(args, "dst", dst)
	}
	if src > 0 && dst > 0 {
		args = append(args, "saved", fmt.Sprintf("%.1f%%", float64(src-dst)/float64(src)*100))
	}
	if detail != "" {
		args = append(args, "detail", detail)
	}
	w.log().Info("encode job", args...)
}

// FormatSpeed renders an ffmpeg speed multiplier, for example 2.3x.
func FormatSpeed(speed float64) string {
	return strconv.FormatFloat(speed, 'f', 1, 64) + "x"
}
