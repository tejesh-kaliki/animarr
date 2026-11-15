package encode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseProbeFixtures(t *testing.T) {
	hevc := readProbe(t, "hevc10.json")
	if !hevc.HasVideo || hevc.VideoCodec != "hevc" || hevc.BitDepth != 10 || !AlreadyEfficient(hevc) {
		t.Fatalf("%+v", hevc)
	}
	if hevc.Duration < 1440*time.Second || hevc.Size != 800000000 {
		t.Fatalf("duration %s size %d", hevc.Duration, hevc.Size)
	}
	h264 := readProbe(t, "h264.json")
	if h264.VideoCodec != "h264" || h264.BitDepth != 8 || AlreadyEfficient(h264) {
		t.Fatalf("%+v", h264)
	}
	av1 := readProbe(t, "av1.json")
	if !AlreadyEfficient(av1) || av1.BitDepth != 8 {
		t.Fatalf("%+v", av1)
	}
}

func TestDecide(t *testing.T) {
	src := Probe{Duration: 100 * time.Second, Size: 50 << 20, HasVideo: true, VideoCodec: "h264"}
	ok := Probe{Duration: 100*time.Second + 400*time.Millisecond, Size: 20 << 20, HasVideo: true, VideoCodec: "hevc"}
	if state, reason := Decide(src, ok); state != "done" || reason != "" {
		t.Fatalf("%s %s", state, reason)
	}
	big := ok
	big.Size = src.Size
	if state, _ := Decide(src, big); state != "skipped" {
		t.Fatalf("not smaller: %s", state)
	}
	short := ok
	short.Duration = 90 * time.Second
	if state, _ := Decide(src, short); state != "failed" {
		t.Fatalf("duration: %s", state)
	}
	empty := ok
	empty.HasVideo = false
	if state, _ := Decide(src, empty); state != "failed" {
		t.Fatalf("no video: %s", state)
	}
	tiny := ok
	tiny.Size = 1000
	if state, _ := Decide(src, tiny); state != "failed" {
		t.Fatalf("tiny: %s", state)
	}
}

func readProbe(t *testing.T, name string) Probe {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParseProbeJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
