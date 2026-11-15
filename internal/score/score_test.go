package score

import (
	"strings"
	"testing"
	"time"

	"animarr/internal/parser"
)

func TestScoreOrdering(t *testing.T) {
	cfg := DefaultConfig()
	p := func(title string) parser.Parsed { return parser.Parse(title) }

	toons := p("[ToonsHub] Title - 07 [1080p HEVC]")
	subs := p("[SubsPlease] Title - 07 [1080p HEVC]")
	unknown := p("[Nobody] Title - 07 [1080p HEVC]")
	avc := p("[ToonsHub] Title - 07 [1080p]")
	dub := p("[ToonsHub] Title - 07 [1080p][English Dub][HEVC]")
	dual := p("[ToonsHub] Title - 07 [1080p][Dual Audio][HEVC]")
	p720 := p("[ToonsHub] Title - 07 [720p HEVC]")
	p2160 := p("[ToonsHub] Title - 07 [2160p HEVC]")
	repack := p("[ToonsHub] Title - 07v2 [1080p HEVC]")
	batch := p("[ToonsHub] Title - 01-12 [1080p HEVC]")

	s := func(parsed parser.Parsed, seeders int, preferred string) (int, bool) {
		t.Helper()
		return Score(parsed, seeders, preferred, cfg)
	}

	toonsS, ok := s(toons, 20, "")
	if !ok {
		t.Fatal("toons rejected")
	}
	subsS, _ := s(subs, 20, "")
	unkS, _ := s(unknown, 20, "")
	if !(toonsS > subsS && subsS > unkS) {
		t.Fatalf("group order toons %d subs %d unknown %d", toonsS, subsS, unkS)
	}
	avcS, _ := s(avc, 20, "")
	if toonsS-avcS != cfg.HEVCBonus {
		t.Fatalf("hevc bonus got %d want %d", toonsS-avcS, cfg.HEVCBonus)
	}
	dubS, _ := s(dub, 20, "")
	if toonsS-dubS != cfg.DubPenalty {
		t.Fatalf("dub penalty got %d", toonsS-dubS)
	}
	dualS, _ := s(dual, 20, "")
	if dualS != toonsS {
		t.Fatalf("dual audio changed score %d vs %d", dualS, toonsS)
	}
	s720, _ := s(p720, 20, "")
	s2160, _ := s(p2160, 20, "")
	if !(toonsS > s2160 && s2160 > s720) {
		t.Fatalf("resolution order 1080 %d 2160 %d 720 %d", toonsS, s2160, s720)
	}
	pref2160, _ := s(p2160, 20, "2160p")
	pref1080as2160, _ := s(toons, 20, "2160p")
	if pref2160 <= pref1080as2160 {
		t.Fatalf("per-show 2160 preference failed %d vs %d", pref2160, pref1080as2160)
	}
	repS, _ := s(repack, 20, "")
	if repS-toonsS != cfg.RepackBonus {
		t.Fatalf("repack bonus %d", repS-toonsS)
	}
	if _, ok := s(batch, 100, ""); ok {
		t.Fatal("batch should be rejected")
	}
	explained := Explain(batch, 100, "", cfg)
	if explained.OK || explained.Total <= 0 || !strings.Contains(explained.Reject, "batch") {
		t.Fatalf("explain batch %+v", explained)
	}
	if len(explained.Lines) == 0 {
		t.Fatal("explain lines empty")
	}
	if _, ok := s(toons, 0, ""); ok {
		t.Fatal("zero seeders should be rejected at the default floor")
	}
}

func TestShouldUpgrade(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-2 * time.Hour)
	old := now.Add(-10 * 24 * time.Hour)
	up := Upgrade{Enabled: false, Repack: true, Window: 72 * time.Hour, MinDelta: 15}
	v2 := parser.Parse("[Erai-raws] Title - 07v2 [1080p HEVC]")
	plain := parser.Parse("[ToonsHub] Title - 07 [1080p HEVC]")

	if !ShouldUpgrade(100, 1, recent, 115, v2, now, up) {
		t.Fatal("expected repack inside the window to upgrade while general upgrades are off")
	}
	if ShouldUpgrade(100, 1, old, 115, v2, now, up) {
		t.Fatal("repack outside the window should stay")
	}
	if ShouldUpgrade(100, 1, recent, 130, plain, now, up) {
		t.Fatal("non-repack upgrade should stay off")
	}
	up.Enabled = true
	if !ShouldUpgrade(100, 1, recent, 130, plain, now, up) {
		t.Fatal("enabled upgrade with a clear gain should replace")
	}
	if ShouldUpgrade(100, 1, recent, 110, plain, now, up) {
		t.Fatal("gain below min delta should stay")
	}
	if ShouldUpgrade(200, 1, recent, 150, v2, now, up) {
		t.Fatal("worse repack should stay")
	}
}
