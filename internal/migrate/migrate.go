package migrate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"animarr/internal/anilist"
	"animarr/internal/config"
	"animarr/internal/franchise"
	"animarr/internal/importer"
	"animarr/internal/match"
	"animarr/internal/nfo"
	"animarr/internal/pipeline"
	"animarr/internal/store"
)

// Refresher is the Jellyfin call used once after --relayout.
// A nil refresher skips the refresh.
type Refresher interface {
	Enabled() bool
	Refresh(ctx context.Context) error
}

// Runner records Sonarr episode files in animarr.
// It is an explicit command and ignores AUTOMATION_ENABLED.
// Without --relayout the Sonarr path is stored and the file stays where it is.
// With --relayout the file is renamed into the franchise folder on the same filesystem.
// A cross-filesystem rename is skipped. Files are not copied.
type Runner struct {
	Config    config.Config
	Store     *store.Store
	AniList   *anilist.Client
	Pipe      *pipeline.Service
	Sonarr    *Sonarr
	Jellyfin  Refresher
	DryRun    bool
	Relayout  bool
	Overrides string
	Out       io.Writer
}

// Result is the printed report, kept for tests.
type Result struct {
	Lines        []string
	FilesMarked  int
	ShowsWritten int
	FilesMoved   int
}

const relayoutWarning = "warning: moving files makes Jellyfin treat them as new items. Watch history and played state for those items may be lost. Back up Jellyfin's data or accept that before running this."

type movedFile struct {
	file placed
	dest string
}

// Run matches each Sonarr series to an AniList franchise and records files.
// An ambiguous series is reported and not written.
func (r *Runner) Run(ctx context.Context) (Result, error) {
	if r.Sonarr == nil {
		return Result{}, fmt.Errorf("sonarr client is nil")
	}
	if r.Store == nil {
		return Result{}, fmt.Errorf("store is nil")
	}
	out := r.Out
	if out == nil {
		out = os.Stdout
	}
	var res Result
	line := func(format string, args ...any) {
		s := fmt.Sprintf(format, args...)
		res.Lines = append(res.Lines, s)
		fmt.Fprintln(out, s)
	}

	overrides, err := loadOverrides(r.Overrides)
	if err != nil {
		return res, err
	}
	if r.DryRun {
		line("dry-run: no database writes")
	}
	if r.Relayout {
		line(relayoutWarning)
	}
	if !r.DryRun && strings.TrimSpace(r.Config.AniListUsername) != "" && r.Pipe != nil {
		line("syncing AniList @%s before match", r.Config.AniListUsername)
		if err := r.Pipe.SyncAniList(ctx); err != nil {
			return res, err
		}
	}

	series, err := r.Sonarr.SeriesList(ctx)
	if err != nil {
		return res, err
	}
	local, err := r.Store.ListShows(ctx, "all")
	if err != nil {
		return res, err
	}
	threshold := r.Config.MatchThreshold
	if threshold <= 0 {
		threshold = 0.82
	}

	for _, sr := range series {
		eps, err := r.Sonarr.Episodes(ctx, sr.ID)
		if err != nil {
			return res, err
		}
		files, err := r.Sonarr.EpisodeFiles(ctx, sr.ID)
		if err != nil {
			return res, err
		}
		spec, hasOverride := lookupOverride(overrides, sr.Title)
		var plan seriesPlan
		how := ""
		switch {
		case hasOverride:
			plan, err = r.planOverride(ctx, spec)
			how = "override"
		default:
			var media anilist.Media
			media, how, err = r.pick(ctx, sr, local, threshold, line)
			if err != nil {
				return res, err
			}
			if media.ID == 0 {
				continue
			}
			plan, err = r.planChain(ctx, media)
		}
		if err != nil {
			return res, err
		}
		src := sourceFiles(sr, eps, files)
		if plan.Ambiguous != "" {
			line("ambiguous: %q %s", sr.Title, plan.Ambiguous)
			for _, f := range src {
				if f.Season == 0 {
					line("  special: S00E%02d not mapped (%s)", f.Episode, f.Path)
				}
			}
			continue
		}
		anchor := plan.Media[plan.FranchiseID]
		if anchor.ID == 0 && len(plan.Main) > 0 {
			anchor = plan.Media[plan.Main[0].ID]
		}
		line("matched: %q -> AniList %d (%s) via %s", sr.Title, anchor.ID, anchor.DisplayTitle(), how)
		line("  chain: %s", plan.describe())

		entries := plan.Main
		if len(plan.Rules) > 0 {
			entries = plan.allEntries()
		}
		mapped := mapFiles(entries, src, plan.Rules)
		if mapped.Mode == "ambiguous" {
			line("ambiguous: %q %s", sr.Title, mapped.Evidence)
			for _, note := range mapped.Notes {
				line("  %s", note)
			}
			continue
		}
		line("  mapping: %s", mapped.Mode)
		for _, note := range mapped.Notes {
			line("  %s", note)
		}
		if r.DryRun {
			for _, f := range mapped.Files {
				line("  would mark S%02dE%02d downloaded %s", f.SonarrSeason, f.SonarrEpisode, f.Path)
				if r.Relayout {
					line("  %s -> %s", f.Path, destPath(r.Config.LibraryDir, plan, f, extOf(f.Path)))
				}
			}
			continue
		}
		saved, nShows, err := r.savePlan(ctx, plan)
		if err != nil {
			return res, err
		}
		res.ShowsWritten += nShows
		paths, nMarked, err := r.markFiles(ctx, saved, mapped.Files, line)
		if err != nil {
			return res, err
		}
		res.FilesMarked += nMarked
		if r.Relayout {
			moved, err := r.relayout(ctx, plan, saved, paths, line)
			if err != nil {
				return res, err
			}
			res.FilesMoved += moved
		}
	}
	if r.Relayout && !r.DryRun && r.Jellyfin != nil && r.Jellyfin.Enabled() {
		if err := r.Jellyfin.Refresh(ctx); err != nil {
			line("Jellyfin refresh failed: %v", err)
		} else {
			line("Jellyfin library refresh requested")
		}
	}
	line("done: %d shows written, %d episode files recorded", res.ShowsWritten, res.FilesMarked)
	return res, nil
}

func (r *Runner) savePlan(ctx context.Context, plan seriesPlan) (map[int]store.Show, int, error) {
	out := map[int]store.Show{}
	n := 0
	save := func(e chainEntry) error {
		media, ok := plan.Media[e.ID]
		if !ok {
			return fmt.Errorf("anilist %d missing from plan", e.ID)
		}
		sh, err := r.saveShow(ctx, media, plan.FranchiseID, e.SeasonNumber)
		if err != nil {
			return err
		}
		out[e.ID] = sh
		n++
		return nil
	}
	for _, e := range plan.Main {
		if err := save(e); err != nil {
			return nil, n, err
		}
	}
	for _, e := range plan.Extras {
		if err := save(e); err != nil {
			return nil, n, err
		}
	}
	return out, n, nil
}

func (r *Runner) saveShow(ctx context.Context, media anilist.Media, franchiseID, seasonNumber int) (store.Show, error) {
	existing, err := r.Store.GetShowByAniList(ctx, media.ID)
	preserve := err == nil
	if err != nil && err != store.ErrNotFound {
		return store.Show{}, err
	}
	status := "MANUAL"
	monitored := strings.EqualFold(media.Status, "RELEASING") || media.NextEpisode() > 0
	if preserve {
		status = existing.ListStatus
		monitored = existing.Monitored
	}
	var nextAt *time.Time
	if media.NextAiringEpisode != nil && media.NextAiringEpisode.AiringAt > 0 {
		t := time.Unix(media.NextAiringEpisode.AiringAt, 0).UTC()
		nextAt = &t
	}
	saved, err := r.Store.UpsertShow(ctx, store.Show{
		AniListID:         media.ID,
		TitleRomaji:       media.Title.Romaji,
		TitleEnglish:      media.Title.English,
		TitleNative:       media.Title.Native,
		Synonyms:          media.Synonyms,
		Format:            media.Format,
		Status:            media.Status,
		ListStatus:        status,
		Episodes:          media.EpisodeTotal(),
		NextAiringEpisode: media.NextEpisode(),
		NextAiringAt:      nextAt,
		CoverImage:        media.CoverURL(),
		Season:            media.Season,
		SeasonYear:        media.SeasonYearValue(),
		StartYear:         media.StartYear(),
		Monitored:         monitored,
		RelationsJSON:     media.RelationsJSON(),
		Description:       media.Description,
		Genres:            media.Genres,
		Studio:            media.StudioName(),
	}, preserve)
	if err != nil {
		return store.Show{}, err
	}
	if err := r.Store.SetFranchise(ctx, saved.ID, franchiseID, seasonNumber); err != nil {
		return store.Show{}, err
	}
	saved.FranchiseID = franchiseID
	saved.SeasonNumber = seasonNumber
	now := time.Now()
	airs := media.AirTimes()
	for n := 1; n <= media.EpisodeTotal(); n++ {
		var air *time.Time
		if t, ok := airs[n]; ok {
			tt := t
			air = &tt
		}
		if err := r.Store.EnsureEpisode(ctx, saved.ID, n, air, pipeline.DesiredState(n, air, media.NextEpisode(), now)); err != nil {
			return store.Show{}, err
		}
	}
	return saved, nil
}

func (r *Runner) markFiles(ctx context.Context, saved map[int]store.Show, files []placed, line func(string, ...any)) (map[placed]string, int, error) {
	paths := map[placed]string{}
	marked := 0
	for _, f := range files {
		sh, ok := saved[f.AniListID]
		if !ok {
			line("  unmapped: AniList %d is not in the saved chain", f.AniListID)
			continue
		}
		row, err := r.Store.GetEpisode(ctx, sh.ID, f.Episode)
		if err != nil {
			return paths, marked, err
		}
		if row.State == store.StateDownloaded && row.FilePath != "" {
			line("  keep S%02dE%02d already downloaded %s", f.LibrarySeason, f.Episode, row.FilePath)
			paths[f] = row.FilePath
			continue
		}
		if strings.TrimSpace(f.Path) == "" {
			line("  unmapped: S%02dE%02d has no file path", f.SonarrSeason, f.SonarrEpisode)
			continue
		}
		now := time.Now().UTC()
		row.State = store.StateDownloaded
		row.FilePath = f.Path
		row.ReleaseTitle = "sonarr"
		row.DownloadedAt = &now
		if err := r.Store.UpdateEpisode(ctx, row); err != nil {
			return paths, marked, err
		}
		marked++
		paths[f] = f.Path
		line("  marked S%02dE%02d downloaded %s", f.LibrarySeason, f.Episode, f.Path)
	}
	return paths, marked, nil
}

func (r *Runner) relayout(ctx context.Context, plan seriesPlan, saved map[int]store.Show, paths map[placed]string, line func(string, ...any)) (int, error) {
	var okFiles []movedFile
	moved := 0
	for f, src := range paths {
		ext := extOf(src)
		dst := destPath(r.Config.LibraryDir, plan, f, ext)
		line("  %s -> %s", src, dst)
		if strings.TrimSpace(src) == "" {
			line("  skip: S%02dE%02d has no source path", f.SonarrSeason, f.SonarrEpisode)
			continue
		}
		if filepath.Clean(src) == filepath.Clean(dst) {
			okFiles = append(okFiles, movedFile{f, dst})
			continue
		}
		if _, err := os.Stat(dst); err == nil {
			line("  conflict: %s -> %s (destination exists, left in place)", src, dst)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			line("  skip: %s -> %s (%v)", src, dst, err)
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			line("  skip: %s -> %s (%v)", src, dst, err)
			continue
		}
		sh := saved[f.AniListID]
		row, err := r.Store.GetEpisode(ctx, sh.ID, f.Episode)
		if err != nil {
			return moved, err
		}
		row.FilePath = dst
		if err := r.Store.UpdateEpisode(ctx, row); err != nil {
			return moved, err
		}
		moved++
		okFiles = append(okFiles, movedFile{f, dst})
	}
	if r.Config.NFOEnabled {
		if err := r.writeNFO(ctx, plan, saved, okFiles); err != nil {
			line("  nfo: %v", err)
		}
	}
	return moved, nil
}

func (r *Runner) writeNFO(ctx context.Context, plan seriesPlan, saved map[int]store.Show, files []movedFile) error {
	if len(files) == 0 {
		return nil
	}
	root, ok := saved[plan.FranchiseID]
	if !ok {
		for _, sh := range saved {
			root = sh
			break
		}
	}
	seriesMeta := nfo.Series{
		Title: root.DisplayTitle(), Original: root.TitleRomaji, Plot: root.Description,
		Year: root.StartYear, Genres: root.Genres, Studio: root.Studio, AniListID: root.AniListID,
		PosterURL: root.CoverImage,
	}
	if plan.RootYear > 0 {
		seriesMeta.Year = plan.RootYear
	}
	if strings.TrimSpace(plan.RootTitle) != "" {
		seriesMeta.Title = plan.RootTitle
	}
	movie := strings.EqualFold(plan.RootFormat, "MOVIE") && len(plan.Main) == 1 && len(plan.Extras) == 0
	seenSeason := map[string]bool{}
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, f := range files {
		sh := saved[f.file.AniListID]
		dir := filepath.Dir(f.dest)
		if movie {
			note(nfo.WriteMovie(dir, seriesMeta))
			note(nfo.WriteMovieFile(f.dest, seriesMeta))
			note(nfo.EnsurePoster(ctx, nil, dir, seriesMeta.PosterURL))
			continue
		}
		seriesDir := filepath.Dir(dir)
		if !seenSeason[seriesDir] {
			note(nfo.WriteSeries(seriesDir, seriesMeta))
			note(nfo.EnsurePoster(ctx, nil, seriesDir, seriesMeta.PosterURL))
			seenSeason[seriesDir] = true
		}
		if !seenSeason[dir] {
			sn := sh.SeasonNumber
			if sn <= 0 && franchise.TV(sh.Format) {
				sn = 1
			}
			note(nfo.WriteSeason(dir, nfo.Season{
				Number: sn, Title: sh.DisplayTitle(), Plot: sh.Description,
				Year: sh.StartYear, AniListID: sh.AniListID,
			}))
			note(nfo.EnsureImage(ctx, nil, filepath.Join(seriesDir, nfo.SeasonPosterName(sn)), sh.CoverImage))
			seenSeason[dir] = true
		}
		ep, err := r.Store.GetEpisode(ctx, sh.ID, f.file.Episode)
		if err != nil {
			note(err)
			continue
		}
		var aired time.Time
		if ep.AirDate != nil {
			aired = *ep.AirDate
		}
		sn := sh.SeasonNumber
		if sn < 0 {
			sn = 0
		}
		note(nfo.WriteEpisode(nfo.Episode{
			VideoPath: f.dest, Season: sn, Number: f.file.Episode,
			Plot: sh.Description, Aired: aired, AniListID: sh.AniListID,
		}))
	}
	return firstErr
}

func (r *Runner) pick(ctx context.Context, sr Series, local []store.Show, threshold float64, line func(string, ...any)) (anilist.Media, string, error) {
	queries := seriesNames(sr)
	localCands := make([]match.Candidate, 0, len(local))
	localBy := map[int64]store.Show{}
	for _, sh := range local {
		localCands = append(localCands, match.Candidate{Key: sh.ID, Titles: sh.AllTitles()})
		localBy[sh.ID] = sh
	}
	localHit, localScore, localAmb, localOK := choose(queries, localCands, threshold)
	if localAmb {
		line("ambiguous: %q matches more than one synced show", sr.Title)
	}

	var remote []anilist.Media
	if r.AniList != nil && len(queries) > 0 {
		found, err := r.searchMedia(ctx, queries, threshold)
		if err != nil {
			return anilist.Media{}, "", err
		}
		remote = found
	}
	remoteCands := make([]match.Candidate, 0, len(remote))
	remoteBy := map[int64]anilist.Media{}
	for _, m := range remote {
		remoteCands = append(remoteCands, match.Candidate{Key: int64(m.ID), Titles: m.Names()})
		remoteBy[int64(m.ID)] = m
	}
	remoteHit, remoteScore, remoteAmb, remoteOK := choose(queries, remoteCands, threshold)
	if remoteAmb && !localOK {
		line("ambiguous: %q matches more than one AniList result; add an override", sr.Title)
		return anilist.Media{}, "", nil
	}

	useLocal := localOK && (!remoteOK || localScore+0.02 >= remoteScore)
	if useLocal {
		sh := localBy[localHit.Key]
		media, err := r.get(ctx, sh.AniListID)
		if err != nil {
			line("matched local show %q but AniList fetch failed: %v", sh.DisplayTitle(), err)
			return anilist.Media{}, "", err
		}
		return media, "synced list", nil
	}
	if remoteOK {
		return remoteBy[remoteHit.Key], "anilist search", nil
	}
	if !localAmb {
		line("unmatched: %q", sr.Title)
	}
	return anilist.Media{}, "", nil
}

func (r *Runner) get(ctx context.Context, id int) (anilist.Media, error) {
	if r.AniList == nil {
		return anilist.Media{}, fmt.Errorf("anilist client is nil")
	}
	return r.AniList.Get(ctx, id)
}

func choose(queries []string, cands []match.Candidate, threshold float64) (match.Candidate, float64, bool, bool) {
	var best match.Candidate
	var bestScore float64
	found := false
	ambiguous := false
	for _, q := range queries {
		hit, score, ok := match.Best(q, cands, threshold)
		if ok {
			if !found || score > bestScore {
				best, bestScore, found = hit, score, true
			}
			continue
		}
		if ambiguousPair(q, cands, threshold) {
			ambiguous = true
		}
	}
	return best, bestScore, ambiguous && !found, found
}

func ambiguousPair(query string, cands []match.Candidate, threshold float64) bool {
	var scores []float64
	for _, c := range cands {
		s := match.ScoreTitles(query, c.Titles)
		if s >= threshold {
			scores = append(scores, s)
		}
	}
	if len(scores) < 2 {
		return false
	}
	sort.Float64s(scores)
	top := scores[len(scores)-1]
	second := scores[len(scores)-2]
	return top-second < 0.03 && top < 1
}

func seriesNames(sr Series) []string {
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		for _, e := range out {
			if strings.EqualFold(e, s) {
				return
			}
		}
		out = append(out, s)
	}
	add(sr.Title)
	add(sr.SortTitle)
	add(sr.CleanTitle)
	for _, alt := range sr.AlternateTitles {
		add(alt.Title)
	}
	return out
}

// searchMedia queries AniList with every Sonarr title, then with a few
// rewrites. AniList's search treats "Road Trip" and "Roadtrip" as different
// strings, so a full-title miss is retried with the subtitle removed and with
// one space between adjacent words removed. Returned rows are still scored
// with the fuzzy matcher against the original titles.
func (r *Runner) searchMedia(ctx context.Context, queries []string, threshold float64) ([]anilist.Media, error) {
	var remote []anilist.Media
	seenID := map[int]bool{}
	seenQ := map[string]bool{}
	try := func(q string) (bool, error) {
		q = strings.TrimSpace(q)
		if len(q) < 2 {
			return false, nil
		}
		key := strings.ToLower(q)
		if seenQ[key] {
			return false, nil
		}
		seenQ[key] = true
		found, err := r.AniList.Search(ctx, q)
		if err != nil {
			return false, err
		}
		for _, m := range found {
			if m.ID == 0 || seenID[m.ID] {
				continue
			}
			seenID[m.ID] = true
			remote = append(remote, m)
		}
		_, _, _, ok := choose(queries, mediaCandidates(remote), threshold)
		return ok, nil
	}
	for _, q := range queries {
		ok, err := try(q)
		if err != nil || ok {
			return remote, err
		}
	}
	for i, q := range fallbackQueries(queries) {
		if i >= 12 {
			break
		}
		ok, err := try(q)
		if err != nil || ok {
			return remote, err
		}
	}
	return remote, nil
}

func mediaCandidates(remote []anilist.Media) []match.Candidate {
	out := make([]match.Candidate, 0, len(remote))
	for _, m := range remote {
		out = append(out, match.Candidate{Key: int64(m.ID), Titles: m.Names()})
	}
	return out
}

// fallbackQueries rewrites titles after the raw strings missed.
// The clause before a colon comes first. Then one adjacent word pair is
// joined, right to left, rotating across titles so a short alternate is not
// stuck behind every join of a long English title.
func fallbackQueries(queries []string) []string {
	seen := map[string]bool{}
	for _, q := range queries {
		seen[strings.ToLower(strings.TrimSpace(q))] = true
	}
	var mains [][]string
	maxWords := 0
	for _, q := range queries {
		main := q
		if i := strings.Index(q, ":"); i > 0 {
			main = strings.TrimSpace(q[:i])
		}
		words := strings.Fields(main)
		mains = append(mains, words)
		if len(words) > maxWords {
			maxWords = len(words)
		}
	}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if len(s) < 2 {
			return
		}
		key := strings.ToLower(s)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, s)
	}
	for _, q := range queries {
		if i := strings.Index(q, ":"); i > 0 {
			add(q[:i])
		}
	}
	for drop := 1; drop < maxWords; drop++ {
		for _, words := range mains {
			i := len(words) - 1 - drop
			if i < 0 {
				continue
			}
			add(joinWords(words, i))
		}
	}
	return out
}

func joinWords(words []string, i int) string {
	if i < 0 || i+1 >= len(words) {
		return ""
	}
	out := make([]string, 0, len(words)-1)
	out = append(out, words[:i]...)
	out = append(out, words[i]+words[i+1])
	out = append(out, words[i+2:]...)
	return strings.Join(out, " ")
}

func sourceFiles(sr Series, eps []Episode, files []EpisodeFile) []srcFile {
	byID := fileByID(files)
	out := make([]srcFile, 0, len(eps))
	for _, ep := range eps {
		if !ep.HasFile {
			continue
		}
		out = append(out, srcFile{
			Season: ep.SeasonNumber, Episode: ep.EpisodeNumber, Path: resolvePath(sr, ep, byID),
		})
	}
	return out
}

func fileByID(files []EpisodeFile) map[int]EpisodeFile {
	out := make(map[int]EpisodeFile, len(files))
	for _, f := range files {
		out[f.ID] = f
	}
	return out
}

func resolvePath(sr Series, ep Episode, files map[int]EpisodeFile) string {
	f, ok := files[ep.EpisodeFileID]
	if !ok {
		return ""
	}
	if strings.TrimSpace(f.Path) != "" {
		return f.Path
	}
	if f.RelativePath == "" {
		return ""
	}
	if sr.Path == "" {
		return f.RelativePath
	}
	return filepath.Join(sr.Path, f.RelativePath)
}

func joinInts(n []int) string {
	parts := make([]string, len(n))
	for i, v := range n {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, ",")
}

func destPath(library string, plan seriesPlan, f placed, ext string) string {
	ent, ok := plan.entry(f.AniListID)
	title := plan.RootTitle
	if strings.TrimSpace(title) == "" && ok {
		title = ent.Title
	}
	standaloneMovie := ok && strings.EqualFold(ent.Format, "MOVIE") &&
		(ent.ID == plan.FranchiseID || plan.FranchiseID == 0) &&
		!franchise.TV(plan.RootFormat)
	if standaloneMovie {
		year := plan.RootYear
		if ent.ID != 0 {
			if m, ok := plan.Media[ent.ID]; ok && m.StartYear() > 0 {
				year = m.StartYear()
			}
		}
		name := ent.Title
		if strings.TrimSpace(name) == "" {
			name = title
		}
		return importer.DestPath(library, importer.Show{Title: name, Year: year, Format: "MOVIE"}, f.Episode, ext)
	}
	special := f.LibrarySeason <= 0 && (ok && !franchise.TV(ent.Format) || f.LibrarySeason == 0 && !franchise.TV(plan.RootFormat))
	season := f.LibrarySeason
	if special {
		season = 0
	}
	return importer.DestPath(library, importer.Show{
		Title: title, Year: plan.RootYear, Format: "TV", Season: season, Special: special,
	}, f.Episode, ext)
}

func extOf(path string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return ".mkv"
	}
	return ext
}

type overrideSpec struct {
	Single   int
	Rules    []rule
	Explicit bool
}

func loadOverrides(path string) (map[string]overrideSpec, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return map[string]overrideSpec{}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("overrides: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("overrides: %w", err)
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	out := make(map[string]overrideSpec, len(raw))
	for title, msg := range raw {
		msg = bytes.TrimSpace(msg)
		if len(msg) == 0 || string(msg) == "null" {
			return nil, fmt.Errorf("overrides: %q is empty", title)
		}
		if msg[0] == '{' {
			var body struct {
				Entries []struct {
					AniList       int `json:"anilist"`
					SonarrSeason  int `json:"sonarrSeason"`
					EpisodeOffset int `json:"episodeOffset"`
				} `json:"entries"`
			}
			if err := json.Unmarshal(msg, &body); err != nil {
				return nil, fmt.Errorf("overrides: %q: %w", title, err)
			}
			spec := overrideSpec{Explicit: true}
			for _, e := range body.Entries {
				if e.AniList <= 0 {
					return nil, fmt.Errorf("overrides: %q has an entry without anilist", title)
				}
				spec.Rules = append(spec.Rules, rule{
					AniListID: e.AniList, SonarrSeason: e.SonarrSeason, EpisodeOffset: e.EpisodeOffset,
				})
			}
			out[title] = spec
			continue
		}
		var id int
		if err := json.Unmarshal(msg, &id); err != nil || id <= 0 {
			return nil, fmt.Errorf("overrides: %q must be an AniList id or {\"entries\":[...]}", title)
		}
		out[title] = overrideSpec{Single: id}
	}
	return out, nil
}

func lookupOverride(m map[string]overrideSpec, title string) (overrideSpec, bool) {
	if spec, ok := m[title]; ok && spec.usable() {
		return spec, true
	}
	for k, spec := range m {
		if strings.EqualFold(k, title) && spec.usable() {
			return spec, true
		}
	}
	return overrideSpec{}, false
}

func (s overrideSpec) usable() bool {
	return s.Single > 0 || s.Explicit
}
