package encode

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// DefaultVideoToolboxQuality is ffmpeg -q:v for hevc_videotoolbox. Higher is better, from 1 to 100.
const DefaultVideoToolboxQuality = 65

// DefaultCRF is the libx265 CRF. Lower is better, from 0 to 51.
const DefaultCRF = 22

// EncodeOptions selects the encoder. Quality 0 uses the default for that encoder.
// Threads applies to libx265 only.
type EncodeOptions struct {
	Encoder  string
	Quality  int
	Threads  int
	BitDepth int
}

// EncodeArgs builds an ffmpeg argv that copies every non-video stream and encodes video to HEVC.
func EncodeArgs(src, dst string, opt EncodeOptions) ([]string, error) {
	switch opt.Encoder {
	case "hevc_videotoolbox", "libx265":
	default:
		return nil, fmt.Errorf("encoder must be hevc_videotoolbox or libx265")
	}
	q := opt.Quality
	if q == 0 {
		if opt.Encoder == "libx265" {
			q = DefaultCRF
		} else {
			q = DefaultVideoToolboxQuality
		}
	}
	args := []string{
		"-y", "-i", src,
		"-map", "0",
		"-map_metadata", "0",
		"-c", "copy",
		"-c:v", opt.Encoder,
		"-tag:v", "hvc1",
	}
	if opt.BitDepth >= 10 {
		args = append(args, "-profile:v", "main10")
		if opt.Encoder == "hevc_videotoolbox" {
			args = append(args, "-pix_fmt", "p010le")
		} else {
			args = append(args, "-pix_fmt", "yuv420p10le")
		}
	}
	if opt.Encoder == "hevc_videotoolbox" {
		if q < 1 || q > 100 {
			return nil, fmt.Errorf("VideoToolbox quality must be from 1 to 100")
		}
		args = append(args, "-q:v", strconv.Itoa(q))
	} else {
		if q < 0 || q > 51 {
			return nil, fmt.Errorf("libx265 CRF must be from 0 to 51")
		}
		args = append(args, "-crf", strconv.Itoa(q))
		if opt.Threads > 0 {
			args = append(args, "-x265-params", fmt.Sprintf("pools=%d", opt.Threads))
		}
	}
	args = append(args, "-progress", "pipe:1", "-nostats", dst)
	return args, nil
}

// RunFFmpeg runs ffmpeg and calls on for each progress block. ctx cancels the process.
func RunFFmpeg(ctx context.Context, bin string, args []string, total time.Duration, on func(Progress)) error {
	if bin == "" {
		bin = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg: %w", err)
	}
	readProgress(stdout, total, on)
	err = cmd.Wait()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := trimMsg(stderr.Bytes())
		if msg == "" {
			return fmt.Errorf("ffmpeg: %w", err)
		}
		return fmt.Errorf("ffmpeg: %s", msg)
	}
	return nil
}

func readProgress(r io.Reader, total time.Duration, on func(Progress)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var block strings.Builder
	for sc.Scan() {
		line := sc.Text()
		block.WriteString(line)
		block.WriteByte('\n')
		if strings.HasPrefix(line, "progress=") && on != nil {
			on(ParseProgress(block.String(), total))
			block.Reset()
		}
	}
}

// EncoderAvailable reports whether ffmpeg lists the named encoder.
func EncoderAvailable(ctx context.Context, bin, name string) error {
	if bin == "" {
		bin = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, bin, "-hide_banner", "-encoders")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("ffmpeg: %w", err)
	}
	if !bytes.Contains(out, []byte(" "+name+" ")) && !bytes.Contains(out, []byte(" "+name+"\n")) {
		return fmt.Errorf("ffmpeg has no %s encoder (ffmpeg -encoders | grep %s)", name, name)
	}
	return nil
}
