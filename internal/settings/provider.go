// Package settings overlays values saved in the UI on the env and file configuration.
// Saved wins over the environment, which wins over CONFIG_FILE, which wins over built-in defaults.
package settings

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"animarr/internal/config"
	"animarr/internal/secret"
	"animarr/internal/store"
)

// ValidationError is one or more rejected fields. Nothing was written.
type ValidationError struct {
	Messages []string
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Messages) == 0 {
		return "invalid settings"
	}
	return strings.Join(e.Messages, "; ")
}

// Provider reads the settings table on each call so a save applies on the next cycle.
type Provider struct {
	base       config.Config
	origin     map[string]config.Source
	store      *store.Store
	box        *secret.Box
	keySource  string
	keyWarning string
	logger     *slog.Logger
}

// New builds a provider. base is the env/file/default config. loaded supplies the cipher.
func New(base config.Config, origin map[string]config.Source, st *store.Store, loaded secret.Loaded) *Provider {
	if origin == nil {
		origin = map[string]config.Source{}
	}
	return &Provider{
		base:       base.Clone(),
		origin:     origin,
		store:      st,
		box:        loaded.Box,
		keySource:  loaded.Source,
		keyWarning: loaded.Warning,
		logger:     slog.Default(),
	}
}

// Current is the config the pipeline and scheduler should use.
// A decrypt failure leaves that secret empty and does not return an error.
// A database error returns the env/file config and the error.
func (p *Provider) Current(ctx context.Context) (config.Config, error) {
	cfg, _, _, err := p.overlay(ctx)
	return cfg, err
}

// Page is the settings screen. Secret values are not copied onto it.
func (p *Provider) Page(ctx context.Context) Page {
	cfg, states, problems, err := p.overlay(ctx)
	if err != nil {
		problems = append(problems, "Saved settings could not be read. Showing environment and defaults.")
	}
	return buildPage(cfg, states, problems, p.keySource, p.keyWarning)
}

// Save writes the posted form. Blank secret fields are left unchanged.
// empty_<KEY>=1 stores an empty secret and overrides the environment.
func (p *Provider) Save(ctx context.Context, form url.Values) error {
	if p.box == nil {
		return errNoKey
	}
	current, _, _, _ := p.overlay(ctx)
	var errs []string
	type item struct {
		key, value string
		secret     bool
	}
	var items []item
	for _, spec := range config.Specs() {
		if spec.Kind == config.KindSecret {
			if form.Get("empty_"+spec.Key) == "1" {
				items = append(items, item{key: spec.Key, secret: true})
				continue
			}
			raw := form.Get(spec.Key)
			if raw == "" {
				continue
			}
			canon, err := config.Canonical(spec.Key, raw)
			if err != nil {
				errs = append(errs, safeErr(err))
				continue
			}
			items = append(items, item{key: spec.Key, value: canon, secret: true})
			continue
		}
		if _, ok := form[spec.Key]; !ok {
			continue
		}
		canon, err := config.Canonical(spec.Key, form.Get(spec.Key))
		if err != nil {
			errs = append(errs, safeErr(err))
			continue
		}
		if canon == config.Format(current, spec.Key) {
			continue
		}
		items = append(items, item{key: spec.Key, value: canon})
	}
	if len(errs) > 0 {
		return &ValidationError{Messages: errs}
	}
	now := time.Now().UTC()
	for _, it := range items {
		stored := it.value
		if it.secret {
			enc, err := p.box.Encrypt(it.key, it.value)
			if err != nil {
				return errNoKey
			}
			stored = enc
		}
		if err := p.store.UpsertSetting(ctx, store.Setting{
			Key: it.key, Value: stored, Secret: it.secret, UpdatedAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

// Reset deletes the saved row so the env, file, or default applies again.
func (p *Provider) Reset(ctx context.Context, key string) error {
	if !config.Editable(key) {
		return &ValidationError{Messages: []string{"unknown setting " + key}}
	}
	return p.store.DeleteSetting(ctx, key)
}

// Preview applies typed form values on top of the effective config without saving.
// Blank secrets keep the effective value. Invalid fields return ValidationError.
func (p *Provider) Preview(ctx context.Context, form url.Values) (config.Config, error) {
	cfg, _, _, err := p.overlay(ctx)
	var errs []string
	if err != nil {
		errs = append(errs, "could not read saved settings")
	}
	if form == nil {
		if len(errs) > 0 {
			return cfg, &ValidationError{Messages: errs}
		}
		return cfg, nil
	}
	for _, spec := range config.Specs() {
		if spec.Kind == config.KindSecret {
			if form.Get("empty_"+spec.Key) == "1" {
				if e := config.Apply(&cfg, spec.Key, ""); e != nil {
					errs = append(errs, safeErr(e))
				}
				continue
			}
			raw := form.Get(spec.Key)
			if raw == "" {
				continue
			}
			if e := config.Apply(&cfg, spec.Key, raw); e != nil {
				errs = append(errs, safeErr(e))
			}
			continue
		}
		if _, ok := form[spec.Key]; !ok {
			continue
		}
		if e := config.Apply(&cfg, spec.Key, form.Get(spec.Key)); e != nil {
			errs = append(errs, safeErr(e))
		}
	}
	if len(errs) > 0 {
		return cfg, &ValidationError{Messages: errs}
	}
	return cfg, nil
}

type rowState struct {
	source        config.Source
	fallback      config.Source
	decryptFailed bool
}

func (p *Provider) overlay(ctx context.Context) (config.Config, map[string]rowState, []string, error) {
	cfg := p.base.Clone()
	states := make(map[string]rowState, len(config.Specs()))
	for _, spec := range config.Specs() {
		src := config.SourceDefault
		if s, ok := p.origin[spec.Key]; ok {
			src = s
		}
		states[spec.Key] = rowState{source: src}
	}
	if p.store == nil {
		return cfg, states, []string{"settings store is not available"}, errNoStore
	}
	rows, err := p.store.ListSettings(ctx)
	if err != nil {
		return cfg, states, nil, err
	}
	var problems []string
	for _, row := range rows {
		spec, ok := config.Lookup(row.Key)
		if !ok {
			continue
		}
		st := states[row.Key]
		if st.fallback == "" {
			st.fallback = st.source
		}
		st.source = config.SourceSaved
		val := row.Value
		if row.Secret || spec.Kind == config.KindSecret {
			if p.box == nil {
				st.decryptFailed = true
				_ = config.Apply(&cfg, row.Key, "")
				states[row.Key] = st
				problems = append(problems, decryptProblem(row.Key))
				continue
			}
			plain, derr := p.box.Decrypt(row.Key, row.Value)
			if derr != nil {
				st.decryptFailed = true
				_ = config.Apply(&cfg, row.Key, "")
				states[row.Key] = st
				problems = append(problems, decryptProblem(row.Key))
				p.log().Warn("setting decrypt failed", "key", row.Key)
				continue
			}
			val = plain
		}
		if err := config.Apply(&cfg, row.Key, val); err != nil {
			states[row.Key] = st
			problems = append(problems, row.Key+": saved value was ignored ("+safeErr(err)+")")
			p.log().Warn("setting ignored", "key", row.Key)
			continue
		}
		states[row.Key] = st
	}
	return cfg, states, problems, nil
}

func decryptProblem(key string) string {
	return key + " could not be decrypted and is treated as unset. The saved value was kept. Reset it or enter a new one."
}

func (p *Provider) log() *slog.Logger {
	if p != nil && p.logger != nil {
		return p.logger
	}
	return slog.Default()
}

func safeErr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

var (
	errNoKey   = errString("secret key is not available")
	errNoStore = errString("settings store is not available")
)

type errString string

func (e errString) Error() string { return string(e) }
