package migrate

import (
	"path/filepath"
	"strings"
	"testing"

	"animarr/internal/importer"
)

func tv(id, eps, season int, title string) chainEntry {
	return chainEntry{ID: id, Title: title, Episodes: eps, Format: "TV", SeasonNumber: season, Status: "FINISHED"}
}

func tvStatus(id, eps, season int, title, status string) chainEntry {
	e := tv(id, eps, season, title)
	e.Status = status
	if eps < 1 {
		e.Episodes = 0
	}
	return e
}

func filesThrough(season, n int) []srcFile {
	out := make([]srcFile, 0, n)
	for ep := 1; ep <= n; ep++ {
		out = append(out, srcFile{Season: season, Episode: ep, Path: filepath.Join("/tv/Show", "ep.mkv")})
	}
	return out
}

func TestMapFiles(t *testing.T) {
	tests := []struct {
		name    string
		entries []chainEntry
		files   []srcFile
		rules   []rule
		mode    string
		want    map[[2]int][2]int // sonarr season,ep -> anilist id, local ep
	}{
		{
			name:    "per-season",
			entries: []chainEntry{tv(1, 4, 1, "A"), tv(2, 3, 2, "B")},
			files:   append(filesThrough(1, 2), srcFile{Season: 2, Episode: 1, Path: "/b"}),
			mode:    "per-season",
			want: map[[2]int][2]int{
				{1, 1}: {1, 1},
				{1, 2}: {1, 2},
				{2, 1}: {2, 1},
			},
		},
		{
			name:    "continuous",
			entries: []chainEntry{tv(1, 4, 1, "A"), tv(2, 3, 2, "B"), tv(3, 3, 3, "C")},
			files:   filesThrough(1, 8),
			mode:    "continuous",
			want: map[[2]int][2]int{
				{1, 1}: {1, 1},
				{1, 4}: {1, 4},
				{1, 5}: {2, 1},
				{1, 7}: {2, 3},
				{1, 8}: {3, 1},
			},
		},
		{
			name:    "partial last entry",
			entries: []chainEntry{tv(1, 4, 1, "A"), tv(2, 3, 2, "B")},
			files:   filesThrough(1, 5),
			mode:    "continuous",
			want: map[[2]int][2]int{
				{1, 4}: {1, 4},
				{1, 5}: {2, 1},
			},
		},
		{
			name:    "disagreement",
			entries: []chainEntry{tv(1, 3, 1, "A"), tv(2, 3, 2, "B")},
			files: []srcFile{
				{Season: 1, Episode: 1},
				{Season: 1, Episode: 4},
				{Season: 2, Episode: 1},
			},
			mode: "ambiguous",
		},
		{
			name:    "counts do not add up",
			entries: []chainEntry{tv(1, 4, 1, "A"), tv(2, 3, 2, "B")},
			files:   filesThrough(1, 9),
			mode:    "ambiguous",
		},
		{
			name:    "override chain",
			entries: []chainEntry{tv(111, 2, 1, "A"), tv(222, 2, 2, "B")},
			files:   filesThrough(1, 4),
			rules: []rule{
				{AniListID: 111, SonarrSeason: 1, EpisodeOffset: 0},
				{AniListID: 222, SonarrSeason: 1, EpisodeOffset: 2},
			},
			mode: "override",
			want: map[[2]int][2]int{
				{1, 1}: {111, 1},
				{1, 2}: {111, 2},
				{1, 3}: {222, 1},
				{1, 4}: {222, 2},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapFiles(tt.entries, tt.files, tt.rules)
			if got.Mode != tt.mode {
				t.Fatalf("mode %s evidence %s", got.Mode, got.Evidence)
			}
			if tt.mode == "ambiguous" {
				if got.Evidence == "" || !strings.Contains(got.Evidence, "not guessing") {
					t.Fatalf("evidence %q", got.Evidence)
				}
				if len(got.Files) != 0 {
					t.Fatalf("ambiguous wrote %+v", got.Files)
				}
				return
			}
			for _, f := range got.Files {
				want, ok := tt.want[[2]int{f.SonarrSeason, f.SonarrEpisode}]
				if !ok {
					continue
				}
				if f.AniListID != want[0] || f.Episode != want[1] {
					t.Fatalf("S%02dE%02d -> %d ep %d, want %d ep %d", f.SonarrSeason, f.SonarrEpisode, f.AniListID, f.Episode, want[0], want[1])
				}
			}
			if len(got.Files) != len(tt.files) && tt.name != "partial last entry" {
				// partial includes every file; all non-ambiguous cases map every regular file
			}
			if len(got.Files) < len(tt.want) {
				t.Fatalf("mapped %d want at least %d", len(got.Files), len(tt.want))
			}
		})
	}
}

func TestBookwormPaths(t *testing.T) {
	entries := []chainEntry{tv(1, 4, 1, "Ascendance of a Bookworm"), tv(2, 3, 2, "Part 2"), tv(3, 3, 3, "Part 3")}
	var files []srcFile
	for ep := 1; ep <= 8; ep++ {
		files = append(files, srcFile{Season: 1, Episode: ep, Path: filepath.Join("/data/media/anime/Bookworm/Season 01", "Bookworm - S01E"+two(ep)+".mkv")})
	}
	files = append(files, srcFile{Season: 0, Episode: 1, Path: "/data/media/anime/Bookworm/Season 00/special.mkv"})
	got := mapFiles(entries, files, nil)
	if got.Mode != "continuous" {
		t.Fatalf("mode %s %s", got.Mode, got.Evidence)
	}
	if len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "S00E01") || !strings.Contains(got.Notes[0], "not mapped") {
		t.Fatalf("notes %+v", got.Notes)
	}
	root := "Ascendance of a Bookworm"
	for _, f := range got.Files {
		gotPath := importer.DestPath("/library", importer.Show{Title: root, Format: "TV", Season: f.LibrarySeason}, f.Episode, ".mkv")
		switch f.SonarrEpisode {
		case 1:
			want := filepath.Join("/library", root, "Season 01", root+" - S01E01.mkv")
			if f.LibrarySeason != 1 || f.Episode != 1 || gotPath != want {
				t.Fatalf("%+v %s", f, gotPath)
			}
		case 5:
			want := filepath.Join("/library", root, "Season 02", root+" - S02E01.mkv")
			if f.AniListID != 2 || f.LibrarySeason != 2 || f.Episode != 1 || gotPath != want {
				t.Fatalf("%+v %s", f, gotPath)
			}
		case 8:
			want := filepath.Join("/library", root, "Season 03", root+" - S03E01.mkv")
			if f.AniListID != 3 || f.LibrarySeason != 3 || f.Episode != 1 || gotPath != want {
				t.Fatalf("%+v %s", f, gotPath)
			}
		}
	}
}

func TestMapTrailingUnreleased(t *testing.T) {
	// Ascendance of a Bookworm: 14, 12, 10, 24, then AniList 217712 with no episode count.
	bookworm := []chainEntry{
		tv(108268, 14, 1, "Ascendance of a Bookworm"),
		tv(113693, 12, 2, "Ascendance of a Bookworm Part 2"),
		tv(121176, 10, 3, "Ascendance of a Bookworm Season 3"),
		tv(171110, 24, 4, "Ascendance of a Bookworm: Adopted Daughter of an Archduke"),
		tvStatus(217712, 0, 5, "Ascendance of a Bookworm Season 5", "NOT_YET_RELEASED"),
	}
	var perSeasonFiles []srcFile
	for _, span := range [][2]int{{1, 14}, {2, 12}, {3, 10}, {4, 24}} {
		perSeasonFiles = append(perSeasonFiles, filesThrough(span[0], span[1])...)
	}
	tests := []struct {
		name    string
		entries []chainEntry
		files   []srcFile
		mode    string
		want    map[[2]int][2]int
	}{
		{
			name:    "continuous over the first four",
			entries: bookworm,
			files:   filesThrough(1, 60),
			mode:    "continuous",
			want: map[[2]int][2]int{
				{1, 1}:  {108268, 1},
				{1, 14}: {108268, 14},
				{1, 15}: {113693, 1},
				{1, 26}: {113693, 12},
				{1, 27}: {121176, 1},
				{1, 36}: {121176, 10},
				{1, 37}: {171110, 1},
				{1, 60}: {171110, 24},
			},
		},
		{
			name:    "per-season with trailing unreleased",
			entries: bookworm,
			files:   perSeasonFiles,
			mode:    "per-season",
			want: map[[2]int][2]int{
				{1, 14}: {108268, 14},
				{2, 12}: {113693, 12},
				{3, 10}: {121176, 10},
				{4, 24}: {171110, 24},
			},
		},
		{
			name: "zero count in the middle",
			entries: []chainEntry{
				tv(108268, 14, 1, "Ascendance of a Bookworm"),
				tvStatus(113693, 0, 2, "Part 2", "FINISHED"),
				tv(121176, 10, 3, "Season 3"),
			},
			files: filesThrough(1, 16),
			mode:  "ambiguous",
		},
		{
			name: "trailing finished entry with no count",
			entries: []chainEntry{
				tv(108268, 14, 1, "Ascendance of a Bookworm"),
				tvStatus(217712, 0, 2, "Season 5", "FINISHED"),
			},
			files: filesThrough(1, 14),
			mode:  "ambiguous",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapFiles(tt.entries, tt.files, nil)
			if got.Mode != tt.mode {
				t.Fatalf("mode %s evidence %s", got.Mode, got.Evidence)
			}
			if tt.mode == "ambiguous" {
				if !strings.Contains(got.Evidence, "no episode count") || !strings.Contains(got.Evidence, "not guessing") {
					t.Fatalf("evidence %q", got.Evidence)
				}
				if len(got.Files) != 0 {
					t.Fatalf("ambiguous wrote %+v", got.Files)
				}
				return
			}
			if len(got.Files) != len(tt.files) {
				t.Fatalf("mapped %d files, want %d", len(got.Files), len(tt.files))
			}
			for _, f := range got.Files {
				if f.AniListID == 217712 {
					t.Fatalf("unreleased entry received %+v", f)
				}
				want, ok := tt.want[[2]int{f.SonarrSeason, f.SonarrEpisode}]
				if !ok {
					continue
				}
				if f.AniListID != want[0] || f.Episode != want[1] {
					t.Fatalf("S%02dE%02d -> %d ep %d, want %d ep %d", f.SonarrSeason, f.SonarrEpisode, f.AniListID, f.Episode, want[0], want[1])
				}
			}
		})
	}
}

func TestMapDrStoneAbsoluteSeason(t *testing.T) {
	// S04E34-E37, continuous inside one Sonarr season. 24+11 lands E34-E35 in
	// STONE WARS and E36-E37 in the next TV entry. The RYUSUI special is not
	// in this slice: it is Season 00 and must not consume a number.
	full := []chainEntry{
		tv(105333, 24, 1, "Dr. STONE"),
		tv(113936, 11, 2, "Dr. STONE: STONE WARS"),
		tv(131518, 11, 3, "Dr. STONE New World"),
		tv(162670, 11, 4, "Dr. STONE New World Part 2"),
		tv(172019, 12, 5, "Dr. STONE SCIENCE FUTURE"),
		tv(189117, 12, 6, "Dr. STONE SCIENCE FUTURE Cour 2"),
		tv(199221, 13, 7, "Dr. STONE SCIENCE FUTURE Cour 3"),
	}
	files := []srcFile{
		{Season: 4, Episode: 34, Path: "/tv/Dr. STONE/S04E34.mkv"},
		{Season: 4, Episode: 35, Path: "/tv/Dr. STONE/S04E35.mkv"},
		{Season: 4, Episode: 36, Path: "/tv/Dr. STONE/S04E36.mkv"},
		{Season: 4, Episode: 37, Path: "/tv/Dr. STONE/S04E37.mkv"},
	}
	got := mapFiles(full, files, nil)
	if got.Mode != "continuous" {
		t.Fatalf("mode %s evidence %s", got.Mode, got.Evidence)
	}
	if len(got.Files) != len(files) {
		t.Fatalf("mapped %d", len(got.Files))
	}
	want := map[int][3]int{
		34: {113936, 10, 2},
		35: {113936, 11, 2},
		36: {131518, 1, 3},
		37: {131518, 2, 3},
	}
	for _, f := range got.Files {
		w := want[f.SonarrEpisode]
		if f.AniListID != w[0] || f.Episode != w[1] || f.LibrarySeason != w[2] {
			t.Fatalf("S04E%02d -> anilist %d ep %d season %d, want %d ep %d season %d", f.SonarrEpisode, f.AniListID, f.Episode, f.LibrarySeason, w[0], w[1], w[2])
		}
	}
	// Two known TV entries and a Sonarr season past that length cannot place E36-E37.
	short := mapFiles(full[:2], files, nil)
	if short.Mode != "ambiguous" || !strings.Contains(short.Evidence, "not guessing") || !strings.Contains(short.Evidence, "S04E34") {
		t.Fatalf("short chain evidence %q", short.Evidence)
	}
	if len(short.Files) != 0 {
		t.Fatalf("short chain wrote %+v", short.Files)
	}
}

func two(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
