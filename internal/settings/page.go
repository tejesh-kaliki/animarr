package settings

import (
	"net/url"

	"animarr/internal/config"
)

// Page is the settings screen. It never contains a secret value.
type Page struct {
	ListenAddr string
	DataPath   string
	LogLevel   string
	KeySource  string
	KeyWarning string
	Groups     []Group
	Problems   []string
}

// Group is one section of the form.
type Group struct {
	Name   string
	Test   string
	Fields []Field
}

// Field is one input. Value is empty when Secret is set.
type Field struct {
	Key           string
	Label         string
	Help          string
	Value         string
	Source        string
	Fallback      string
	Secret        bool
	Set           bool
	DecryptFailed bool
	Kind          string
	Step          string
	Options       []Option
}

// Option is one choice in a select.
type Option struct {
	Value    string
	Label    string
	Selected bool
}

// PageFromConfig builds a read-only-looking page when no provider is attached.
// Every editable field is reported as a built-in default.
func PageFromConfig(cfg config.Config) Page {
	states := make(map[string]rowState, len(config.Specs()))
	for _, spec := range config.Specs() {
		states[spec.Key] = rowState{source: config.SourceDefault}
	}
	return buildPage(cfg, states, nil, "", "")
}

// WithPosted shows what the user typed after a validation error.
// Secret fields are not copied back into the page.
func (p Page) WithPosted(form url.Values) Page {
	if form == nil {
		return p
	}
	for gi := range p.Groups {
		for fi := range p.Groups[gi].Fields {
			f := &p.Groups[gi].Fields[fi]
			if f.Secret {
				continue
			}
			if _, ok := form[f.Key]; !ok {
				continue
			}
			f.Value = form.Get(f.Key)
			for oi := range f.Options {
				f.Options[oi].Selected = f.Options[oi].Value == f.Value
			}
		}
	}
	return p
}

func buildPage(cfg config.Config, states map[string]rowState, problems []string, keySource, keyWarning string) Page {
	page := Page{
		ListenAddr: cfg.ListenAddr,
		DataPath:   cfg.DataPath,
		LogLevel:   cfg.LogLevel,
		KeySource:  keySourceLabel(keySource),
		KeyWarning: keyWarning,
		Problems:   problems,
	}
	index := map[string]int{}
	for _, spec := range config.Specs() {
		gi, ok := index[spec.Group]
		if !ok {
			gi = len(page.Groups)
			index[spec.Group] = gi
			page.Groups = append(page.Groups, Group{Name: spec.Group, Test: groupTest(spec.Group)})
		}
		st := states[spec.Key]
		f := Field{
			Key:           spec.Key,
			Label:         spec.Label,
			Help:          spec.Help,
			Source:        string(st.source),
			Fallback:      string(st.fallback),
			Secret:        spec.Kind == config.KindSecret,
			DecryptFailed: st.decryptFailed,
			Kind:          inputKind(spec),
		}
		if f.Source == "" {
			f.Source = string(config.SourceDefault)
		}
		if f.Fallback == "" {
			f.Fallback = f.Source
		}
		if f.Secret {
			f.Set = !st.decryptFailed && config.SecretSet(cfg, spec.Key)
		} else {
			f.Value = config.Format(cfg, spec.Key)
			f.Options = optionsFor(spec, f.Value)
			if spec.Kind == config.KindFloat {
				f.Step = "0.01"
			}
			if spec.Kind == config.KindInt {
				f.Step = "1"
			}
		}
		page.Groups[gi].Fields = append(page.Groups[gi].Fields, f)
	}
	return page
}

func keySourceLabel(source string) string {
	switch source {
	case "env":
		return "environment variable ANIMARR_SECRET_KEY"
	case "file":
		return "file secret.key in the data directory"
	default:
		return "not loaded"
	}
}

func groupTest(group string) string {
	switch group {
	case "Nyaa":
		return "nyaa"
	case "qBittorrent":
		return "qbit"
	case "Jellyfin":
		return "jellyfin"
	default:
		return ""
	}
}

func inputKind(spec config.Spec) string {
	switch spec.Kind {
	case config.KindSecret:
		return "password"
	case config.KindDuration:
		return "duration"
	case config.KindInt, config.KindFloat:
		return "number"
	case config.KindBool, config.KindResolution:
		return "select"
	default:
		return "text"
	}
}

func optionsFor(spec config.Spec, current string) []Option {
	if len(spec.Options) == 0 {
		return nil
	}
	labels := map[string]string{"true": "on", "false": "off"}
	out := make([]Option, 0, len(spec.Options))
	seen := false
	for _, v := range spec.Options {
		label := v
		if l, ok := labels[v]; ok {
			label = l
		}
		out = append(out, Option{Value: v, Label: label, Selected: v == current})
		if v == current {
			seen = true
		}
	}
	if current != "" && !seen {
		out = append(out, Option{Value: current, Label: current, Selected: true})
	}
	return out
}
