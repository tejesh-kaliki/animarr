package scheduler

import (
	"context"
	"testing"
	"time"

	"animarr/internal/config"
	"animarr/internal/pipeline"
)

type stubSettings struct {
	cfg config.Config
	err error
}

func (s stubSettings) Current(context.Context) (config.Config, error) {
	return s.cfg, s.err
}

func TestIntervalsFollowProvider(t *testing.T) {
	s := &Scheduler{
		Settings: stubSettings{cfg: config.Config{
			SyncInterval:   5 * time.Minute,
			RSSInterval:    time.Minute,
			QBPollInterval: 30 * time.Second,
		}},
		SyncEvery: time.Hour,
		RSSEvery:  time.Hour,
		PollEvery: time.Hour,
	}
	syncEvery, rssEvery, pollEvery := s.intervals(context.Background())
	if syncEvery != 5*time.Minute || rssEvery != time.Minute || pollEvery != 30*time.Second {
		t.Fatalf("%s %s %s", syncEvery, rssEvery, pollEvery)
	}
}

func TestAutomationFollowsSettings(t *testing.T) {
	ctx := context.Background()
	off := &Scheduler{Settings: stubSettings{cfg: config.Config{}}}
	if off.automationOn(ctx) {
		t.Fatal("automation defaults off")
	}
	on := &Scheduler{Settings: stubSettings{cfg: config.Config{AutomationEnabled: true}}}
	if !on.automationOn(ctx) {
		t.Fatal("saved switch should enable the next cycle")
	}
	settingsWin := &Scheduler{
		Settings: stubSettings{cfg: config.Config{AutomationEnabled: false}},
		Pipe:     &pipeline.Service{Config: config.Config{AutomationEnabled: true}},
	}
	if settingsWin.automationOn(ctx) {
		t.Fatal("live settings should win over the startup config")
	}
	pipeOnly := &Scheduler{Pipe: &pipeline.Service{Config: config.Config{AutomationEnabled: true}}}
	if !pipeOnly.automationOn(ctx) {
		t.Fatal("without a provider, the pipe config is the switch")
	}
}
