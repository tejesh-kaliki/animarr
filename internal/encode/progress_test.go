package encode

import (
	"testing"
	"time"
)

func TestParseProgress(t *testing.T) {
	block := "frame=120\nfps=80.5\nout_time=00:01:00.000000\nspeed=2.30x\nprogress=continue\n"
	p := ParseProgress(block, 2*time.Minute)
	if p.FPS != 80.5 || p.Speed != 2.3 || p.Out != time.Minute {
		t.Fatalf("%+v", p)
	}
	if p.Percent < 49.9 || p.Percent > 50.1 {
		t.Fatalf("percent %v", p.Percent)
	}
}
