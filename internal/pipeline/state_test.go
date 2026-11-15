package pipeline

import (
	"testing"
	"time"
)

func TestDesiredState(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	tests := []struct {
		name string
		n    int
		air  *time.Time
		next int
		want string
	}{
		{"aired", 1, &past, 3, "wanted"},
		{"future air", 4, &future, 3, "upcoming"},
		{"no date before next", 2, nil, 3, "wanted"},
		{"no date at next", 3, nil, 3, "upcoming"},
		{"finished unknown", 1, nil, 0, "wanted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DesiredState(tt.n, tt.air, tt.next, now); got != tt.want {
				t.Fatalf("%s", got)
			}
		})
	}
}
