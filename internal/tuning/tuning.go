// Package tuning holds the score and upgrade weights.
// config and score both use these types. config does not import score.
package tuning

import "time"

// Score holds tunable weights. Zero values are not replaced; use DefaultScore.
type Score struct {
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

// DefaultScore matches the homelab defaults: 1080p, HEVC +10, dub penalty,
// and the group order ToonsHub, Erai-raws, ASW, SubsPlease, Judas, DKB, EMBER.
func DefaultScore() Score {
	return Score{
		PreferredResolution: "1080p",
		HEVCBonus:           10,
		DubPenalty:          40,
		RepackBonus:         15,
		MinSeeders:          1,
		GroupOrder:          []string{"ToonsHub", "Erai-raws", "ASW", "SubsPlease", "Judas", "DKB", "EMBER"},
		GroupStep:           10,
	}
}

// DefaultUpgrade is repacks inside 72 hours, with other upgrades off.
func DefaultUpgrade() Upgrade {
	return Upgrade{
		Enabled:  false,
		Repack:   true,
		Window:   72 * time.Hour,
		MinDelta: 15,
	}
}
