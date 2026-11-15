// Package web is the server-rendered UI. HTML and CSS are embedded.
package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"animarr/internal/anilist"
	"animarr/internal/config"
	"animarr/internal/importer"
	"animarr/internal/pipeline"
	"animarr/internal/settings"
	"animarr/internal/store"
)

//go:embed templates static
var assets embed.FS

// Server serves the UI and a health check.
type Server struct {
	Pipe   *pipeline.Service
	Store  *store.Store
	Config config.Config
	Log    *slog.Logger
	tmpl   *template.Template
	mux    *http.ServeMux
}

type view struct {
	Title, Nav, Body, Flash, Error string
	Dash                           store.Dash
	Shows                          []store.Show
	Filter                         string
	Show                           store.Show
	Episodes                       []store.Episode
	Query                          string
	Results                        []anilist.Media
	History                        []store.History
	Events                         []histView
	Problems                       []histView
	ProblemCount                   int
	SyncedAt                       time.Time
	Level                          string
	AirGroups                      []airGroup
	Cards                          []showCard
	Grid                           []gridCard
	Entries                        []franchiseEntry
	Sort                           string
	Rows                           []epView
	EpSummary                      string
	JumpEp                         int
	AirClock                       string
	Library                        map[int]int64
	Settings                       settings.Page
	CSRF                           string
	TestResult                     string
	TestName                       string
	Preferred                      string
	AutomationOff                  bool
	CopyFallback                   bool
}

type epView struct {
	Show        store.Show
	Episode     store.Episode
	Flash       string
	Progress    int
	HasProgress bool
	Next        bool
}

// New parses templates and routes.
func New(pipe *pipeline.Service, st *store.Store, cfg config.Config) (*Server, error) {
	s := &Server{Pipe: pipe, Store: st, Config: cfg, Log: slog.Default()}
	funcMap := template.FuncMap{
		"time": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("02 Jan 15:04")
		},
		"ptime":       formatAirDay,
		"clock":       formatClock,
		"rfc":         formatRFC,
		"human":       humanStatus,
		"humanFormat": humanFormat,
		"icon":        stateIcon,
		"lower":       strings.ToLower,
		"srcTitle":    srcTitle,
		"revert":      revertLabel,
		"letter": func(s string) string {
			s = strings.TrimSpace(s)
			if s == "" {
				return "?"
			}
			r, _ := utf8.DecodeRuneInString(s)
			return string(r)
		},
		"epRow": func(sh store.Show, ep store.Episode) epView {
			return epView{Show: sh, Episode: ep}
		},
	}
	tmpl, err := template.New("pages").Funcs(funcMap).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s.tmpl = tmpl
	mux := http.NewServeMux()
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("GET /{$}", s.dashboard)
	mux.HandleFunc("GET /shows", s.shows)
	mux.HandleFunc("GET /shows/{id}", s.show)
	mux.HandleFunc("GET /franchises/{id}", s.franchise)
	mux.HandleFunc("POST /shows/{id}/monitored", s.postMonitored)
	mux.HandleFunc("POST /shows/{id}/resolution", s.postResolution)
	mux.HandleFunc("POST /shows/{id}/episodes/{n}/ignore", s.postEpisode("ignore"))
	mux.HandleFunc("POST /shows/{id}/episodes/{n}/unignore", s.postEpisode("unignore"))
	mux.HandleFunc("POST /shows/{id}/episodes/{n}/search", s.postEpisode("search"))
	mux.HandleFunc("POST /shows/{id}/episodes/{n}/redownload", s.postEpisode("redownload"))
	mux.HandleFunc("POST /shows/add", s.postAdd)
	mux.HandleFunc("GET /search", s.search)
	mux.HandleFunc("GET /activity", s.activity)
	mux.HandleFunc("GET /settings", s.settings)
	mux.HandleFunc("POST /settings", s.postSettings)
	mux.HandleFunc("POST /sync", s.postSync)
	mux.HandleFunc("POST /settings/test/{name}", s.postTest)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /favicon.ico", s.favicon)
	s.mux = mux
	return s, nil
}

// Handler is the HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

// Run serves until ctx is cancelled.
func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		return nil
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.log().Error("template", "name", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (s *Server) page(w http.ResponseWriter, v view) {
	if v.Title == "" {
		v.Title = "animarr"
	}
	s.fill(&v)
	s.render(w, "page", v)
}

func (s *Server) fill(v *view) {
	cfg := s.Config
	if s.Pipe != nil && s.Pipe.Settings != nil {
		cur, err := s.Pipe.Settings.Current(context.Background())
		if err != nil {
			s.log().Error("settings", "err", err)
		}
		cfg = cur
	}
	v.AutomationOff = !cfg.AutomationEnabled
	v.CopyFallback = importer.LastImportCopied()
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error, code int) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(code)
		fmt.Fprintf(w, `<p class="error" id="flash">%s</p>`, template.HTMLEscapeString(err.Error()))
		return
	}
	http.Error(w, err.Error(), code)
}

func (s *Server) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func hx(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }
