package settings

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"animarr/internal/config"
	"animarr/internal/score"
	"animarr/internal/secret"
	"animarr/internal/store"
)

func TestPrecedenceSavedOverEnvOverDefault(t *testing.T) {
	st, prov := newProv(t, map[string]config.Source{
		config.KeyAniListUsername: config.SourceEnv,
		config.KeyLibraryDir:      config.SourceDefault,
		config.KeyNyaaCategory:    config.SourceFile,
	})
	ctx := context.Background()
	cur, err := prov.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cur.AniListUsername != "from-env" || cur.LibraryDir != "/library" || cur.NyaaCategory != "1_4" {
		t.Fatalf("%+v", cur)
	}
	page := prov.Page(ctx)
	if src := field(page, config.KeyAniListUsername).Source; src != string(config.SourceEnv) {
		t.Fatalf("username source %s", src)
	}
	if src := field(page, config.KeyLibraryDir).Source; src != string(config.SourceDefault) {
		t.Fatalf("library source %s", src)
	}
	if src := field(page, config.KeyNyaaCategory).Source; src != string(config.SourceFile) {
		t.Fatalf("category source %s", src)
	}

	if err := prov.Save(ctx, url.Values{
		config.KeyLibraryDir:      {"/data/media/anime"},
		config.KeyAniListUsername: {"from-env"},
		config.KeyNyaaCategory:    {"1_4"},
	}); err != nil {
		t.Fatal(err)
	}
	cur, err = prov.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cur.LibraryDir != "/data/media/anime" {
		t.Fatalf("library %s", cur.LibraryDir)
	}
	if cur.AniListUsername != "from-env" {
		t.Fatal("unchanged username should stay on the env layer")
	}
	rows, err := st.ListSettings(ctx)
	if err != nil || len(rows) != 1 || rows[0].Key != config.KeyLibraryDir {
		t.Fatalf("rows %+v %v", rows, err)
	}
	saved := field(prov.Page(ctx), config.KeyLibraryDir)
	if saved.Source != string(config.SourceSaved) || saved.Fallback != string(config.SourceDefault) {
		t.Fatalf("library source %s fallback %s", saved.Source, saved.Fallback)
	}

	if err := prov.Save(ctx, url.Values{config.KeyAniListUsername: {"from-ui"}}); err != nil {
		t.Fatal(err)
	}
	cur, _ = prov.Current(ctx)
	if cur.AniListUsername != "from-ui" {
		t.Fatalf("saved should override env, got %s", cur.AniListUsername)
	}
	if err := prov.Reset(ctx, config.KeyAniListUsername); err != nil {
		t.Fatal(err)
	}
	cur, _ = prov.Current(ctx)
	if cur.AniListUsername != "from-env" {
		t.Fatalf("reset should restore env, got %s", cur.AniListUsername)
	}
	if err := prov.Reset(ctx, config.KeyLibraryDir); err != nil {
		t.Fatal(err)
	}
	cur, _ = prov.Current(ctx)
	if cur.LibraryDir != "/library" {
		t.Fatalf("reset should restore default, got %s", cur.LibraryDir)
	}
}

func TestSecretRoundTripHiddenAndDecryptFailure(t *testing.T) {
	st, prov := newProv(t, map[string]config.Source{
		config.KeyJellyfinAPIKey: config.SourceEnv,
	})
	ctx := context.Background()
	const secretValue = "jellyfin-secret-value-zz"
	if err := prov.Save(ctx, url.Values{config.KeyJellyfinAPIKey: {secretValue}}); err != nil {
		t.Fatal(err)
	}
	cur, err := prov.Current(ctx)
	if err != nil || cur.JellyfinAPIKey != secretValue {
		t.Fatalf("effective %q %v", cur.JellyfinAPIKey, err)
	}
	row, err := st.GetSetting(ctx, config.KeyJellyfinAPIKey)
	if err != nil || !row.Secret || !strings.HasPrefix(row.Value, "v1:") || strings.Contains(row.Value, secretValue) {
		t.Fatalf("stored %+v %v", row, err)
	}
	page := prov.Page(ctx)
	f := field(page, config.KeyJellyfinAPIKey)
	if !f.Set || f.Value != "" || f.Secret == false {
		t.Fatalf("field %+v", f)
	}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), secretValue) || strings.Contains(string(body), row.Value) {
		t.Fatalf("page json leaked a secret: %s", body)
	}

	other := settingsWithBox(st, prov, secret.NewBox(keyByte(9)))
	cur, err = other.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cur.JellyfinAPIKey != "" {
		t.Fatalf("unreadable secret should be unset, got %q", cur.JellyfinAPIKey)
	}
	page = other.Page(ctx)
	if !field(page, config.KeyJellyfinAPIKey).DecryptFailed {
		t.Fatal("expected decrypt failure on the page")
	}
	if !strings.Contains(strings.Join(page.Problems, " "), "could not be decrypted") {
		t.Fatalf("problems %v", page.Problems)
	}
	kept, err := st.GetSetting(ctx, config.KeyJellyfinAPIKey)
	if err != nil || kept.Value != row.Value {
		t.Fatal("decrypt failure must keep the row")
	}
	again, err := json.Marshal(page)
	if err != nil || strings.Contains(string(again), secretValue) {
		t.Fatalf("failed page leaked secret: %s", again)
	}
}

func TestValidationAndPreview(t *testing.T) {
	_, prov := newProv(t, nil)
	ctx := context.Background()
	err := prov.Save(ctx, url.Values{config.KeyLibraryDir: {"not/absolute"}})
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("%v", err)
	}
	rows, errList := prov.store.ListSettings(ctx)
	if errList != nil || len(rows) != 0 {
		t.Fatalf("invalid save wrote rows %+v %v", rows, errList)
	}
	cfg, err := prov.Preview(ctx, url.Values{config.KeyQBURL: {"http://127.0.0.1:9090"}})
	if err != nil || cfg.QBURL != "http://127.0.0.1:9090" {
		t.Fatalf("preview %s %v", cfg.QBURL, err)
	}
	cur, _ := prov.Current(ctx)
	if cur.QBURL == cfg.QBURL {
		t.Fatal("preview must not save")
	}
	if _, err := prov.Preview(ctx, url.Values{config.KeyQBURL: {"not a url"}}); err == nil {
		t.Fatal("expected preview validation error")
	}
}

func TestRotateRewritesSecretsAndRollsBack(t *testing.T) {
	st, prov := newProv(t, nil)
	ctx := context.Background()
	if err := prov.Save(ctx, url.Values{
		config.KeyJellyfinAPIKey: {"jf-one"},
		config.KeyQBPassword:     {"qb-one"},
	}); err != nil {
		t.Fatal(err)
	}
	oldBox := prov.box
	newBox := secret.NewBox(keyByte(7))
	n, err := Rotate(ctx, st, oldBox, newBox)
	if err != nil || n != 2 {
		t.Fatalf("rotate %d %v", n, err)
	}
	for _, key := range []string{config.KeyJellyfinAPIKey, config.KeyQBPassword} {
		row, err := st.GetSetting(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := oldBox.Decrypt(key, row.Value); !errors.Is(err, secret.ErrDecrypt) {
			t.Fatalf("old key still opens %s: %v", key, err)
		}
		if _, err := newBox.Decrypt(key, row.Value); err != nil {
			t.Fatalf("new key: %v", err)
		}
	}

	good, err := st.GetSetting(ctx, config.KeyJellyfinAPIKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSetting(ctx, store.Setting{
		Key: config.KeyQBPassword, Value: "v1:not-valid", Secret: true, UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	newer := secret.NewBox(keyByte(8))
	if _, err := Rotate(ctx, st, newBox, newer); err == nil {
		t.Fatal("expected rotate to fail on a bad row")
	}
	row, err := st.GetSetting(ctx, config.KeyJellyfinAPIKey)
	if err != nil || row.Value != good.Value {
		t.Fatal("failed rotate changed a good row")
	}
	if _, err := newBox.Decrypt(config.KeyJellyfinAPIKey, row.Value); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownSavedKeyIsIgnored(t *testing.T) {
	st, prov := newProv(t, nil)
	ctx := context.Background()
	if err := st.UpsertSetting(ctx, store.Setting{
		Key: "SONARR_URL", Value: "http://127.0.0.1:8989", UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSetting(ctx, store.Setting{
		Key: "SONARR_API_KEY", Value: "ciphertext", Secret: true, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := prov.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AniListUsername != "from-env" || cfg.LibraryDir != "/library" {
		t.Fatalf("unknown rows changed config: %+v", cfg)
	}
	page := prov.Page(ctx)
	for _, p := range page.Problems {
		if strings.Contains(p, "SONARR") {
			t.Fatalf("unknown key surfaced: %s", p)
		}
	}
}

func newProv(t *testing.T, origin map[string]config.Source) (*store.Store, *Provider) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "animarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	base := config.Config{
		AniListUsername: "from-env",
		ListenAddr:      ":8990",
		DataPath:        dir,
		LibraryDir:      "/library",
		DownloadsDir:    "/downloads",
		NyaaURLs:        []string{"https://nyaa.si"},
		NyaaCategory:    "1_4",
		UserAgent:       "animarr-test",
		QBURL:           "http://127.0.0.1:8080",
		QBCategory:      "animarr",
		JellyfinURL:     "http://jellyfin:8096",
		JellyfinAPIKey:  "env-jellyfin-key",
		SyncInterval:    30 * time.Minute,
		RSSInterval:     15 * time.Minute,
		QBPollInterval:  time.Minute,
		RSSWindow:       6 * time.Hour,
		SearchBackoff:   6 * time.Hour,
		RequestInterval: 2 * time.Second,
		BacklogPerPoll:  3,
		Score:           score.DefaultConfig(),
		Upgrade:         score.Upgrade{Repack: true, Window: 72 * time.Hour, MinDelta: 15},
		MatchThreshold:  0.82,
		NFOEnabled:      true,
		LogLevel:        "info",
	}
	return st, New(base, origin, st, secret.Loaded{Box: secret.NewBox(keyByte(3)), Source: "file"})
}

func settingsWithBox(st *store.Store, prev *Provider, box *secret.Box) *Provider {
	return New(prev.base, prev.origin, st, secret.Loaded{Box: box, Source: "env"})
}

func field(page Page, key string) Field {
	for _, g := range page.Groups {
		for _, f := range g.Fields {
			if f.Key == key {
				return f
			}
		}
	}
	return Field{}
}

func keyByte(b byte) [32]byte {
	var key [32]byte
	for i := range key {
		key[i] = b
	}
	return key
}
