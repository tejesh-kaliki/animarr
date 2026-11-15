// Package scheduler runs AniList sync, Nyaa polling, and download imports on timers.
package scheduler

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"animarr/internal/config"
	"animarr/internal/pipeline"
)

// ConfigSource is the live settings the scheduler reads each cycle.
type ConfigSource interface {
	Current(context.Context) (config.Config, error)
}

// Scheduler is a single loop. The web UI calls the pipeline directly.
type Scheduler struct {
	Pipe      *pipeline.Service
	Settings  ConfigSource
	Log       *slog.Logger
	SyncEvery time.Duration
	RSSEvery  time.Duration
	PollEvery time.Duration
}

// Run performs one pass immediately, then repeats until ctx is cancelled.
// Interval changes are picked up after the next tick.
func (s *Scheduler) Run(ctx context.Context) {
	s.Once(ctx)
	syncEvery, rssEvery, pollEvery := s.intervals(ctx)
	syncT := time.NewTicker(syncEvery)
	rssT := time.NewTicker(rssEvery)
	pollT := time.NewTicker(pollEvery)
	defer syncT.Stop()
	defer rssT.Stop()
	defer pollT.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-syncT.C:
			s.sync(ctx)
			syncEvery, rssEvery, pollEvery = s.retune(ctx, syncT, rssT, pollT, syncEvery, rssEvery, pollEvery)
		case <-rssT.C:
			s.rss(ctx)
			s.promote(ctx)
			syncEvery, rssEvery, pollEvery = s.retune(ctx, syncT, rssT, pollT, syncEvery, rssEvery, pollEvery)
		case <-pollT.C:
			s.poll(ctx)
			s.promote(ctx)
			syncEvery, rssEvery, pollEvery = s.retune(ctx, syncT, rssT, pollT, syncEvery, rssEvery, pollEvery)
		}
	}
}

func (s *Scheduler) intervals(ctx context.Context) (syncEvery, rssEvery, pollEvery time.Duration) {
	syncEvery, rssEvery, pollEvery = s.SyncEvery, s.RSSEvery, s.PollEvery
	if s.Settings != nil {
		cfg, err := s.Settings.Current(ctx)
		if err != nil {
			s.log().Error("settings", "err", err)
		}
		if cfg.SyncInterval > 0 {
			syncEvery = cfg.SyncInterval
		}
		if cfg.RSSInterval > 0 {
			rssEvery = cfg.RSSInterval
		}
		if cfg.QBPollInterval > 0 {
			pollEvery = cfg.QBPollInterval
		}
	}
	if syncEvery <= 0 {
		syncEvery = 30 * time.Minute
	}
	if rssEvery <= 0 {
		rssEvery = 15 * time.Minute
	}
	if pollEvery <= 0 {
		pollEvery = time.Minute
	}
	return syncEvery, rssEvery, pollEvery
}

func (s *Scheduler) retune(ctx context.Context, syncT, rssT, pollT *time.Ticker, syncEvery, rssEvery, pollEvery time.Duration) (time.Duration, time.Duration, time.Duration) {
	nextSync, nextRSS, nextPoll := s.intervals(ctx)
	if nextSync != syncEvery {
		syncT.Reset(nextSync)
		syncEvery = nextSync
	}
	if nextRSS != rssEvery {
		rssT.Reset(nextRSS)
		rssEvery = nextRSS
	}
	if nextPoll != pollEvery {
		pollT.Reset(nextPoll)
		pollEvery = nextPoll
	}
	return syncEvery, rssEvery, pollEvery
}

// Once runs promote, sync, RSS, and download polling one time.
func (s *Scheduler) Once(ctx context.Context) {
	s.promote(ctx)
	s.sync(ctx)
	s.rss(ctx)
	s.poll(ctx)
}

func (s *Scheduler) sync(ctx context.Context) {
	if s.Pipe == nil {
		return
	}
	user := s.Pipe.Config.AniListUsername
	if s.Settings != nil {
		cfg, err := s.Settings.Current(ctx)
		if err != nil {
			s.log().Error("settings", "err", err)
		}
		user = cfg.AniListUsername
	}
	if strings.TrimSpace(user) == "" {
		return
	}
	if err := s.Pipe.SyncAniList(ctx); err != nil {
		s.log().Error("anilist sync", "err", err)
	}
}

func (s *Scheduler) rss(ctx context.Context) {
	if s.Pipe == nil || !s.automationOn(ctx) {
		return
	}
	if err := s.Pipe.PollRSS(ctx); err != nil {
		s.log().Error("rss", "err", err)
	}
}

func (s *Scheduler) poll(ctx context.Context) {
	if s.Pipe == nil || !s.automationOn(ctx) {
		return
	}
	if err := s.Pipe.PollDownloads(ctx); err != nil {
		s.log().Error("downloads", "err", err)
	}
}

// automationOn reads the live switch. Sync and the UI are not gated by it.
// The pipeline also refuses grabs and imports, so a manual button cannot
// bypass a scheduler that forgot this check.
func (s *Scheduler) automationOn(ctx context.Context) bool {
	if s.Settings != nil {
		cfg, err := s.Settings.Current(ctx)
		if err != nil {
			s.log().Error("settings", "err", err)
		}
		return cfg.AutomationEnabled
	}
	if s.Pipe != nil {
		return s.Pipe.Config.AutomationEnabled
	}
	return false
}

func (s *Scheduler) promote(ctx context.Context) {
	if s.Pipe == nil {
		return
	}
	if err := s.Pipe.PromoteAired(ctx); err != nil {
		s.log().Error("promote", "err", err)
	}
}

func (s *Scheduler) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}
