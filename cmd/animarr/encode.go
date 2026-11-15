package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"animarr/internal/config"
	"animarr/internal/encode"
)

func encodeWorker(args []string) error {
	fs := flag.NewFlagSet("encode-worker", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	server := fs.String("server", "http://127.0.0.1:8990", "animarr base URL")
	pathMap := fs.String("path-map", "", "container=host library prefix (required)")
	ffmpeg := fs.String("ffmpeg", "ffmpeg", "ffmpeg binary")
	ffprobe := fs.String("ffprobe", "ffprobe", "ffprobe binary")
	encoder := fs.String("encoder", encode.DefaultEncoder, "hevc_videotoolbox or libx265")
	quality := fs.Int("quality", 0, "VideoToolbox -q:v 1-100 (higher is better; 0 means 65) or libx265 CRF 0-51 (lower is better; 0 means 22)")
	threads := fs.Int("threads", 0, "libx265 thread pools. 0 uses the encoder default. Ignored by VideoToolbox")
	once := fs.Bool("once", false, "take one job and exit")
	dry := fs.Bool("dry-run", false, "probe the queue and release it without encoding")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*pathMap) == "" {
		return fmt.Errorf("encode-worker needs --path-map container=host")
	}
	pm, err := encode.ParsePathMap(*pathMap)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	w := &encode.Worker{
		Server:  *server,
		Map:     pm,
		FFmpeg:  *ffmpeg,
		FFprobe: *ffprobe,
		Encoder: *encoder,
		Quality: *quality,
		Threads: *threads,
		Once:    *once,
		DryRun:  *dry,
		Log:     slog.Default(),
	}
	return w.Run(ctx)
}

func encodeEnqueue(cfg config.Config, origin map[string]config.Source) error {
	st, prov, err := openSettings(cfg, origin)
	if err != nil {
		return err
	}
	defer st.Close()
	cur, err := prov.Current(context.Background())
	if err != nil {
		return err
	}
	if !cur.EncodeEnabled {
		return fmt.Errorf("ENCODE_ENABLED is off. Turn it on in Settings, then run this again")
	}
	n, err := st.EnqueueDownloaded(context.Background(), time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("queued %d file(s)\n", n)
	return nil
}
