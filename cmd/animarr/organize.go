package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"animarr/internal/config"
	"animarr/internal/franchise"
	"animarr/internal/importer"
	"animarr/internal/nfo"
	"animarr/internal/store"
)

// organizeCmd moves standalone movies into their own folders and backfills
// movie/season/episode NFOs and season posters for the existing library.
func organizeCmd(cfg config.Config, origin map[string]config.Source, args []string) error {
	fs := flag.NewFlagSet("organize", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dry := fs.Bool("dry-run", false, "print the plan without touching files or the database")
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
		return err
	}
	return organize(context.Background(), cur.LibraryDir, st, *dry)
}

func organize(ctx context.Context, library string, st *store.Store, dry bool) error {
	shows, err := st.ListShows(ctx, "all")
	if err != nil {
		return err
	}
	byAni := map[int]store.Show{}
	for _, sh := range shows {
		byAni[sh.AniListID] = sh
	}
	act := func(format string, a ...any) { fmt.Printf(format+"\n", a...) }
	var errs int
	fail := func(what string, err error) {
		if err != nil {
			errs++
			act("ERROR %s: %v", what, err)
		}
	}
	touched := map[string]bool{}
	for _, sh := range shows {
		eps, err := st.ListEpisodes(ctx, sh.ID)
		if err != nil {
			return err
		}
		movie := strings.EqualFold(sh.Format, "MOVIE") && (sh.FranchiseID == 0 || sh.FranchiseID == sh.AniListID)
		root := sh
		if r, ok := byAni[sh.FranchiseID]; ok && sh.FranchiseID > 0 {
			root = r
		}
		for _, ep := range eps {
			if ep.FilePath == "" {
				continue
			}
			cur := ep.FilePath
			if _, err := os.Stat(cur); err != nil {
				act("MISSING %s", cur)
				continue
			}
			if movie {
				dest := importer.DestPath(library, importer.Show{Title: sh.DisplayTitle(), Year: sh.StartYear, Format: "MOVIE"}, 1, filepath.Ext(cur))
				if dest != cur {
					act("MOVE %s\n  -> %s", cur, dest)
					if !dry {
						if _, err := os.Stat(dest); err == nil {
							fail("move", fmt.Errorf("destination exists: %s", dest))
							continue
						}
						if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
							fail("mkdir", err)
							continue
						}
						if err := os.Rename(cur, dest); err != nil {
							fail("rename", err)
							continue
						}
						ep.FilePath = dest
						fail("db", st.UpdateEpisode(ctx, ep))
						touched[filepath.Dir(cur)] = true
					}
					cur = dest
				}
				meta := nfo.Series{
					Title: sh.DisplayTitle(), Original: sh.TitleRomaji, Plot: sh.Description,
					Year: sh.StartYear, Genres: sh.Genres, Studio: sh.Studio,
					AniListID: sh.AniListID, PosterURL: sh.CoverImage,
				}
				act("NFO+POSTER %s", filepath.Dir(cur))
				if !dry {
					fail("movie.nfo", nfo.WriteMovie(filepath.Dir(cur), meta))
					fail("movie nfo", nfo.WriteMovieFile(cur, meta))
					fail("poster", nfo.EnsurePoster(ctx, nil, filepath.Dir(cur), sh.CoverImage))
				}
				continue
			}
			sn := sh.SeasonNumber
			if sn <= 0 && franchise.TV(sh.Format) {
				sn = 1
			}
			if sn < 0 {
				sn = 0
			}
			seasonDir := filepath.Dir(cur)
			seriesDir := filepath.Dir(seasonDir)
			posterPath := filepath.Join(seriesDir, nfo.SeasonPosterName(sn))
			if !touched[posterPath] {
				touched[posterPath] = true
				act("SEASON POSTER %s", posterPath)
				if !dry {
					fail("season poster", nfo.EnsureImage(ctx, nil, posterPath, sh.CoverImage))
				}
			}
			if sn == 0 {
				act("SPECIAL NFO %s", cur)
				if !dry {
					fail("season.nfo", nfo.WriteSeason(seasonDir, nfo.Season{
						Number: 0, Title: "Specials", Plot: sh.Description, Year: sh.StartYear, AniListID: sh.AniListID,
					}))
					var aired = ep.AirDate
					e := nfo.Episode{VideoPath: cur, Season: 0, Number: ep.Number, Plot: sh.Description, AniListID: sh.AniListID}
					if aired != nil {
						e.Aired = *aired
					}
					fail("episode nfo", nfo.WriteEpisode(e))
					fail("series poster", nfo.EnsurePoster(ctx, nil, seriesDir, root.CoverImage))
				}
			}
		}
	}
	if !dry {
		for dir := range touched {
			if !strings.HasSuffix(dir, ".jpg") {
				removeIfEmpty(dir)
			}
		}
	}
	if errs > 0 {
		return fmt.Errorf("%d error(s)", errs)
	}
	return nil
}

// removeIfEmpty deletes dir when only metadata files remain, then its parent if empty.
func removeIfEmpty(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if e.IsDir() || !(strings.HasSuffix(n, ".nfo") || strings.HasSuffix(n, ".jpg")) {
			return
		}
	}
	for _, e := range entries {
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
	_ = os.Remove(dir)
}
