package encode

import (
	"strconv"
	"strings"
	"time"
)

// Progress is one ffmpeg -progress block.
type Progress struct {
	Percent float64
	FPS     float64
	Speed   float64
	Out     time.Duration
}

// ParseProgress reads key=value lines from ffmpeg -progress.
// total is the source duration, used to turn out_time into a percent.
func ParseProgress(text string, total time.Duration) Progress {
	var p Progress
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "fps":
			p.FPS, _ = strconv.ParseFloat(v, 64)
		case "speed":
			p.Speed, _ = strconv.ParseFloat(strings.TrimSuffix(v, "x"), 64)
		case "out_time":
			p.Out = parseOutTime(v)
		}
	}
	if total > 0 && p.Out > 0 {
		p.Percent = float64(p.Out) / float64(total) * 100
		if p.Percent > 100 {
			p.Percent = 100
		}
	}
	return p
}

func parseOutTime(s string) time.Duration {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0
	}
	h, errH := strconv.Atoi(parts[0])
	m, errM := strconv.Atoi(parts[1])
	sec, errS := strconv.ParseFloat(parts[2], 64)
	if errH != nil || errM != nil || errS != nil {
		return 0
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec*float64(time.Second))
}
