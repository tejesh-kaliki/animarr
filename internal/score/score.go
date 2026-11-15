// Package score ranks a parsed release. Higher is better.
// A release below the seeder floor or a batch is rejected.
package score

import (
	"strings"
	"time"

	"animarr/internal/parser"
)

// Config holds tunable weights. Zero values are not replaced; use DefaultConfig.
type Config struct {
	PreferredResolution string
	HEVCBonus           int
	DubPenalty          int
	RepackBonus         int
	MinSeeders          int
	GroupOrder          []string
	GroupStep           int
}

// Upgrade decides when a grabbed file may be replaced.
type Upgrade struct {
	Enabled  bool
	Repack   bool
	Window   time.Duration
	MinDelta int
}

// DefaultConfig matches the homelab defaults: 1080p, HEVC +10, dub penalty,
// and the group order ToonsHub, Erai-raws, ASW, SubsPlease, Judas, DKB, EMBER.
func DefaultConfig() Config {
	return Config{
		PreferredResolution: "1080p",
		HEVCBonus:           10,
		DubPenalty:          40,
		RepackBonus:         15,
		MinSeeders:          1,
		GroupOrder:          []string{"ToonsHub", "Erai-raws", "ASW", "SubsPlease", "Judas", "DKB", "EMBER"},
		GroupStep:           10,
	}
}

// Score returns a comparable total. ok is false when the release must be skipped.
func Score(p parser.Parsed, seeders int, preferred string, cfg Config) (int, bool) {
	if p.Batch {
		return 0, false
	}
	if seeders < cfg.MinSeeders {
		return 0, false
	}
	if preferred == "" {
		preferred = cfg.PreferredResolution
	}
	if preferred == "" {
		preferred = "1080p"
	}
	total := resolutionScore(p.Resolution, preferred)
	if p.Codec == "hevc" {
		total += cfg.HEVCBonus
	}
	total += groupScore(p.Group, cfg)
	if p.Dub && !p.DualAudio {
		total -= cfg.DubPenalty
	}
	if p.Repack || p.Version >= 2 {
		total += cfg.RepackBonus
	}
	capN := seeders
	if capN > 50 {
		capN = 50
	}
	if capN > 0 {
		total += capN / 5
	}
	return total, true
}

// ShouldUpgrade reports whether next should replace a file grabbed at downloadedAt.
// Repacks are allowed while Upgrade.Repack is on. Other upgrades require Enabled.
// Both must fall inside Window and must not lower the score.
func ShouldUpgrade(currentScore, currentVersion int, downloadedAt time.Time, nextScore int, next parser.Parsed, now time.Time, cfg Upgrade) bool {
	if downloadedAt.IsZero() || cfg.Window <= 0 || now.Sub(downloadedAt) > cfg.Window {
		return false
	}
	repack := next.Repack || next.Version >= 2
	if cfg.Repack && repack && next.Version > currentVersion && nextScore >= currentScore {
		return true
	}
	if cfg.Enabled && nextScore >= currentScore+cfg.MinDelta {
		return true
	}
	return false
}

func groupScore(group string, cfg Config) int {
	step := cfg.GroupStep
	if step == 0 {
		step = 10
	}
	n := len(cfg.GroupOrder)
	for i, g := range cfg.GroupOrder {
		if strings.EqualFold(strings.TrimSpace(g), strings.TrimSpace(group)) {
			return (n - i) * step
		}
	}
	return 0
}

func resolutionScore(got, preferred string) int {
	got = strings.ToLower(got)
	preferred = strings.ToLower(preferred)
	table, ok := resTables[preferred]
	if !ok {
		table = resTables["1080p"]
	}
	if s, ok := table[got]; ok {
		return s
	}
	if s, ok := table[""]; ok {
		return s
	}
	return 20
}

// resTables ranks resolutions for a preferred target.
// 2160p is allowed when 1080p is preferred, but it does not win.
var resTables = map[string]map[string]int{
	"1080p": {"1080p": 100, "2160p": 50, "720p": 40, "480p": 15, "360p": 10, "": 20},
	"720p":  {"720p": 100, "1080p": 60, "2160p": 40, "480p": 25, "360p": 15, "": 20},
	"2160p": {"2160p": 100, "1080p": 70, "720p": 30, "480p": 10, "360p": 5, "": 20},
	"480p":  {"480p": 100, "720p": 40, "1080p": 30, "2160p": 20, "360p": 15, "": 15},
}
