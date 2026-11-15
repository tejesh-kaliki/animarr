package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Source is the layer that produced a setting value.
type Source string

const (
	SourceSaved   Source = "saved"
	SourceEnv     Source = "env"
	SourceFile    Source = "file"
	SourceDefault Source = "default"
)

// Kind is how a setting is parsed and shown.
type Kind string

const (
	KindText       Kind = "text"
	KindSecret     Kind = "secret"
	KindURL        Kind = "url"
	KindURLs       Kind = "urls"
	KindDuration   Kind = "duration"
	KindInt        Kind = "int"
	KindFloat      Kind = "float"
	KindBool       Kind = "bool"
	KindPath       Kind = "path"
	KindResolution Kind = "resolution"
)

const (
	KeyAniListUsername     = "ANILIST_USERNAME"
	KeyAniListToken        = "ANILIST_TOKEN"
	KeyNyaaURLs            = "NYAA_URLS"
	KeyNyaaCategory        = "NYAA_CATEGORY"
	KeyUserAgent           = "NYAA_USER_AGENT"
	KeyQBURL               = "QB_URL"
	KeyQBUsername          = "QB_USERNAME"
	KeyQBPassword          = "QB_PASSWORD"
	KeyQBCategory          = "QB_CATEGORY"
	KeyJellyfinURL         = "JELLYFIN_URL"
	KeyJellyfinAPIKey      = "JELLYFIN_API_KEY"
	KeyLibraryDir          = "LIBRARY_DIR"
	KeyDownloadsDir        = "DOWNLOADS_DIR"
	KeyPreferredResolution = "SCORE_PREFERRED_RESOLUTION"
	KeyGroupOrder          = "SCORE_GROUP_ORDER"
	KeyMinSeeders          = "SCORE_MIN_SEEDERS"
	KeyHEVCBonus           = "SCORE_HEVC_BONUS"
	KeyDubPenalty          = "SCORE_DUB_PENALTY"
	KeyRepackBonus         = "SCORE_REPACK_BONUS"
	KeyMatchThreshold      = "SCORE_MATCH_THRESHOLD"
	KeyUpgradeEnabled      = "UPGRADE_ENABLED"
	KeyUpgradeRepack       = "UPGRADE_REPACK"
	KeyUpgradeWindow       = "UPGRADE_WINDOW"
	KeyUpgradeMinDelta     = "UPGRADE_MIN_DELTA"
	KeySyncInterval        = "SYNC_INTERVAL"
	KeyRSSInterval         = "RSS_INTERVAL"
	KeyQBPollInterval      = "QB_POLL_INTERVAL"
	KeyRSSWindow           = "RSS_WINDOW"
	KeySearchBackoff       = "SEARCH_BACKOFF"
	KeyRequestInterval     = "REQUEST_INTERVAL"
	KeyBacklogPerPoll      = "BACKLOG_PER_POLL"
	KeyNFOEnabled          = "NFO_ENABLED"
	KeyAutomationEnabled   = "AUTOMATION_ENABLED"
	KeySonarrURL           = "SONARR_URL"
	KeySonarrAPIKey        = "SONARR_API_KEY"
)

// Spec describes one UI-editable setting.
type Spec struct {
	Key        string
	Label      string
	Help       string
	Group      string
	Kind       Kind
	AllowEmpty bool
	Positive   bool
	Min        int
	MinSet     bool
	Pattern    string
	Options    []string
}

// Specs is every setting the UI can edit, in page order.
// LISTEN_ADDR, DATA_PATH, LOG_LEVEL, and the secret key are not included.
func Specs() []Spec {
	return specs
}

// Lookup returns the spec for an environment-style key.
func Lookup(key string) (Spec, bool) {
	for _, s := range specs {
		if s.Key == key {
			return s, true
		}
	}
	return Spec{}, false
}

// Editable reports whether the UI may store this key.
func Editable(key string) bool {
	_, ok := Lookup(key)
	return ok
}

// Canonical validates value and returns the form stored in the database.
// Secrets are returned trimmed of a trailing newline only, not rewritten.
func Canonical(key, value string) (string, error) {
	spec, ok := Lookup(key)
	if !ok {
		return "", fmt.Errorf("unknown setting %s", key)
	}
	if spec.Kind == KindSecret {
		if err := validate(spec, value); err != nil {
			return "", err
		}
		return strings.TrimRight(value, "\r\n"), nil
	}
	value = strings.TrimSpace(value)
	if err := validate(spec, value); err != nil {
		return "", err
	}
	var tmp Config
	if err := applyValue(&tmp, spec, value); err != nil {
		return "", err
	}
	return Format(tmp, key), nil
}

// Apply validates value and writes it onto cfg.
func Apply(cfg *Config, key, value string) error {
	spec, ok := Lookup(key)
	if !ok {
		return fmt.Errorf("unknown setting %s", key)
	}
	if spec.Kind != KindSecret {
		value = strings.TrimSpace(value)
	} else {
		value = strings.TrimRight(value, "\r\n")
	}
	if err := validate(spec, value); err != nil {
		return err
	}
	return applyValue(cfg, spec, value)
}

// Format renders a non-secret value the way the form shows it.
// Secret values are not formatted; use SecretValue.
func Format(cfg Config, key string) string {
	spec, ok := Lookup(key)
	if !ok || spec.Kind == KindSecret {
		return ""
	}
	switch spec.Key {
	case KeyAniListUsername:
		return cfg.AniListUsername
	case KeyNyaaURLs:
		return strings.Join(cfg.NyaaURLs, ", ")
	case KeyNyaaCategory:
		return cfg.NyaaCategory
	case KeyUserAgent:
		return cfg.UserAgent
	case KeyQBURL:
		return cfg.QBURL
	case KeyQBUsername:
		return cfg.QBUsername
	case KeyQBCategory:
		return cfg.QBCategory
	case KeyJellyfinURL:
		return cfg.JellyfinURL
	case KeyLibraryDir:
		return cfg.LibraryDir
	case KeyDownloadsDir:
		return cfg.DownloadsDir
	case KeyPreferredResolution:
		return cfg.Score.PreferredResolution
	case KeyGroupOrder:
		return strings.Join(cfg.Score.GroupOrder, ", ")
	case KeyMinSeeders:
		return strconv.Itoa(cfg.Score.MinSeeders)
	case KeyHEVCBonus:
		return strconv.Itoa(cfg.Score.HEVCBonus)
	case KeyDubPenalty:
		return strconv.Itoa(cfg.Score.DubPenalty)
	case KeyRepackBonus:
		return strconv.Itoa(cfg.Score.RepackBonus)
	case KeyMatchThreshold:
		return strconv.FormatFloat(cfg.MatchThreshold, 'f', -1, 64)
	case KeyUpgradeEnabled:
		return strconv.FormatBool(cfg.Upgrade.Enabled)
	case KeyUpgradeRepack:
		return strconv.FormatBool(cfg.Upgrade.Repack)
	case KeyUpgradeWindow:
		return formatDuration(cfg.Upgrade.Window)
	case KeyUpgradeMinDelta:
		return strconv.Itoa(cfg.Upgrade.MinDelta)
	case KeySyncInterval:
		return formatDuration(cfg.SyncInterval)
	case KeyRSSInterval:
		return formatDuration(cfg.RSSInterval)
	case KeyQBPollInterval:
		return formatDuration(cfg.QBPollInterval)
	case KeyRSSWindow:
		return formatDuration(cfg.RSSWindow)
	case KeySearchBackoff:
		return formatDuration(cfg.SearchBackoff)
	case KeyRequestInterval:
		return formatDuration(cfg.RequestInterval)
	case KeyBacklogPerPoll:
		return strconv.Itoa(cfg.BacklogPerPoll)
	case KeyNFOEnabled:
		return strconv.FormatBool(cfg.NFOEnabled)
	case KeyAutomationEnabled:
		return strconv.FormatBool(cfg.AutomationEnabled)
	case KeySonarrURL:
		return cfg.SonarrURL
	default:
		return ""
	}
}

// SecretValue is the plaintext of a secret field. It is not for the browser.
func SecretValue(cfg Config, key string) string {
	switch key {
	case KeyAniListToken:
		return cfg.AniListToken
	case KeyQBPassword:
		return cfg.QBPassword
	case KeyJellyfinAPIKey:
		return cfg.JellyfinAPIKey
	case KeySonarrAPIKey:
		return cfg.SonarrAPIKey
	default:
		return ""
	}
}

// SecretSet reports whether the effective secret is non-empty.
func SecretSet(cfg Config, key string) bool {
	return strings.TrimSpace(SecretValue(cfg, key)) != ""
}

// SecretValues lists effective secrets so errors can be redacted.
func SecretValues(cfg Config) []string {
	return []string{cfg.AniListToken, cfg.QBPassword, cfg.JellyfinAPIKey, cfg.SonarrAPIKey}
}

func validate(spec Spec, value string) error {
	if strings.ContainsRune(value, 0) {
		return fmt.Errorf("%s: invalid character", spec.Key)
	}
	if value == "" {
		if spec.AllowEmpty {
			return nil
		}
		return fmt.Errorf("%s is required", spec.Key)
	}
	switch spec.Kind {
	case KindSecret:
		if len(value) > 4096 {
			return fmt.Errorf("%s is too long", spec.Key)
		}
	case KindURL:
		if err := checkURL(value); err != nil {
			return fmt.Errorf("%s: %w", spec.Key, err)
		}
	case KindURLs:
		parts := splitComma(value)
		if len(parts) == 0 {
			return fmt.Errorf("%s is required", spec.Key)
		}
		for _, p := range parts {
			if err := checkURL(p); err != nil {
				return fmt.Errorf("%s: %w", spec.Key, err)
			}
		}
	case KindDuration:
		d, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("%s: %w", spec.Key, err)
		}
		if spec.Positive && d <= 0 {
			return fmt.Errorf("%s must be greater than 0", spec.Key)
		}
		if d < 0 {
			return fmt.Errorf("%s must be zero or greater", spec.Key)
		}
	case KindInt:
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s: enter a whole number", spec.Key)
		}
		if spec.MinSet && n < spec.Min {
			return fmt.Errorf("%s must be at least %d", spec.Key, spec.Min)
		}
	case KindFloat:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("%s: enter a number", spec.Key)
		}
		if f <= 0 || f > 1 {
			return fmt.Errorf("%s must be greater than 0 and at most 1", spec.Key)
		}
	case KindBool:
		if _, ok := parseBool(value); !ok {
			return fmt.Errorf("%s must be true or false", spec.Key)
		}
	case KindPath:
		clean := filepath.Clean(value)
		if !filepath.IsAbs(clean) {
			return fmt.Errorf("%s must be an absolute path", spec.Key)
		}
	case KindResolution:
		switch value {
		case "480p", "720p", "1080p", "2160p":
		default:
			return fmt.Errorf("%s must be 480p, 720p, 1080p, or 2160p", spec.Key)
		}
	case KindText:
		if len(value) > 2000 {
			return fmt.Errorf("%s is too long", spec.Key)
		}
		if spec.Pattern != "" {
			for _, part := range splitComma(value) {
				if !matchPattern(spec.Pattern, part) {
					return fmt.Errorf("%s: %q is not allowed", spec.Key, part)
				}
			}
			if spec.Key != KeyGroupOrder && !matchPattern(spec.Pattern, value) {
				return fmt.Errorf("%s: %q is not allowed", spec.Key, value)
			}
		}
	}
	return nil
}

func matchPattern(pattern, value string) bool {
	switch pattern {
	case "token":
		if value == "" {
			return false
		}
		for _, r := range value {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			default:
				return false
			}
		}
		return true
	default:
		return true
	}
}

func checkURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("must be an http or https URL")
	}
	return nil
}

func parseBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "yes", "y", "on":
		return true, true
	case "0", "f", "false", "no", "n", "off":
		return false, true
	default:
		return false, false
	}
}

func applyValue(cfg *Config, spec Spec, value string) error {
	switch spec.Key {
	case KeyAniListUsername:
		cfg.AniListUsername = strings.TrimSpace(value)
	case KeyAniListToken:
		cfg.AniListToken = value
	case KeyNyaaURLs:
		cfg.NyaaURLs = cleanURLs(splitComma(value))
	case KeyNyaaCategory:
		cfg.NyaaCategory = value
	case KeyUserAgent:
		cfg.UserAgent = value
	case KeyQBURL:
		cfg.QBURL = strings.TrimRight(value, "/")
	case KeyQBUsername:
		cfg.QBUsername = value
	case KeyQBPassword:
		cfg.QBPassword = value
	case KeyQBCategory:
		cfg.QBCategory = value
	case KeyJellyfinURL:
		cfg.JellyfinURL = strings.TrimRight(value, "/")
	case KeyJellyfinAPIKey:
		cfg.JellyfinAPIKey = value
	case KeyLibraryDir:
		cfg.LibraryDir = filepath.Clean(value)
	case KeyDownloadsDir:
		cfg.DownloadsDir = filepath.Clean(value)
	case KeyPreferredResolution:
		cfg.Score.PreferredResolution = value
	case KeyGroupOrder:
		cfg.Score.GroupOrder = splitComma(value)
	case KeyMinSeeders:
		n, _ := strconv.Atoi(value)
		cfg.Score.MinSeeders = n
	case KeyHEVCBonus:
		n, _ := strconv.Atoi(value)
		cfg.Score.HEVCBonus = n
	case KeyDubPenalty:
		n, _ := strconv.Atoi(value)
		cfg.Score.DubPenalty = n
	case KeyRepackBonus:
		n, _ := strconv.Atoi(value)
		cfg.Score.RepackBonus = n
	case KeyMatchThreshold:
		f, _ := strconv.ParseFloat(value, 64)
		cfg.MatchThreshold = f
	case KeyUpgradeEnabled:
		b, _ := parseBool(value)
		cfg.Upgrade.Enabled = b
	case KeyUpgradeRepack:
		b, _ := parseBool(value)
		cfg.Upgrade.Repack = b
	case KeyUpgradeWindow:
		d, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("%s: %w", spec.Key, err)
		}
		cfg.Upgrade.Window = d
	case KeyUpgradeMinDelta:
		n, _ := strconv.Atoi(value)
		cfg.Upgrade.MinDelta = n
	case KeySyncInterval:
		d, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		cfg.SyncInterval = d
	case KeyRSSInterval:
		d, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		cfg.RSSInterval = d
	case KeyQBPollInterval:
		d, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		cfg.QBPollInterval = d
	case KeyRSSWindow:
		d, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		cfg.RSSWindow = d
	case KeySearchBackoff:
		d, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		cfg.SearchBackoff = d
	case KeyRequestInterval:
		d, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		cfg.RequestInterval = d
	case KeyBacklogPerPoll:
		n, _ := strconv.Atoi(value)
		cfg.BacklogPerPoll = n
	case KeyNFOEnabled:
		b, _ := parseBool(value)
		cfg.NFOEnabled = b
	case KeyAutomationEnabled:
		b, _ := parseBool(value)
		cfg.AutomationEnabled = b
	case KeySonarrURL:
		cfg.SonarrURL = strings.TrimRight(value, "/")
	case KeySonarrAPIKey:
		cfg.SonarrAPIKey = value
	default:
		return fmt.Errorf("unknown setting %s", spec.Key)
	}
	return nil
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		return d.String()
	}
	if d != 0 && d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int64(d/time.Hour))
	}
	if d != 0 && d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int64(d/time.Minute))
	}
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", int64(d/time.Second))
	}
	return d.String()
}

var specs = []Spec{
	{Key: KeyAutomationEnabled, Label: "Automation", Help: "Off until you turn it on. AniList sync and these pages keep working. Searches, grabs, imports, NFO writes, and Jellyfin refresh do not.", Group: "Automation", Kind: KindBool, Options: []string{"true", "false"}},

	{Key: KeyAniListUsername, Label: "Username", Help: "Public AniList user. Empty skips the scheduled sync.", Group: "AniList", Kind: KindText, AllowEmpty: true},
	{Key: KeyAniListToken, Label: "Token", Help: "Optional. Leave blank to keep the current token. Stored encrypted.", Group: "AniList", Kind: KindSecret, AllowEmpty: true},

	{Key: KeyNyaaURLs, Label: "URLs", Help: "Comma-separated. The next one is tried when a request fails.", Group: "Nyaa", Kind: KindURLs},
	{Key: KeyNyaaCategory, Label: "Category", Help: "1_2 is English-translated anime.", Group: "Nyaa", Kind: KindText, Pattern: "token"},
	{Key: KeyUserAgent, Label: "User-Agent", Help: "Sent on Nyaa and AniList requests.", Group: "Nyaa", Kind: KindText},
	{Key: KeyRequestInterval, Label: "Request interval", Help: "Minimum gap between Nyaa requests, such as 2s.", Group: "Nyaa", Kind: KindDuration, Positive: true},

	{Key: KeyQBURL, Label: "URL", Group: "qBittorrent", Kind: KindURL},
	{Key: KeyQBUsername, Label: "Username", Help: "Empty username and password together skip login.", Group: "qBittorrent", Kind: KindText, AllowEmpty: true},
	{Key: KeyQBPassword, Label: "Password", Help: "Leave blank to keep the current password. Stored encrypted.", Group: "qBittorrent", Kind: KindSecret, AllowEmpty: true},
	{Key: KeyQBCategory, Label: "Category", Group: "qBittorrent", Kind: KindText, Pattern: "token"},

	{Key: KeyJellyfinURL, Label: "URL", Group: "Jellyfin", Kind: KindURL},
	{Key: KeyJellyfinAPIKey, Label: "API key", Help: "Leave blank to keep the current key. Empty skips library refresh. Stored encrypted.", Group: "Jellyfin", Kind: KindSecret, AllowEmpty: true},

	{Key: KeyLibraryDir, Label: "Library directory", Help: "Jellyfin library root inside this container.", Group: "Library", Kind: KindPath},
	{Key: KeyDownloadsDir, Label: "Downloads directory", Help: "qBittorrent download root. Grabs go in animarr/ under this.", Group: "Library", Kind: KindPath},
	{Key: KeyNFOEnabled, Label: "NFO files", Help: "Write tvshow.nfo, season.nfo, and episode .nfo files.", Group: "Library", Kind: KindBool, Options: []string{"true", "false"}},

	{Key: KeyPreferredResolution, Label: "Preferred resolution", Group: "Scoring", Kind: KindResolution, Options: []string{"480p", "720p", "1080p", "2160p"}},
	{Key: KeyGroupOrder, Label: "Group order", Help: "Earlier groups score higher. Comma-separated.", Group: "Scoring", Kind: KindText, AllowEmpty: true},
	{Key: KeyMinSeeders, Label: "Minimum seeders", Group: "Scoring", Kind: KindInt, Min: 0, MinSet: true},
	{Key: KeyHEVCBonus, Label: "x265 bonus", Help: "Added for x265, HEVC, or H.265.", Group: "Scoring", Kind: KindInt},
	{Key: KeyDubPenalty, Label: "Dub penalty", Help: "Subtracted for English-dub-only releases.", Group: "Scoring", Kind: KindInt},
	{Key: KeyRepackBonus, Label: "Repack bonus", Group: "Scoring", Kind: KindInt},
	{Key: KeyMatchThreshold, Label: "Match threshold", Help: "Title similarity from 0 to 1.", Group: "Scoring", Kind: KindFloat},
	{Key: KeyUpgradeEnabled, Label: "Upgrades", Group: "Scoring", Kind: KindBool, Options: []string{"true", "false"}},
	{Key: KeyUpgradeRepack, Label: "Repack upgrades", Group: "Scoring", Kind: KindBool, Options: []string{"true", "false"}},
	{Key: KeyUpgradeWindow, Label: "Upgrade window", Group: "Scoring", Kind: KindDuration, Positive: true},
	{Key: KeyUpgradeMinDelta, Label: "Upgrade score gap", Group: "Scoring", Kind: KindInt, Min: 0, MinSet: true},

	{Key: KeySyncInterval, Label: "AniList sync", Help: "How often the list is refreshed, such as 30m.", Group: "Scheduler", Kind: KindDuration, Positive: true},
	{Key: KeyRSSInterval, Label: "Nyaa RSS", Group: "Scheduler", Kind: KindDuration, Positive: true},
	{Key: KeyQBPollInterval, Label: "qBittorrent poll", Group: "Scheduler", Kind: KindDuration, Positive: true},
	{Key: KeyRSSWindow, Label: "RSS window", Help: "Episodes aired inside this window are left to the RSS poll. 0 searches them as backlog.", Group: "Scheduler", Kind: KindDuration},
	{Key: KeySearchBackoff, Label: "Search backoff", Help: "Minimum gap between backlog searches of one episode.", Group: "Scheduler", Kind: KindDuration},
	{Key: KeyBacklogPerPoll, Label: "Backlog per poll", Group: "Scheduler", Kind: KindInt, Min: 0, MinSet: true},

	{Key: KeySonarrURL, Label: "URL", Help: "Used only by migrate-sonarr. Empty is allowed.", Group: "Sonarr", Kind: KindURL, AllowEmpty: true},
	{Key: KeySonarrAPIKey, Label: "API key", Help: "Leave blank to keep the current key. Stored encrypted.", Group: "Sonarr", Kind: KindSecret, AllowEmpty: true},
}
