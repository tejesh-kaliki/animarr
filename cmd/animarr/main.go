// Command animarr tracks an AniList anime collection, grabs episodes from Nyaa,
// and imports them into a Jellyfin library.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"path/filepath"

	"animarr/internal/anilist"
	"animarr/internal/config"
	"animarr/internal/jellyfin"
	"animarr/internal/migrate"
	"animarr/internal/nyaa"
	"animarr/internal/pipeline"
	"animarr/internal/qbit"
	"animarr/internal/scheduler"
	"animarr/internal/secret"
	"animarr/internal/settings"
	"animarr/internal/store"
	"animarr/internal/web"
)

func main() {
	cfg, origin, err := config.LoadBase()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel(cfg.LogLevel)})))

	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
	case "serve":
		err = serve(cfg, origin)
	case "migrate-sonarr":
		err = migrateCmd(cfg, origin, args)
	case "organize":
		err = organizeCmd(cfg, origin, args)
	case "rotate-key":
		err = rotateCmd(cfg, args)
	case "health":
		err = health(cfg)
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		slog.Error(cmd, "err", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `animarr — anime tracker

Usage:
  animarr [serve]
  animarr migrate-sonarr [--dry-run] [--relayout] [--overrides file]
  animarr rotate-key [--old material] [--new material] [--write-file auto|true|false]
  animarr health

Saved settings override the environment, which overrides CONFIG_FILE, then built-in defaults.
LISTEN_ADDR, DATA_PATH, and ANIMARR_SECRET_KEY are read at startup. See README.md.
`)
}

func serve(cfg config.Config, origin map[string]config.Source) error {
	st, prov, err := openSettings(cfg, origin)
	if err != nil {
		return err
	}
	defer st.Close()
	logAutomation(prov)

	pipe := newPipe(cfg, st)
	pipe.Settings = prov
	srv, err := web.New(pipe, st, cfg)
	if err != nil {
		return err
	}
	sched := &scheduler.Scheduler{
		Pipe:      pipe,
		Settings:  prov,
		SyncEvery: cfg.SyncInterval,
		RSSEvery:  cfg.RSSInterval,
		PollEvery: cfg.QBPollInterval,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go sched.Run(ctx)
	slog.Info("listening", "addr", cfg.ListenAddr, "db", cfg.DBPath())
	return srv.Run(ctx, cfg.ListenAddr)
}

func migrateCmd(cfg config.Config, origin map[string]config.Source, args []string) error {
	fs := flag.NewFlagSet("migrate-sonarr", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dry := fs.Bool("dry-run", false, "print the plan without writing the database")
	relayout := fs.Bool("relayout", false, "rename recorded files into franchise season folders")
	overrides := fs.String("overrides", "", "JSON map of Sonarr title to an AniList id or an entries chain")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, prov, err := openSettings(cfg, origin)
	if err != nil {
		return err
	}
	defer st.Close()
	cur, err := prov.Current(context.Background())
	if err != nil {
		slog.Warn("settings", "err", err)
	}
	cur.ListenAddr = cfg.ListenAddr
	cur.DataPath = cfg.DataPath
	cur.LogLevel = cfg.LogLevel
	pipe := newPipe(cur, st)
	pipe.Settings = prov
	_, err = (&migrate.Runner{
		Config:    cur,
		Store:     st,
		AniList:   pipe.AniList,
		Pipe:      pipe,
		Sonarr:    migrate.NewSonarr(cur.SonarrURL, cur.SonarrAPIKey),
		Jellyfin:  pipe.Jellyfin,
		DryRun:    *dry,
		Relayout:  *relayout,
		Overrides: *overrides,
		Out:       os.Stdout,
	}).Run(context.Background())
	return err
}

func rotateCmd(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("rotate-key", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	oldMat := fs.String("old", "", "old key material (base64, hex, or passphrase). Default: ANIMARR_OLD_SECRET_KEY, else the current key")
	newMat := fs.String("new", "", "new key material. Default: ANIMARR_NEW_SECRET_KEY")
	writeFile := fs.String("write-file", "auto", "auto, true, or false. auto updates secret.key when ANIMARR_SECRET_KEY is unset")
	if err := fs.Parse(args); err != nil {
		return err
	}
	oldKey, err := resolveOld(*oldMat, cfg.DataPath)
	if err != nil {
		return err
	}
	newMaterial := strings.TrimSpace(*newMat)
	if newMaterial == "" {
		newMaterial = strings.TrimSpace(os.Getenv("ANIMARR_NEW_SECRET_KEY"))
	}
	if newMaterial == "" {
		return fmt.Errorf("rotate-key needs --new or ANIMARR_NEW_SECRET_KEY")
	}
	newKey, err := secret.ParseKey(newMaterial)
	if err != nil {
		return fmt.Errorf("new key: %w", err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	n, err := settings.Rotate(context.Background(), st, secret.NewBox(oldKey), secret.NewBox(newKey))
	if err != nil {
		return err
	}
	path := filepath.Join(cfg.DataPath, secret.FileName)
	if wantWrite(*writeFile) {
		if err := secret.WriteKeyFile(path, newKey); err != nil {
			return fmt.Errorf("re-encrypted %d secret(s), but secret.key was not updated: %w", n, err)
		}
		fmt.Printf("re-encrypted %d secret(s) and updated %s\n", n, path)
		return nil
	}
	fmt.Printf("re-encrypted %d secret(s). Set ANIMARR_SECRET_KEY to the new key before the next start, or re-run with --write-file=true.\n", n)
	return nil
}

func resolveOld(material, dataPath string) ([32]byte, error) {
	material = strings.TrimSpace(material)
	if material == "" {
		material = strings.TrimSpace(os.Getenv("ANIMARR_OLD_SECRET_KEY"))
	}
	if material != "" {
		key, err := secret.ParseKey(material)
		if err != nil {
			return [32]byte{}, fmt.Errorf("old key: %w", err)
		}
		return key, nil
	}
	loaded, err := secret.Load(dataPath)
	if err != nil {
		return [32]byte{}, err
	}
	if loaded.Warning != "" {
		slog.Warn("secret key", "warning", loaded.Warning)
	}
	return loaded.Box.Key(), nil
}

func wantWrite(flag string) bool {
	switch strings.ToLower(strings.TrimSpace(flag)) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		v := strings.TrimSpace(os.Getenv(secret.EnvKey))
		return v == ""
	}
}

func logAutomation(prov *settings.Provider) {
	if prov == nil {
		return
	}
	cur, err := prov.Current(context.Background())
	if err != nil {
		slog.Warn("settings", "err", err)
	}
	if cur.AutomationEnabled {
		return
	}
	slog.Warn("automation is disabled: AniList sync and the UI still run, but searches, grabs, imports, NFO writes, and Jellyfin refresh do not. Enable AUTOMATION_ENABLED in Settings after existing files are recorded")
}

func openSettings(cfg config.Config, origin map[string]config.Source) (*store.Store, *settings.Provider, error) {
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return nil, nil, err
	}
	loaded, err := secret.Load(cfg.DataPath)
	if err != nil {
		_ = st.Close()
		return nil, nil, err
	}
	if loaded.Warning != "" {
		slog.Warn("secret key", "warning", loaded.Warning)
	} else {
		slog.Info("secret key", "source", loaded.Source)
	}
	return st, settings.New(cfg, origin, st, loaded), nil
}

func health(cfg config.Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL(cfg.ListenAddr), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health %s", resp.Status)
	}
	return nil
}

func healthURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + strings.TrimPrefix(addr, "http://") + "/healthz"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz"
}

func newPipe(cfg config.Config, st *store.Store) *pipeline.Service {
	ix := nyaa.New(cfg.NyaaURLs, cfg.NyaaCategory, cfg.UserAgent, cfg.RequestInterval)
	ix.Log = slog.Default()
	qb, err := qbit.New(cfg.QBURL, cfg.QBUsername, cfg.QBPassword)
	if err != nil {
		slog.Error("qbit", "err", err)
	}
	return &pipeline.Service{
		Config:   cfg,
		Store:    st,
		AniList:  anilist.New(cfg.UserAgent),
		Indexer:  ix,
		Qbit:     qb,
		Jellyfin: jellyfin.New(cfg.JellyfinURL, cfg.JellyfinAPIKey),
		Log:      slog.Default(),
	}
}

func logLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
