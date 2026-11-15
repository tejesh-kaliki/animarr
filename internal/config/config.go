// Package config loads animarr settings from the environment and an optional JSON file.
// Environment variables override the file. Secrets are never given default values.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"animarr/internal/tuning"
)

// Config is the process configuration.
type Config struct {
	AniListUsername   string
	AniListToken      string
	ListenAddr        string
	DataPath          string
	LibraryDir        string
	DownloadsDir      string
	NyaaURLs          []string
	NyaaCategory      string
	UserAgent         string
	QBURL             string
	QBUsername        string
	QBPassword        string
	QBCategory        string
	JellyfinURL       string
	JellyfinAPIKey    string
	SyncInterval      time.Duration
	RSSInterval       time.Duration
	QBPollInterval    time.Duration
	RSSWindow         time.Duration
	SearchBackoff     time.Duration
	RequestInterval   time.Duration
	BacklogPerPoll    int
	Score             tuning.Score
	Upgrade           tuning.Upgrade
	MatchThreshold    float64
	NFOEnabled        bool
	AutomationEnabled bool
	EncodeEnabled     bool
	LogLevel          string
	ConfigFile        string
}

// Public is the settings page view. Secrets are reduced to a set/unset flag.
type Public struct {
	AniListUsername   string
	AniListTokenSet   bool
	ListenAddr        string
	DataPath          string
	LibraryDir        string
	DownloadsDir      string
	NyaaURLs          string
	NyaaCategory      string
	UserAgent         string
	QBURL             string
	QBUsername        string
	QBPasswordSet     bool
	QBCategory        string
	JellyfinURL       string
	JellyfinKeySet    bool
	SyncInterval      string
	RSSInterval       string
	QBPollInterval    string
	RSSWindow         string
	SearchBackoff     string
	RequestInterval   string
	BacklogPerPoll    int
	PreferredRes      string
	HEVCBonus         int
	DubPenalty        int
	RepackBonus       int
	MinSeeders        int
	GroupOrder        string
	MatchThreshold    float64
	UpgradeEnabled    bool
	UpgradeRepack     bool
	UpgradeWindow     string
	UpgradeMinDelta   int
	NFOEnabled        bool
	AutomationEnabled bool
	EncodeEnabled     bool
	LogLevel          string
}

// Clone copies slice fields so a caller can overlay settings without mutating the base.
func (c Config) Clone() Config {
	out := c
	if c.NyaaURLs != nil {
		out.NyaaURLs = append([]string(nil), c.NyaaURLs...)
	}
	if c.Score.GroupOrder != nil {
		out.Score.GroupOrder = append([]string(nil), c.Score.GroupOrder...)
	}
	return out
}

// DBPath is the SQLite file under DataPath.
func (c Config) DBPath() string {
	return strings.TrimRight(c.DataPath, "/") + "/animarr.db"
}

// SaveDir is the qBittorrent save path for animarr grabs.
func (c Config) SaveDir() string {
	return strings.TrimRight(c.DownloadsDir, "/") + "/animarr"
}

// Public redacts secrets for display.
func (c Config) Public() Public {
	return Public{
		AniListUsername:   c.AniListUsername,
		AniListTokenSet:   c.AniListToken != "",
		ListenAddr:        c.ListenAddr,
		DataPath:          c.DataPath,
		LibraryDir:        c.LibraryDir,
		DownloadsDir:      c.DownloadsDir,
		NyaaURLs:          strings.Join(c.NyaaURLs, ", "),
		NyaaCategory:      c.NyaaCategory,
		UserAgent:         c.UserAgent,
		QBURL:             c.QBURL,
		QBUsername:        c.QBUsername,
		QBPasswordSet:     c.QBPassword != "",
		QBCategory:        c.QBCategory,
		JellyfinURL:       c.JellyfinURL,
		JellyfinKeySet:    c.JellyfinAPIKey != "",
		SyncInterval:      c.SyncInterval.String(),
		RSSInterval:       c.RSSInterval.String(),
		QBPollInterval:    c.QBPollInterval.String(),
		RSSWindow:         c.RSSWindow.String(),
		SearchBackoff:     c.SearchBackoff.String(),
		RequestInterval:   c.RequestInterval.String(),
		BacklogPerPoll:    c.BacklogPerPoll,
		PreferredRes:      c.Score.PreferredResolution,
		HEVCBonus:         c.Score.HEVCBonus,
		DubPenalty:        c.Score.DubPenalty,
		RepackBonus:       c.Score.RepackBonus,
		MinSeeders:        c.Score.MinSeeders,
		GroupOrder:        strings.Join(c.Score.GroupOrder, ", "),
		MatchThreshold:    c.MatchThreshold,
		UpgradeEnabled:    c.Upgrade.Enabled,
		UpgradeRepack:     c.Upgrade.Repack,
		UpgradeWindow:     c.Upgrade.Window.String(),
		UpgradeMinDelta:   c.Upgrade.MinDelta,
		NFOEnabled:        c.NFOEnabled,
		AutomationEnabled: c.AutomationEnabled,
		EncodeEnabled:     c.EncodeEnabled,
		LogLevel:          c.LogLevel,
	}
}

type fileConfig struct {
	PreferredResolution *string  `json:"preferredResolution"`
	HEVCBonus           *int     `json:"hevcBonus"`
	DubPenalty          *int     `json:"dubPenalty"`
	RepackBonus         *int     `json:"repackBonus"`
	MinSeeders          *int     `json:"minSeeders"`
	GroupOrder          []string `json:"groupOrder"`
	MatchThreshold      *float64 `json:"matchThreshold"`
	UpgradeEnabled      *bool    `json:"upgradeEnabled"`
	UpgradeRepack       *bool    `json:"upgradeRepack"`
	UpgradeWindow       *string  `json:"upgradeWindow"`
	UpgradeMinDelta     *int     `json:"upgradeMinDelta"`
	NyaaURLs            []string `json:"nyaaUrls"`
	NyaaCategory        *string  `json:"nyaaCategory"`
	NFOEnabled          *bool    `json:"nfoEnabled"`
	AutomationEnabled   *bool    `json:"automationEnabled"`
	EncodeEnabled       *bool    `json:"encodeEnabled"`
}

// Load reads defaults, then CONFIG_FILE, then the environment.
func Load() (Config, error) {
	cfg, _, err := LoadBase()
	return cfg, err
}

// LoadBase is Load plus the source of each value before any UI override.
// A missing key in origin means SourceDefault.
func LoadBase() (Config, map[string]Source, error) {
	cfg := defaults()
	origin := map[string]Source{}
	if path, ok := os.LookupEnv("CONFIG_FILE"); ok && strings.TrimSpace(path) != "" {
		cfg.ConfigFile = path
		keys, err := applyFile(&cfg, path)
		if err != nil {
			return Config{}, nil, err
		}
		for _, k := range keys {
			origin[k] = SourceFile
		}
	}
	for _, k := range applyEnv(&cfg) {
		origin[k] = SourceEnv
	}
	if err := cfg.validate(); err != nil {
		return Config{}, nil, err
	}
	return cfg, origin, nil
}

func defaults() Config {
	return Config{
		ListenAddr:        ":8990",
		DataPath:          "/config",
		LibraryDir:        "/data/media/anime",
		DownloadsDir:      "/data/downloads",
		NyaaURLs:          []string{"https://nyaa.si"},
		NyaaCategory:      "1_2",
		UserAgent:         "animarr/0.1 (+homelab; anime tracker)",
		QBURL:             "http://127.0.0.1:8080",
		QBCategory:        "animarr",
		JellyfinURL:       "http://jellyfin:8096",
		SyncInterval:      30 * time.Minute,
		RSSInterval:       15 * time.Minute,
		QBPollInterval:    time.Minute,
		RSSWindow:         6 * time.Hour,
		SearchBackoff:     6 * time.Hour,
		RequestInterval:   2 * time.Second,
		BacklogPerPoll:    3,
		Score:             tuning.DefaultScore(),
		Upgrade:           tuning.DefaultUpgrade(),
		MatchThreshold:    0.82,
		NFOEnabled:        true,
		AutomationEnabled: false,
		LogLevel:          "info",
	}
}

func applyFile(cfg *Config, path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config file: %w", err)
	}
	var f fileConfig
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("config file: %w", err)
	}
	var keys []string
	mark := func(key string) { keys = append(keys, key) }
	if f.PreferredResolution != nil {
		cfg.Score.PreferredResolution = *f.PreferredResolution
		mark(KeyPreferredResolution)
	}
	if f.HEVCBonus != nil {
		cfg.Score.HEVCBonus = *f.HEVCBonus
		mark(KeyHEVCBonus)
	}
	if f.DubPenalty != nil {
		cfg.Score.DubPenalty = *f.DubPenalty
		mark(KeyDubPenalty)
	}
	if f.RepackBonus != nil {
		cfg.Score.RepackBonus = *f.RepackBonus
		mark(KeyRepackBonus)
	}
	if f.MinSeeders != nil {
		cfg.Score.MinSeeders = *f.MinSeeders
		mark(KeyMinSeeders)
	}
	if len(f.GroupOrder) > 0 {
		cfg.Score.GroupOrder = f.GroupOrder
		mark(KeyGroupOrder)
	}
	if f.MatchThreshold != nil {
		cfg.MatchThreshold = *f.MatchThreshold
		mark(KeyMatchThreshold)
	}
	if f.UpgradeEnabled != nil {
		cfg.Upgrade.Enabled = *f.UpgradeEnabled
		mark(KeyUpgradeEnabled)
	}
	if f.UpgradeRepack != nil {
		cfg.Upgrade.Repack = *f.UpgradeRepack
		mark(KeyUpgradeRepack)
	}
	if f.UpgradeWindow != nil {
		d, err := time.ParseDuration(*f.UpgradeWindow)
		if err != nil {
			return nil, fmt.Errorf("upgradeWindow: %w", err)
		}
		cfg.Upgrade.Window = d
		mark(KeyUpgradeWindow)
	}
	if f.UpgradeMinDelta != nil {
		cfg.Upgrade.MinDelta = *f.UpgradeMinDelta
		mark(KeyUpgradeMinDelta)
	}
	if len(f.NyaaURLs) > 0 {
		cfg.NyaaURLs = cleanURLs(f.NyaaURLs)
		mark(KeyNyaaURLs)
	}
	if f.NyaaCategory != nil && *f.NyaaCategory != "" {
		cfg.NyaaCategory = *f.NyaaCategory
		mark(KeyNyaaCategory)
	}
	if f.NFOEnabled != nil {
		cfg.NFOEnabled = *f.NFOEnabled
		mark(KeyNFOEnabled)
	}
	if f.AutomationEnabled != nil {
		cfg.AutomationEnabled = *f.AutomationEnabled
		mark(KeyAutomationEnabled)
	}
	if f.EncodeEnabled != nil {
		cfg.EncodeEnabled = *f.EncodeEnabled
		mark(KeyEncodeEnabled)
	}
	return keys, nil
}

func applyEnv(cfg *Config) []string {
	var keys []string
	mark := func(key string) { keys = append(keys, key) }
	if v, ok := os.LookupEnv("ANILIST_USERNAME"); ok {
		cfg.AniListUsername = strings.TrimSpace(v)
		mark(KeyAniListUsername)
	}
	if v, ok := os.LookupEnv("ANILIST_TOKEN"); ok {
		cfg.AniListToken = v
		mark(KeyAniListToken)
	}
	if v, ok := lookup("LISTEN_ADDR"); ok {
		cfg.ListenAddr = v
	}
	if v, ok := lookup("DATA_PATH"); ok {
		cfg.DataPath = v
	}
	if v, ok := lookup("LIBRARY_DIR"); ok {
		cfg.LibraryDir = v
		mark(KeyLibraryDir)
	}
	if v, ok := lookup("DOWNLOADS_DIR"); ok {
		cfg.DownloadsDir = v
		mark(KeyDownloadsDir)
	}
	if v, ok := lookup("NYAA_URLS"); ok {
		cfg.NyaaURLs = cleanURLs(strings.Split(v, ","))
		mark(KeyNyaaURLs)
	}
	if v, ok := lookup("NYAA_CATEGORY"); ok {
		cfg.NyaaCategory = v
		mark(KeyNyaaCategory)
	}
	if v, ok := lookup("NYAA_USER_AGENT"); ok {
		cfg.UserAgent = v
		mark(KeyUserAgent)
	}
	if v, ok := lookup("QB_URL"); ok {
		cfg.QBURL = strings.TrimRight(v, "/")
		mark(KeyQBURL)
	}
	if v, ok := os.LookupEnv("QB_USERNAME"); ok {
		cfg.QBUsername = v
		mark(KeyQBUsername)
	}
	if v, ok := os.LookupEnv("QB_PASSWORD"); ok {
		cfg.QBPassword = v
		mark(KeyQBPassword)
	}
	if v, ok := lookup("QB_CATEGORY"); ok {
		cfg.QBCategory = v
		mark(KeyQBCategory)
	}
	if v, ok := lookup("JELLYFIN_URL"); ok {
		cfg.JellyfinURL = strings.TrimRight(v, "/")
		mark(KeyJellyfinURL)
	}
	if v, ok := os.LookupEnv("JELLYFIN_API_KEY"); ok {
		cfg.JellyfinAPIKey = v
		mark(KeyJellyfinAPIKey)
	}
	if setDur(&cfg.SyncInterval, "SYNC_INTERVAL") {
		mark(KeySyncInterval)
	}
	if setDur(&cfg.RSSInterval, "RSS_INTERVAL") {
		mark(KeyRSSInterval)
	}
	if setDur(&cfg.QBPollInterval, "QB_POLL_INTERVAL") {
		mark(KeyQBPollInterval)
	}
	if setDur(&cfg.RSSWindow, "RSS_WINDOW") {
		mark(KeyRSSWindow)
	}
	if setDur(&cfg.SearchBackoff, "SEARCH_BACKOFF") {
		mark(KeySearchBackoff)
	}
	if setDur(&cfg.RequestInterval, "REQUEST_INTERVAL") {
		mark(KeyRequestInterval)
	}
	if setInt(&cfg.BacklogPerPoll, "BACKLOG_PER_POLL") {
		mark(KeyBacklogPerPoll)
	}
	if v, ok := lookup("SCORE_PREFERRED_RESOLUTION"); ok {
		cfg.Score.PreferredResolution = v
		mark(KeyPreferredResolution)
	}
	if setInt(&cfg.Score.HEVCBonus, "SCORE_HEVC_BONUS") {
		mark(KeyHEVCBonus)
	}
	if setInt(&cfg.Score.DubPenalty, "SCORE_DUB_PENALTY") {
		mark(KeyDubPenalty)
	}
	if setInt(&cfg.Score.RepackBonus, "SCORE_REPACK_BONUS") {
		mark(KeyRepackBonus)
	}
	if setInt(&cfg.Score.MinSeeders, "SCORE_MIN_SEEDERS") {
		mark(KeyMinSeeders)
	}
	if v, ok := lookup("SCORE_GROUP_ORDER"); ok {
		cfg.Score.GroupOrder = splitComma(v)
		mark(KeyGroupOrder)
	}
	if v, ok := lookup("SCORE_MATCH_THRESHOLD"); ok {
		f, err := strconv.ParseFloat(v, 64)
		if err == nil {
			cfg.MatchThreshold = f
			mark(KeyMatchThreshold)
		}
	}
	if setBool(&cfg.Upgrade.Enabled, "UPGRADE_ENABLED") {
		mark(KeyUpgradeEnabled)
	}
	if setBool(&cfg.Upgrade.Repack, "UPGRADE_REPACK") {
		mark(KeyUpgradeRepack)
	}
	if setDur(&cfg.Upgrade.Window, "UPGRADE_WINDOW") {
		mark(KeyUpgradeWindow)
	}
	if setInt(&cfg.Upgrade.MinDelta, "UPGRADE_MIN_DELTA") {
		mark(KeyUpgradeMinDelta)
	}
	if v, ok := lookup("LOG_LEVEL"); ok {
		cfg.LogLevel = strings.ToLower(v)
	}
	if setBool(&cfg.NFOEnabled, "NFO_ENABLED") {
		mark(KeyNFOEnabled)
	}
	if setBool(&cfg.AutomationEnabled, "AUTOMATION_ENABLED") {
		mark(KeyAutomationEnabled)
	}
	if setBool(&cfg.EncodeEnabled, "ENCODE_ENABLED") {
		mark(KeyEncodeEnabled)
	}
	return keys
}

func (c Config) validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("LISTEN_ADDR is empty")
	}
	if c.DataPath == "" {
		return fmt.Errorf("DATA_PATH is empty")
	}
	if len(c.NyaaURLs) == 0 {
		return fmt.Errorf("NYAA_URLS is empty")
	}
	if c.MatchThreshold <= 0 || c.MatchThreshold > 1 {
		return fmt.Errorf("SCORE_MATCH_THRESHOLD must be in (0, 1]")
	}
	for _, d := range []struct {
		name string
		v    time.Duration
	}{
		{"SYNC_INTERVAL", c.SyncInterval},
		{"RSS_INTERVAL", c.RSSInterval},
		{"QB_POLL_INTERVAL", c.QBPollInterval},
	} {
		if d.v <= 0 {
			return fmt.Errorf("%s must be > 0", d.name)
		}
	}
	return nil
}

func lookup(key string) (string, bool) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return "", false
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return "", false
	}
	return v, true
}

func setDur(dst *time.Duration, key string) bool {
	v, ok := lookup(key)
	if !ok {
		return false
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return false
	}
	*dst = d
	return true
}

func setInt(dst *int, key string) bool {
	v, ok := lookup(key)
	if !ok {
		return false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return false
	}
	*dst = n
	return true
}

func setBool(dst *bool, key string) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "t", "true", "yes", "y", "on":
		*dst = true
		return true
	case "0", "f", "false", "no", "n", "off":
		*dst = false
		return true
	default:
		return false
	}
}

func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cleanURLs(in []string) []string {
	var out []string
	for _, p := range in {
		p = strings.TrimSpace(p)
		p = strings.TrimRight(p, "/")
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
