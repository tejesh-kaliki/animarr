// Package pipeline connects AniList, the indexer, qBittorrent, and the library.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"animarr/internal/anilist"
	"animarr/internal/config"
	"animarr/internal/franchise"
	"animarr/internal/importer"
	"animarr/internal/indexer"
	"animarr/internal/jellyfin"
	"animarr/internal/match"
	"animarr/internal/nfo"
	"animarr/internal/nyaa"
	"animarr/internal/parser"
	"animarr/internal/qbit"
	"animarr/internal/score"
	"animarr/internal/settings"
	"animarr/internal/store"
)

// ErrNoRelease means a targeted search found nothing acceptable.
var ErrNoRelease = errors.New("no matching release")

// ErrAutomationOff is returned when a manual action would search, grab, import,
// or refresh Jellyfin while AUTOMATION_ENABLED is false.
var ErrAutomationOff = errors.New("Automation is off: enable it in Settings")

// Downloader is the qBittorrent surface the pipeline needs.
type Downloader interface {
	Test(ctx context.Context) (string, error)
	Add(ctx context.Context, req qbit.AddRequest) (string, error)
	Get(ctx context.Context, hash string) (qbit.Torrent, error)
	Files(ctx context.Context, hash string) ([]qbit.File, error)
}

// Library refreshes Jellyfin. A nil library skips the refresh.
type Library interface {
	Enabled() bool
	Test(ctx context.Context) (string, error)
	Refresh(ctx context.Context) error
}

// Service is safe for the scheduler and the web UI to call together.
type Service struct {
	Config   config.Config
	Settings *settings.Provider
	Store    *store.Store
	AniList  *anilist.Client
	Indexer  indexer.Indexer
	Qbit     Downloader
	Jellyfin Library
	Log      *slog.Logger

	mu sync.Mutex
}

// live returns the config for this call. When Settings is set, saved values
// override the copy in Config and the clients are pointed at the new endpoints.
func (s *Service) live(ctx context.Context) config.Config {
	if s == nil || s.Settings == nil {
		if s == nil {
			return config.Config{}
		}
		return s.Config
	}
	cfg, err := s.Settings.Current(ctx)
	if err != nil {
		s.log().Error("settings", "err", err)
	}
	if cfg.DataPath == "" && s.Config.DataPath != "" {
		return s.Config
	}
	s.bind(cfg)
	return cfg
}

func (s *Service) bind(cfg config.Config) {
	if s.AniList != nil {
		s.AniList.Configure(cfg.UserAgent, cfg.AniListToken)
	}
	if n, ok := s.Indexer.(*nyaa.Client); ok {
		n.Configure(cfg.NyaaURLs, cfg.NyaaCategory, cfg.UserAgent, cfg.RequestInterval)
	}
	if q, ok := s.Qbit.(*qbit.Client); ok {
		q.Configure(cfg.QBURL, cfg.QBUsername, cfg.QBPassword)
	}
	if j, ok := s.Jellyfin.(*jellyfin.Client); ok {
		j.Configure(cfg.JellyfinURL, cfg.JellyfinAPIKey)
	}
}

type picked struct {
	rel    indexer.Release
	parsed parser.Parsed
	score  int
}

// SyncAniList refreshes CURRENT and PLANNING shows.
// Both start monitored. A later sync does not turn monitoring back on.
func (s *Service) SyncAniList(ctx context.Context) error {
	cfg := s.live(ctx)
	if strings.TrimSpace(cfg.AniListUsername) == "" {
		return fmt.Errorf("ANILIST_USERNAME is not set")
	}
	entries, err := s.AniList.ListCollection(ctx, cfg.AniListUsername)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, e := range entries {
		status := e.Status
		if status == "" {
			status = "CURRENT"
		}
		if err := s.upsertMedia(ctx, e.Media, status, true, now); err != nil {
			return err
		}
	}
	if err := s.assignFranchises(ctx); err != nil {
		return err
	}
	s.history(ctx, "info", fmt.Sprintf("synced %d shows from AniList", len(entries)), 0, 0)
	return nil
}

// Search queries AniList.
func (s *Service) Search(ctx context.Context, text string) ([]anilist.Media, error) {
	s.live(ctx)
	return s.AniList.Search(ctx, text)
}

// AddShow fetches an anime and monitors it, even if it is not on the user's list.
func (s *Service) AddShow(ctx context.Context, anilistID int) error {
	s.live(ctx)
	media, err := s.AniList.Get(ctx, anilistID)
	if err != nil {
		return err
	}
	status := "MANUAL"
	if existing, err := s.Store.GetShowByAniList(ctx, anilistID); err == nil && existing.ListStatus != "" && existing.ListStatus != "MANUAL" {
		status = existing.ListStatus
	}
	if err := s.upsertMedia(ctx, media, status, false, time.Now()); err != nil {
		return err
	}
	saved, err := s.Store.GetShowByAniList(ctx, anilistID)
	if err != nil {
		return err
	}
	if err := s.Store.SetMonitored(ctx, saved.ID, true); err != nil {
		return err
	}
	if err := s.assignFranchises(ctx); err != nil {
		return err
	}
	s.history(ctx, "info", "added "+saved.DisplayTitle(), saved.ID, 0)
	return nil
}

// maxFranchiseSeasons stops a runaway relation graph.
const maxFranchiseSeasons = 25

// AddFranchise adds an anime and every TV season connected to it through
// SEQUEL and PREQUEL relations. Movies, OVAs and specials are not added.
// It returns how many AniList entries it added or refreshed.
func (s *Service) AddFranchise(ctx context.Context, anilistID int) (int, error) {
	seen := map[int]bool{anilistID: true}
	queue := []int{anilistID}
	var ids []int
	for len(queue) > 0 && len(ids) < maxFranchiseSeasons {
		id := queue[0]
		queue = queue[1:]
		media, err := s.AniList.Get(ctx, id)
		if err != nil {
			if id == anilistID {
				return 0, err
			}
			continue
		}
		if id == anilistID || franchise.TV(media.Format) {
			ids = append(ids, id)
		}
		for _, e := range media.Relations.Edges {
			if e.RelationType != "SEQUEL" && e.RelationType != "PREQUEL" {
				continue
			}
			if e.Node.Type != "ANIME" || !franchise.TV(e.Node.Format) || seen[e.Node.ID] {
				continue
			}
			seen[e.Node.ID] = true
			queue = append(queue, e.Node.ID)
		}
	}
	for _, id := range ids {
		if err := s.AddShow(ctx, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// SetMonitored toggles a show.
func (s *Service) SetMonitored(ctx context.Context, id int64, on bool) error {
	return s.Store.SetMonitored(ctx, id, on)
}

// SetResolution stores a per-show resolution override.
func (s *Service) SetResolution(ctx context.Context, id int64, res string) error {
	switch res {
	case "", "480p", "720p", "1080p", "2160p":
	default:
		return fmt.Errorf("unsupported resolution %q", res)
	}
	return s.Store.SetResolution(ctx, id, res)
}

// IgnoreEpisode marks or clears the ignored state.
func (s *Service) IgnoreEpisode(ctx context.Context, showID int64, number int, ignore bool) error {
	if ignore {
		return s.Store.SetEpisodeState(ctx, showID, number, store.StateIgnored)
	}
	ep, err := s.Store.GetEpisode(ctx, showID, number)
	if err != nil {
		return err
	}
	sh, err := s.Store.GetShow(ctx, showID)
	if err != nil {
		return err
	}
	return s.Store.SetEpisodeState(ctx, showID, number, DesiredState(number, ep.AirDate, sh.NextAiringEpisode, time.Now()))
}

// SearchEpisode searches Nyaa for one episode now.
func (s *Service) SearchEpisode(ctx context.Context, showID int64, number int) error {
	cfg := s.live(ctx)
	if err := automationErr(cfg); err != nil {
		return err
	}
	sh, ep, err := s.loadEp(ctx, showID, number)
	if err != nil {
		return err
	}
	if ep.State == store.StateIgnored {
		return fmt.Errorf("episode is ignored")
	}
	_ = s.Store.TouchSearch(ctx, showID, number, time.Now())
	rels, err := s.searchTitles(ctx, cfg, sh)
	if err != nil {
		return err
	}
	return s.consider(ctx, cfg, sh, ep, rels, true)
}

// Redownload searches again and replaces the grab even if a file is already imported.
func (s *Service) Redownload(ctx context.Context, showID int64, number int) error {
	return s.SearchEpisode(ctx, showID, number)
}

// PollRSS matches the recent feed, then searches a few older wanted episodes.
func (s *Service) PollRSS(ctx context.Context) error {
	cfg := s.live(ctx)
	if err := automationErr(cfg); err != nil {
		return nil
	}
	if s.Indexer == nil {
		return fmt.Errorf("no indexer configured")
	}
	rels, err := s.Indexer.Recent(ctx, cfg.NyaaCategory)
	if err != nil {
		return err
	}
	shows, err := s.Store.ListShows(ctx, "monitored")
	if err != nil {
		return err
	}
	cands := make([]match.Candidate, 0, len(shows))
	byID := make(map[int64]store.Show, len(shows))
	for _, sh := range shows {
		cands = append(cands, match.Candidate{Key: sh.ID, Titles: sh.AllTitles()})
		byID[sh.ID] = sh
	}
	type epKey struct {
		show int64
		ep   int
	}
	grouped := map[epKey][]indexer.Release{}
	seen := map[epKey]bool{}
	for _, rel := range rels {
		p := parser.Parse(rel.Title)
		if p.Batch || strings.TrimSpace(p.Title) == "" {
			continue
		}
		hit, _, ok := match.Best(p.Title, cands, cfg.MatchThreshold)
		if !ok {
			continue
		}
		sh := byID[hit.Key]
		n := p.Episode
		if strings.EqualFold(sh.Format, "MOVIE") && (n == 0 || n == 1) {
			n = 1
		}
		if n <= 0 {
			continue
		}
		k := epKey{sh.ID, n}
		grouped[k] = append(grouped[k], rel)
		seen[k] = true
	}
	for k, list := range grouped {
		ep, err := s.Store.GetEpisode(ctx, k.show, k.ep)
		if err != nil {
			continue
		}
		if err := s.consider(ctx, cfg, byID[k.show], ep, list, false); err != nil && !errors.Is(err, ErrNoRelease) && !errors.Is(err, ErrAutomationOff) {
			s.log().Warn("rss grab", "err", err, "show", byID[k.show].DisplayTitle(), "episode", k.ep)
		}
	}

	window := cfg.RSSWindow
	if window <= 0 {
		window = 6 * time.Hour
	}
	backoff := cfg.SearchBackoff
	if backoff <= 0 {
		backoff = 6 * time.Hour
	}
	limit := cfg.BacklogPerPoll
	if limit < 0 {
		limit = 0
	}
	now := time.Now()
	backlog, err := s.Store.WantedBacklog(ctx, now.Add(-window), now.Add(-backoff), limit)
	if err != nil {
		return err
	}
	for _, item := range backlog {
		if seen[epKey{item.Show.ID, item.Episode.Number}] {
			continue
		}
		_ = s.Store.TouchSearch(ctx, item.Show.ID, item.Episode.Number, now)
		rels, err := s.searchTitles(ctx, cfg, item.Show)
		if err != nil {
			s.history(ctx, "warn", "search failed: "+err.Error(), item.Show.ID, item.Episode.Number)
			continue
		}
		if err := s.consider(ctx, cfg, item.Show, item.Episode, rels, false); err != nil && !errors.Is(err, ErrNoRelease) && !errors.Is(err, ErrAutomationOff) {
			s.history(ctx, "warn", "search failed: "+err.Error(), item.Show.ID, item.Episode.Number)
		}
	}
	return nil
}

// PollDownloads imports torrents that have finished.
func (s *Service) PollDownloads(ctx context.Context) error {
	cfg := s.live(ctx)
	if err := automationErr(cfg); err != nil {
		return nil
	}
	if s.Qbit == nil {
		return nil
	}
	rows, err := s.Store.ListDownloadsByState(ctx, []string{store.DlDownloading})
	if err != nil {
		return err
	}
	var first error
	for _, d := range rows {
		if err := s.pollOne(ctx, cfg, d); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// PromoteAired moves episodes whose air time has passed to wanted.
func (s *Service) PromoteAired(ctx context.Context) error {
	_, err := s.Store.PromoteAired(ctx, time.Now())
	return err
}

// CheckQbit tests the qBittorrent values in cfg, including ones not saved yet.
func CheckQbit(ctx context.Context, cfg config.Config) (string, error) {
	if strings.TrimSpace(cfg.QBURL) == "" {
		return "", fmt.Errorf("qBittorrent is not configured")
	}
	c, err := qbit.New(cfg.QBURL, cfg.QBUsername, cfg.QBPassword)
	if err != nil {
		return "", err
	}
	return c.Test(ctx)
}

// CheckJellyfin tests the Jellyfin values in cfg.
func CheckJellyfin(ctx context.Context, cfg config.Config) (string, error) {
	c := jellyfin.New(cfg.JellyfinURL, cfg.JellyfinAPIKey)
	if !c.Enabled() {
		return "", fmt.Errorf("Jellyfin is not configured")
	}
	return c.Test(ctx)
}

// CheckNyaa reads one recent page from the Nyaa values in cfg.
func CheckNyaa(ctx context.Context, cfg config.Config) (string, error) {
	c := nyaa.New(cfg.NyaaURLs, cfg.NyaaCategory, cfg.UserAgent, cfg.RequestInterval)
	rels, err := c.Recent(ctx, cfg.NyaaCategory)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d recent releases", len(rels)), nil
}

func (s *Service) upsertMedia(ctx context.Context, m anilist.Media, listStatus string, preserve bool, now time.Time) error {
	// CURRENT and PLANNING both start monitored. preserve leaves a cleared flag cleared.
	monitored := true
	var nextAt *time.Time
	if m.NextAiringEpisode != nil && m.NextAiringEpisode.AiringAt > 0 {
		t := time.Unix(m.NextAiringEpisode.AiringAt, 0).UTC()
		nextAt = &t
	}
	sh, err := s.Store.UpsertShow(ctx, store.Show{
		AniListID:         m.ID,
		TitleRomaji:       m.Title.Romaji,
		TitleEnglish:      m.Title.English,
		TitleNative:       m.Title.Native,
		Synonyms:          m.Synonyms,
		Format:            m.Format,
		Status:            m.Status,
		ListStatus:        listStatus,
		Episodes:          m.EpisodeTotal(),
		NextAiringEpisode: m.NextEpisode(),
		NextAiringAt:      nextAt,
		CoverImage:        m.CoverURL(),
		Season:            m.Season,
		SeasonYear:        m.SeasonYearValue(),
		StartYear:         m.StartYear(),
		StartMonth:        m.StartMonth(),
		StartDay:          m.StartDay(),
		Monitored:         monitored,
		RelationsJSON:     m.RelationsJSON(),
		Description:       m.Description,
		Genres:            m.Genres,
		Studio:            m.StudioName(),
	}, preserve)
	if err != nil {
		return err
	}
	airs := m.AirTimes()
	total := m.EpisodeTotal()
	for n := 1; n <= total; n++ {
		var air *time.Time
		if t, ok := airs[n]; ok {
			tt := t
			air = &tt
		}
		if err := s.Store.EnsureEpisode(ctx, sh.ID, n, air, DesiredState(n, air, m.NextEpisode(), now)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) searchTitles(ctx context.Context, cfg config.Config, sh store.Show) ([]indexer.Release, error) {
	if s.Indexer == nil {
		return nil, fmt.Errorf("no indexer configured")
	}
	var texts []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		for _, e := range texts {
			if strings.EqualFold(e, v) {
				return
			}
		}
		texts = append(texts, v)
	}
	add(sh.TitleRomaji)
	add(sh.TitleEnglish)
	if len(texts) == 0 {
		add(sh.TitleNative)
	}
	var lists [][]indexer.Release
	for _, text := range texts {
		rels, err := s.Indexer.Search(ctx, indexer.Query{Text: text, Category: cfg.NyaaCategory})
		if err != nil {
			return nil, err
		}
		lists = append(lists, rels)
	}
	return indexer.Dedupe(lists...), nil
}

func (s *Service) consider(ctx context.Context, cfg config.Config, sh store.Show, ep store.Episode, rels []indexer.Release, force bool) error {
	best, ok := s.pick(cfg, sh, ep, rels)
	if !ok {
		if force {
			return ErrNoRelease
		}
		return nil
	}
	return s.grab(ctx, cfg, sh, ep, best, force)
}

func (s *Service) pick(cfg config.Config, sh store.Show, ep store.Episode, rels []indexer.Release) (picked, bool) {
	var best picked
	found := false
	cands := []match.Candidate{{Key: sh.ID, Titles: sh.AllTitles()}}
	for _, rel := range rels {
		p := parser.Parse(rel.Title)
		if p.Batch {
			continue
		}
		if strings.EqualFold(sh.Format, "MOVIE") {
			if p.Episode != 0 && p.Episode != ep.Number {
				continue
			}
		} else if p.Episode != ep.Number {
			continue
		}
		if _, _, ok := match.Best(p.Title, cands, cfg.MatchThreshold); !ok {
			continue
		}
		total, ok := score.Score(p, rel.Seeders, sh.PreferredResolution, cfg.Score)
		if !ok {
			continue
		}
		if !found || total > best.score {
			best = picked{rel: rel, parsed: p, score: total}
			found = true
		}
	}
	return best, found
}

func (s *Service) grab(ctx context.Context, cfg config.Config, sh store.Show, ep store.Episode, best picked, force bool) error {
	if err := automationErr(cfg); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.Store.GetEpisode(ctx, sh.ID, ep.Number)
	if err != nil {
		return err
	}
	if strings.EqualFold(cur.InfoHash, best.rel.InfoHash) && cur.InfoHash != "" && cur.State == store.StateDownloading {
		return nil
	}
	if !force {
		switch cur.State {
		case store.StateIgnored, store.StateDownloading, store.StateUpcoming:
			return nil
		case store.StateDownloaded:
			at := time.Time{}
			if cur.DownloadedAt != nil {
				at = *cur.DownloadedAt
			}
			if !score.ShouldUpgrade(cur.Score, cur.Version, at, best.score, best.parsed, time.Now(), cfg.Upgrade) {
				return nil
			}
		default:
			// A recorded file that is not in the upgrade path is already owned.
			if strings.TrimSpace(cur.FilePath) != "" {
				return nil
			}
		}
		// Any category: Get looks the hash up with no category filter.
		if s.torrentPresent(ctx, best.rel.InfoHash) {
			return nil
		}
	}
	if s.Qbit == nil {
		return fmt.Errorf("qBittorrent is not configured")
	}
	magnet := best.rel.Magnet
	if magnet == "" && best.rel.InfoHash != "" {
		magnet = "magnet:?xt=urn:btih:" + strings.ToLower(best.rel.InfoHash) + "&dn=" + url.QueryEscape(best.rel.Title)
	}
	if magnet == "" {
		return fmt.Errorf("release has no magnet or infohash")
	}
	save := cfg.SaveDir()
	addedHash, err := s.Qbit.Add(ctx, qbit.AddRequest{
		URL:      magnet,
		Category: cfg.QBCategory,
		SavePath: save,
		Tags:     []string{"animarr", fmt.Sprintf("ep-%d", ep.Number), fmt.Sprintf("anilist-%d", sh.AniListID)},
	})
	if err != nil {
		s.history(ctx, "error", "qBittorrent add failed: "+err.Error(), sh.ID, ep.Number)
		return err
	}
	hash := strings.ToLower(best.rel.InfoHash)
	if h := strings.ToLower(strings.TrimSpace(addedHash)); h != "" {
		hash = h
	}
	cur.State = store.StateDownloading
	cur.ReleaseTitle = best.rel.Title
	cur.InfoHash = hash
	cur.Score = best.score
	cur.Version = best.parsed.Version
	if cur.Version < 1 {
		cur.Version = 1
	}
	if err := s.Store.UpdateEpisode(ctx, cur); err != nil {
		return err
	}
	if _, err := s.Store.AddDownload(ctx, store.Download{
		ShowID:   sh.ID,
		Episode:  ep.Number,
		InfoHash: hash,
		Title:    best.rel.Title,
		SavePath: save,
		State:    store.DlDownloading,
		Score:    best.score,
	}); err != nil {
		return err
	}
	s.history(ctx, "info", "grabbed "+best.rel.Title, sh.ID, ep.Number)
	return nil
}

func (s *Service) pollOne(ctx context.Context, cfg config.Config, d store.Download) error {
	tor, err := s.Qbit.Get(ctx, d.InfoHash)
	if errors.Is(err, qbit.ErrNotFound) {
		if time.Since(d.AddedAt) > 2*time.Minute {
			_ = s.Store.UpdateDownload(ctx, d.ID, store.DlError, d.Progress, "torrent missing")
			_ = s.failEpisode(ctx, d, "torrent missing from qBittorrent")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if qbit.Failed(tor) {
		_ = s.Store.UpdateDownload(ctx, d.ID, store.DlError, int(tor.Progress*100), tor.State)
		return s.failEpisode(ctx, d, "qBittorrent "+tor.State)
	}
	prog := int(tor.Progress * 100)
	if prog > 100 {
		prog = 100
	}
	if !qbit.Complete(tor) {
		return s.Store.UpdateDownload(ctx, d.ID, store.DlDownloading, prog, "")
	}
	return s.importCompleted(ctx, cfg, d, tor)
}

func (s *Service) importCompleted(ctx context.Context, cfg config.Config, d store.Download, tor qbit.Torrent) error {
	if err := automationErr(cfg); err != nil {
		return nil
	}
	s.mu.Lock()
	ep, err := s.Store.GetEpisode(ctx, d.ShowID, d.Episode)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if ep.InfoHash != "" && !strings.EqualFold(ep.InfoHash, d.InfoHash) && ep.State == store.StateDownloaded {
		return s.Store.UpdateDownload(ctx, d.ID, store.DlSuperseded, 100, "replaced by another grab")
	}
	files, err := s.Qbit.Files(ctx, d.InfoHash)
	if err != nil {
		return err
	}
	pickedFiles := make([]importer.File, 0, len(files))
	for _, f := range files {
		pickedFiles = append(pickedFiles, importer.File{Name: f.Name, Size: f.Size})
	}
	video, ok := importer.PickVideo(pickedFiles, d.Episode)
	if !ok {
		_ = s.Store.UpdateDownload(ctx, d.ID, store.DlError, 100, "no video file")
		return s.failEpisode(ctx, d, "no video file in torrent")
	}
	save := tor.SavePath
	if save == "" {
		save = d.SavePath
	}
	src := filepath.Join(save, filepath.FromSlash(video.Name))
	sh, err := s.Store.GetShow(ctx, d.ShowID)
	if err != nil {
		return err
	}
	dst, err := s.libraryDest(ctx, cfg, sh, d.Episode, filepath.Ext(video.Name))
	if err != nil {
		_ = s.Store.UpdateDownload(ctx, d.ID, store.DlError, 100, err.Error())
		s.history(ctx, "error", "import failed: "+err.Error(), d.ShowID, d.Episode)
		return s.failEpisode(ctx, d, err.Error())
	}
	method, err := importer.ImportFile(src, dst)
	if err != nil {
		_ = s.Store.UpdateDownload(ctx, d.ID, store.DlError, 100, err.Error())
		s.history(ctx, "error", "import failed: "+err.Error(), d.ShowID, d.Episode)
		return s.failEpisode(ctx, d, err.Error())
	}
	s.mu.Lock()
	ep, err = s.Store.GetEpisode(ctx, d.ShowID, d.Episode)
	if err == nil {
		now := time.Now().UTC()
		ep.State = store.StateDownloaded
		ep.FilePath = dst
		ep.InfoHash = strings.ToLower(d.InfoHash)
		ep.ReleaseTitle = d.Title
		ep.DownloadedAt = &now
		err = s.Store.UpdateEpisode(ctx, ep)
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := s.Store.UpdateDownload(ctx, d.ID, store.DlImported, 100, ""); err != nil {
		return err
	}
	s.history(ctx, "info", "imported ("+method+") "+dst, d.ShowID, d.Episode)
	if cfg.NFOEnabled {
		if err := s.writeNFO(ctx, sh, ep, dst); err != nil {
			s.history(ctx, "warn", "nfo: "+err.Error(), d.ShowID, d.Episode)
			s.log().Warn("nfo", "err", err)
		}
	}
	if s.Jellyfin != nil && s.Jellyfin.Enabled() {
		if err := s.Jellyfin.Refresh(ctx); err != nil {
			s.history(ctx, "warn", "Jellyfin refresh failed: "+err.Error(), d.ShowID, d.Episode)
			s.log().Warn("jellyfin refresh", "err", err)
		}
	}
	return nil
}

func (s *Service) failEpisode(ctx context.Context, d store.Download, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ep, err := s.Store.GetEpisode(ctx, d.ShowID, d.Episode)
	if err != nil {
		return err
	}
	if !strings.EqualFold(ep.InfoHash, d.InfoHash) || ep.State != store.StateDownloading {
		return fmt.Errorf("%s", msg)
	}
	ep.State = store.StateWanted
	if err := s.Store.UpdateEpisode(ctx, ep); err != nil {
		return err
	}
	s.history(ctx, "error", msg, d.ShowID, d.Episode)
	return fmt.Errorf("%s", msg)
}

// torrentPresent reports a torrent qBittorrent already has for this infohash.
// The lookup is not limited to the animarr category.
func (s *Service) torrentPresent(ctx context.Context, hash string) bool {
	hash = strings.ToLower(strings.TrimSpace(hash))
	if s == nil || s.Qbit == nil || hash == "" {
		return false
	}
	tor, err := s.Qbit.Get(ctx, hash)
	if err != nil {
		return false
	}
	return strings.EqualFold(tor.Hash, hash) && tor.Hash != ""
}

func automationErr(cfg config.Config) error {
	if cfg.AutomationEnabled {
		return nil
	}
	return ErrAutomationOff
}

func (s *Service) loadEp(ctx context.Context, showID int64, number int) (store.Show, store.Episode, error) {
	sh, err := s.Store.GetShow(ctx, showID)
	if err != nil {
		return store.Show{}, store.Episode{}, err
	}
	ep, err := s.Store.GetEpisode(ctx, showID, number)
	if err != nil {
		return store.Show{}, store.Episode{}, err
	}
	return sh, ep, nil
}

func (s *Service) history(ctx context.Context, level, msg string, showID int64, episode int) {
	h := store.History{Level: level, Message: msg}
	if showID > 0 {
		h.ShowID = &showID
	}
	if episode > 0 {
		v := episode
		h.Episode = &v
	}
	if err := s.Store.AddHistory(ctx, h); err != nil {
		s.log().Warn("history", "err", err)
	}
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// assignFranchises numbers seasons from the shows already in the database.
// A sequel whose prequel is not stored yet stays its own franchise until that prequel is synced.
func (s *Service) assignFranchises(ctx context.Context) error {
	shows, err := s.Store.ListShows(ctx, "all")
	if err != nil {
		return err
	}
	in := make([]franchise.Show, 0, len(shows))
	for _, sh := range shows {
		in = append(in, franchise.Show{
			AniListID: sh.AniListID, Title: sh.DisplayTitle(), Format: sh.Format,
			StartYear: sh.StartYear, Season: sh.Season, Episodes: sh.Episodes,
			RelationsJSON: sh.RelationsJSON,
		})
	}
	placed := franchise.Place(in)
	for _, sh := range shows {
		p, ok := placed[sh.AniListID]
		if !ok || (sh.FranchiseID == p.FranchiseID && sh.SeasonNumber == p.SeasonNumber) {
			continue
		}
		if err := s.Store.SetFranchise(ctx, sh.ID, p.FranchiseID, p.SeasonNumber); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) libraryDest(ctx context.Context, cfg config.Config, sh store.Show, episode int, ext string) (string, error) {
	shows, err := s.Store.ListShows(ctx, "all")
	if err != nil {
		return "", err
	}
	root := sh
	if sh.FranchiseID > 0 {
		for _, other := range shows {
			if other.AniListID == sh.FranchiseID {
				root = other
				break
			}
		}
	}
	peers := make([]franchise.Row, 0, len(shows))
	for _, other := range shows {
		peers = append(peers, franchiseRow(other))
	}
	folder, year, format, season, special, fileEp := franchise.FileLayout(franchiseRow(sh), franchiseRow(root), episode, peers)
	return importer.DestPath(cfg.LibraryDir, importer.Show{
		Title: folder, Year: year, Format: format, Season: season, Special: special,
	}, fileEp, ext), nil
}

func franchiseRow(sh store.Show) franchise.Row {
	return franchise.Row{
		AniListID: sh.AniListID, Title: sh.DisplayTitle(), Year: sh.StartYear,
		Format: sh.Format, FranchiseID: sh.FranchiseID, SeasonNumber: sh.SeasonNumber,
		Episodes: sh.Episodes, StartYear: sh.StartYear,
	}
}

func (s *Service) writeNFO(ctx context.Context, sh store.Show, ep store.Episode, videoPath string) error {
	shows, err := s.Store.ListShows(ctx, "all")
	if err != nil {
		return err
	}
	for _, other := range shows {
		if other.ID == sh.ID {
			sh = other
			break
		}
	}
	root := sh
	if sh.FranchiseID > 0 {
		for _, other := range shows {
			if other.AniListID == sh.FranchiseID {
				root = other
				break
			}
		}
	}
	meta := nfo.Series{
		Title: root.DisplayTitle(), Original: root.TitleRomaji, Plot: root.Description,
		Year: root.StartYear, Genres: root.Genres, Studio: root.Studio,
		AniListID: root.AniListID, PosterURL: root.CoverImage,
	}
	movie := strings.EqualFold(sh.Format, "MOVIE") && (sh.FranchiseID == 0 || sh.FranchiseID == sh.AniListID)
	if movie {
		meta.Title = sh.DisplayTitle()
		meta.Original = sh.TitleRomaji
		meta.Plot = sh.Description
		meta.Year = sh.StartYear
		meta.Genres = sh.Genres
		meta.Studio = sh.Studio
		meta.AniListID = sh.AniListID
		meta.PosterURL = sh.CoverImage
		dir := filepath.Dir(videoPath)
		if err := nfo.WriteMovie(dir, meta); err != nil {
			return err
		}
		if err := nfo.WriteMovieFile(videoPath, meta); err != nil {
			return err
		}
		return nfo.EnsurePoster(ctx, nil, dir, sh.CoverImage)
	}
	seasonDir := filepath.Dir(videoPath)
	seriesDir := filepath.Dir(seasonDir)
	if err := nfo.WriteSeries(seriesDir, meta); err != nil {
		return err
	}
	sn := sh.SeasonNumber
	if sn <= 0 {
		if franchise.TV(sh.Format) {
			sn = 1
		}
	}
	if err := nfo.WriteSeason(seasonDir, nfo.Season{
		Number: sn, Title: sh.DisplayTitle(), Plot: sh.Description,
		Year: sh.StartYear, AniListID: sh.AniListID,
	}); err != nil {
		return err
	}
	var aired time.Time
	if ep.AirDate != nil {
		aired = *ep.AirDate
	}
	if err := nfo.WriteEpisode(nfo.Episode{
		VideoPath: videoPath, Season: sn, Number: ep.Number,
		Plot: sh.Description, Aired: aired, AniListID: sh.AniListID,
	}); err != nil {
		return err
	}
	if err := nfo.EnsureImage(ctx, nil, filepath.Join(seriesDir, nfo.SeasonPosterName(sn)), sh.CoverImage); err != nil {
		slog.Warn("season poster", "show", sh.DisplayTitle(), "err", err)
	}
	return nfo.EnsurePoster(ctx, nil, seriesDir, root.CoverImage)
}
