package pipeline

import "time"

// DesiredState is the state a new or non-terminal episode should have.
// A known future air date, or an episode at or after the next airing slot, is upcoming.
func DesiredState(number int, air *time.Time, nextEp int, now time.Time) string {
	if air != nil && air.After(now) {
		return "upcoming"
	}
	if nextEp > 0 && number >= nextEp && (air == nil || air.After(now)) {
		return "upcoming"
	}
	return "wanted"
}
