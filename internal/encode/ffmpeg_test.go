package encode

import (
	"strings"
	"testing"
)

func TestEncodeArgs(t *testing.T) {
	vt, err := EncodeArgs("in.mkv", "out.mkv", EncodeOptions{Encoder: "hevc_videotoolbox", BitDepth: 10})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(vt, " ")
	for _, want := range []string{"-map 0", "-map_metadata 0", "-c copy", "-c:v hevc_videotoolbox", "-tag:v hvc1", "-profile:v main10", "-pix_fmt p010le", "-q:v 65", "-progress pipe:1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %s", want, joined)
		}
	}
	if strings.Contains(joined, "x265-params") {
		t.Fatalf("videotoolbox args include threads: %s", joined)
	}
	x, err := EncodeArgs("in.mkv", "out.mkv", EncodeOptions{Encoder: "libx265", Quality: 20, Threads: 4, BitDepth: 8})
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(x, " ")
	if !strings.Contains(joined, "-crf 20") || !strings.Contains(joined, "pools=4") || strings.Contains(joined, "main10") {
		t.Fatalf("%s", joined)
	}
	if _, err := EncodeArgs("a", "b", EncodeOptions{Encoder: "hevc_videotoolbox", Quality: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := EncodeArgs("a", "b", EncodeOptions{Encoder: "nope"}); err == nil {
		t.Fatal("bad encoder")
	}
}
