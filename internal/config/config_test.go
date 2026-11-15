package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "animarr.json")
	body := []byte(`{"minSeeders":9,"nyaaCategory":"1_4","upgradeEnabled":true,"preferredResolution":"720p"}`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
	t.Setenv("SCORE_MIN_SEEDERS", "4")
	t.Setenv("QB_PASSWORD", "secret-value")
	t.Setenv("JELLYFIN_API_KEY", "jf-secret")
	t.Setenv("ANILIST_USERNAME", "alice")
	t.Setenv("LISTEN_ADDR", ":8990")
	t.Setenv("SCORE_MATCH_THRESHOLD", "0.82")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Score.MinSeeders != 4 {
		t.Fatalf("min seeders: env should win, got %d", cfg.Score.MinSeeders)
	}
	if cfg.NyaaCategory != "1_4" {
		t.Fatalf("category from file: got %s", cfg.NyaaCategory)
	}
	if !cfg.Upgrade.Enabled {
		t.Fatal("upgradeEnabled from file")
	}
	if cfg.Score.PreferredResolution != "720p" {
		t.Fatalf("resolution: got %s", cfg.Score.PreferredResolution)
	}
	if cfg.AniListUsername != "alice" {
		t.Fatalf("username: got %s", cfg.AniListUsername)
	}
	pub := cfg.Public()
	if !pub.QBPasswordSet || !pub.JellyfinKeySet {
		t.Fatalf("secrets should be flagged set: %+v", pub)
	}
	dump := strings.ToLower(pub.AniListUsername + pub.QBURL + pub.JellyfinURL)
	if strings.Contains(dump, "secret") {
		t.Fatalf("public view leaked a secret: %+v", pub)
	}
}

func TestEmptyPasswordDisablesAuth(t *testing.T) {
	t.Setenv("CONFIG_FILE", "")
	t.Setenv("QB_PASSWORD", "")
	t.Setenv("QB_USERNAME", "")
	t.Setenv("SCORE_MATCH_THRESHOLD", "0.82")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.QBPassword != "" || cfg.Public().QBPasswordSet {
		t.Fatal("empty password should mean no auth")
	}
}

func TestNFOEnabled(t *testing.T) {
	t.Setenv("CONFIG_FILE", "")
	t.Setenv("NFO_ENABLED", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.NFOEnabled || !cfg.Public().NFOEnabled {
		t.Fatal("nfo should default on")
	}
	t.Setenv("NFO_ENABLED", "off")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NFOEnabled {
		t.Fatal("NFO_ENABLED=off should disable nfo")
	}
}

func TestAutomationDefaultsOff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "animarr.json")
	if err := os.WriteFile(path, []byte(`{"automationEnabled":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", "")
	t.Setenv("AUTOMATION_ENABLED", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutomationEnabled || cfg.Public().AutomationEnabled {
		t.Fatal("automation should default off")
	}
	if Format(cfg, KeyAutomationEnabled) != "false" {
		t.Fatalf("format %q", Format(cfg, KeyAutomationEnabled))
	}
	t.Setenv("CONFIG_FILE", path)
	cfg, err = Load()
	if err != nil || !cfg.AutomationEnabled {
		t.Fatalf("file should enable: %+v %v", cfg.AutomationEnabled, err)
	}
	t.Setenv("AUTOMATION_ENABLED", "off")
	cfg, err = Load()
	if err != nil || cfg.AutomationEnabled {
		t.Fatal("env should override the file and turn automation off")
	}
	got, err := Canonical(KeyAutomationEnabled, "on")
	if err != nil || got != "true" {
		t.Fatalf("canonical %q %v", got, err)
	}
}

func TestRejectsBadThreshold(t *testing.T) {
	t.Setenv("CONFIG_FILE", "")
	t.Setenv("SCORE_MATCH_THRESHOLD", "2")
	if _, err := Load(); err == nil {
		t.Fatal("expected threshold error")
	}
}
