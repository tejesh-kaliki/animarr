// Package score ranks a parsed release. Higher is better.
// A release below the seeder floor or a batch is rejected.
package score

import (
	"fmt"
	"strings"
	"time"

	"animarr/internal/parser"
	"animarr/internal/tuning"
)

// Config is the weight set. It lives in tuning so config does not import score.
type Config = tuning.Score

// Upgrade is the replacement policy. It lives in tuning so config does not import score.
type Upgrade = tuning.Upgrade

// DefaultConfig matches the homelab defaults: 1080p, HEVC +10, dub penalty,
// and the group order ToonsHub, Erai-raws, ASW, SubsPlease, Judas, DKB, EMBER.
func DefaultConfig() Config {
	return tuning.DefaultScore()
}

// Report is the score and the short reasons behind it.
// Total is the quality total even when OK is false, so a rejected release can still be sorted.
type Report struct {
	Total  int
	OK     bool
	Reject string
	Lines  []string
}

// Explain scores a release and lists why. A batch or a seeder-floor miss is not OK.
// The automatic picker uses Score, which drops those. Interactive search shows them.
func Explain(p parser.Parsed, seeders int, preferred string, cfg Config) Report {
	if preferred == "" {
		preferred = cfg.PreferredResolution
	}
	if preferred == "" {
		preferred = "1080p"
	}
	res := resolutionScore(p.Resolution, preferred)
	var lines []string
	switch {
	case p.Resolution == "":
		lines = append(lines, fmt.Sprintf("no resolution (+%d)", res))
	case strings.EqualFold(p.Resolution, preferred):
		lines = append(lines, fmt.Sprintf("%s matches preferred (+%d)", p.Resolution, res))
	default:
		lines = append(lines, fmt.Sprintf("%s instead of %s (+%d)", p.Resolution, preferred, res))
	}
	total := res
	if p.Codec == "hevc" {
		total += cfg.HEVCBonus
		lines = append(lines, fmt.Sprintf("HEVC (+%d)", cfg.HEVCBonus))
	} else if p.Codec != "" {
		lines = append(lines, strings.ToUpper(p.Codec)+" (+0)")
	}
	gs := groupScore(p.Group, cfg)
	total += gs
	switch {
	case gs > 0:
		lines = append(lines, fmt.Sprintf("%s (+%d)", p.Group, gs))
	case strings.TrimSpace(p.Group) != "":
		lines = append(lines, fmt.Sprintf("%s is not in the group list (+0)", p.Group))
	}
	if p.Dub && !p.DualAudio {
		total -= cfg.DubPenalty
		lines = append(lines, fmt.Sprintf("English dub (−%d)", cfg.DubPenalty))
	} else if p.DualAudio {
		lines = append(lines, "dual audio (+0)")
	}
	if p.Repack || p.Version >= 2 {
		total += cfg.RepackBonus
		lines = append(lines, fmt.Sprintf("repack (+%d)", cfg.RepackBonus))
	}
	capN := seeders
	if capN > 50 {
		capN = 50
	}
	seederPts := 0
	if capN > 0 {
		seederPts = capN / 5
	}
	total += seederPts
	lines = append(lines, fmt.Sprintf("%d seeders (+%d)", seeders, seederPts))

	rep := Report{Total: total, OK: true, Lines: lines}
	if p.Batch {
		rep.OK = false
		rep.Reject = "batch; automatic search skips packs"
	}
	if seeders < cfg.MinSeeders {
		rep.OK = false
		floor := fmt.Sprintf("%d seeders is below the floor of %d", seeders, cfg.MinSeeders)
		if rep.Reject != "" {
			rep.Reject += "; " + floor
		} else {
			rep.Reject = floor
		}
	}
	return rep
}

// Score returns a comparable total. ok is false when the release must be skipped.
func Score(p parser.Parsed, seeders int, preferred string, cfg Config) (int, bool) {
	rep := Explain(p, seeders, preferred, cfg)
	if !rep.OK {
		return 0, false
	}
	return rep.Total, true
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
