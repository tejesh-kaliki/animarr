package encode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Probe is the part of an ffprobe document the worker needs.
type Probe struct {
	Duration   time.Duration
	Size       int64
	VideoCodec string
	BitDepth   int
	HasVideo   bool
}

// AlreadyEfficient reports a video that does not need another HEVC encode.
func AlreadyEfficient(p Probe) bool {
	switch strings.ToLower(strings.TrimSpace(p.VideoCodec)) {
	case "hevc", "h265", "av1":
		return true
	default:
		return false
	}
}

// MinOutputBytes is the smallest encode we will keep. Anything smaller is treated as a failed encode.
const MinOutputBytes int64 = 1 << 20

// Decide says whether a verified output should replace the source.
// "done" replaces it. "skipped" keeps the original because the encode did not shrink it.
// "failed" keeps the original because the output does not look like the source.
func Decide(src, dst Probe) (state, reason string) {
	if !dst.HasVideo {
		return "failed", "output has no video stream"
	}
	if src.Duration <= 0 || dst.Duration <= 0 {
		return "failed", "duration is missing"
	}
	delta := src.Duration - dst.Duration
	if delta < 0 {
		delta = -delta
	}
	if delta > time.Second {
		return "failed", "output duration differs from the source by more than 1s"
	}
	if dst.Size < MinOutputBytes {
		return "failed", "output is smaller than 1 MiB"
	}
	if src.Size <= 0 {
		return "failed", "source size is missing"
	}
	if dst.Size >= src.Size {
		return "skipped", "output is not smaller than the original"
	}
	return "done", ""
}

// ParseProbeJSON reads ffprobe -print_format json.
func ParseProbeJSON(b []byte) (Probe, error) {
	var doc struct {
		Streams []struct {
			CodecType string  `json:"codec_type"`
			CodecName string  `json:"codec_name"`
			PixFmt    string  `json:"pix_fmt"`
			Bits      flexInt `json:"bits_per_raw_sample"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
			Size     string `json:"size"`
		} `json:"format"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return Probe{}, fmt.Errorf("ffprobe json: %w", err)
	}
	var p Probe
	for _, st := range doc.Streams {
		if st.CodecType != "video" || p.HasVideo {
			continue
		}
		p.HasVideo = true
		p.VideoCodec = st.CodecName
		p.BitDepth = st.Bits.N
		if p.BitDepth < 10 {
			p.BitDepth = depthFromPixFmt(st.PixFmt, p.BitDepth)
		}
	}
	if doc.Format.Duration != "" && doc.Format.Duration != "N/A" {
		sec, err := strconv.ParseFloat(doc.Format.Duration, 64)
		if err != nil {
			return Probe{}, fmt.Errorf("ffprobe duration: %w", err)
		}
		p.Duration = time.Duration(sec * float64(time.Second))
	}
	if doc.Format.Size != "" && doc.Format.Size != "N/A" {
		n, err := strconv.ParseInt(doc.Format.Size, 10, 64)
		if err != nil {
			return Probe{}, fmt.Errorf("ffprobe size: %w", err)
		}
		p.Size = n
	}
	return p, nil
}

func depthFromPixFmt(pix string, fallback int) int {
	pix = strings.ToLower(pix)
	switch {
	case strings.Contains(pix, "12"):
		return 12
	case strings.Contains(pix, "10"), strings.Contains(pix, "p010"):
		return 10
	default:
		if fallback == 0 {
			return 8
		}
		return fallback
	}
}

type flexInt struct{ N int }

func (f *flexInt) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" || s == "N/A" {
			return nil
		}
		n, err := strconv.Atoi(s)
		f.N = n
		return err
	}
	n, err := strconv.Atoi(string(b))
	f.N = n
	return err
}

// ProbeFile runs ffprobe on path.
func ProbeFile(ctx context.Context, bin, path string) (Probe, error) {
	if bin == "" {
		bin = "ffprobe"
	}
	cmd := exec.CommandContext(ctx, bin, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return Probe{}, fmt.Errorf("ffprobe: %s", trimMsg(ee.Stderr))
		}
		return Probe{}, fmt.Errorf("ffprobe: %w", err)
	}
	return ParseProbeJSON(out)
}

func trimMsg(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 500 {
		s = s[len(s)-500:]
	}
	return s
}
