package pipeline

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"animarr/internal/indexer"
	"animarr/internal/parser"
	"animarr/internal/store"
)

// seedSplitCour stores a 13-episode first season and an 11-episode 2nd cour,
// the way AniList splits Chitose Is in the Ramune Bottle.
func seedSplitCour(t *testing.T, st *store.Store) (s1, s2 store.Show) {
	t.Helper()
	ctx := context.Background()
	var err error
	s1, err = st.UpsertShow(ctx, store.Show{
		AniListID: 100, TitleRomaji: "Chitose-kun wa Ramune Bin no Naka", TitleEnglish: "Chitose Is in the Ramune Bottle",
		Format: "TV", Status: "FINISHED", Episodes: 13, Monitored: false,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	s2, err = st.UpsertShow(ctx, store.Show{
		AniListID: 200, TitleRomaji: "Chitose-kun wa Ramune Bin no Naka Part 2", TitleEnglish: "Chitose Is in the Ramune Bottle 2nd Cour",
		Format: "TV", Status: "RELEASING", Episodes: 11, Monitored: true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetFranchise(ctx, s1.ID, 100, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFranchise(ctx, s2.ID, 100, 2); err != nil {
		t.Fatal(err)
	}
	s1, _ = st.GetShow(ctx, s1.ID)
	s2, _ = st.GetShow(ctx, s2.ID)
	aired := time.Now().Add(-24 * time.Hour)
	if err := st.EnsureEpisode(ctx, s2.ID, 6, &aired, store.StateWanted); err != nil {
		t.Fatal(err)
	}
	return s1, s2
}

func TestNumberingOffset(t *testing.T) {
	st := openStore(t)
	s1, s2 := seedSplitCour(t, st)
	all := []store.Show{s1, s2}
	nb := numberingOf(s2, all)
	if nb.offset != 13 {
		t.Fatalf("offset %d", nb.offset)
	}
	if numberingOf(s1, all).offset != 0 {
		t.Fatal("first season has no offset")
	}
	cases := []struct {
		n    int
		want int
		abs  bool
		ok   bool
	}{{6, 6, false, true}, {19, 6, true, true}, {14, 1, true, true}, {12, 0, false, false}, {25, 0, false, false}}
	for _, c := range cases {
		got, abs, ok := nb.local(s2, c.n)
		if got != c.want || abs != c.abs || ok != c.ok {
			t.Errorf("local(%d) = %d,%v,%v", c.n, got, abs, ok)
		}
	}
	// An unknown earlier season length makes the offset untrustworthy.
	s1.Episodes = 0
	if numberingOf(s2, []store.Show{s1, s2}).offset != 0 {
		t.Fatal("unknown prequel length must disable the offset")
	}
}

func TestRSSAbsoluteAndPartNumbering(t *testing.T) {
	for _, tc := range []struct{ name, title string }{
		{"absolute", "[SubsPlease] Chitose-kun wa Ramune Bin no Naka - 19 (1080p) [7C786D13].mkv"},
		{"absolute sxe", "[ToonsHub] Chitose Is in the Ramune Bottle S01E19 1080p CR WEB-DL AAC2.0 H.264 (Multi-Subs)"},
		{"part", "[Erai-raws] Chitose-kun wa Ramune Bin no Naka Part 2 - 06 [1080p CR WEBRip HEVC AAC][MultiSub][EB90CEBA]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t)
			_, s2 := seedSplitCour(t, st)
			const hash = "cccccccccccccccccccccccccccccccccccccccc"
			qb := &fakeQbit{}
			svc := &Service{
				Config: testCfg(t.TempDir()), Store: st, Qbit: qb,
				Indexer: &fakeIndex{recent: []indexer.Release{{Title: tc.title, InfoHash: hash, Magnet: "magnet:?xt=urn:btih:" + hash, Seeders: 50}}},
				Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			if err := svc.PollRSS(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(qb.added) != 1 {
				t.Fatalf("grabs: %d", len(qb.added))
			}
			ep, err := st.GetEpisode(context.Background(), s2.ID, 6)
			if err != nil || ep.State != store.StateDownloading {
				t.Fatalf("episode: %+v %v", ep, err)
			}
		})
	}
}

func TestRSSFirstSeasonReleaseNotGrabbedForSequel(t *testing.T) {
	st := openStore(t)
	seedSplitCour(t, st)
	qb := &fakeQbit{}
	svc := &Service{
		Config: testCfg(t.TempDir()), Store: st, Qbit: qb,
		Indexer: &fakeIndex{recent: []indexer.Release{{Title: "[SubsPlease] Chitose-kun wa Ramune Bin no Naka - 06 (1080p) [AAAA1111].mkv", InfoHash: strings.Repeat("d", 40), Seeders: 50}}},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := svc.PollRSS(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(qb.added) != 0 {
		t.Fatalf("season 1 episode 6 must not satisfy season 2 episode 6: %+v", qb.added)
	}
}

func TestSearchFindsAbsoluteRelease(t *testing.T) {
	st := openStore(t)
	_, s2 := seedSplitCour(t, st)
	qb := &fakeQbit{}
	svc := &Service{
		Config: testCfg(t.TempDir()), Store: st, Qbit: qb,
		Indexer: &fakeIndex{search: []indexer.Release{{Title: "[SubsPlease] Chitose-kun wa Ramune Bin no Naka - 19 (1080p) [7C786D13].mkv", InfoHash: strings.Repeat("e", 40), Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("e", 40), Seeders: 50}}},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := svc.SearchEpisode(context.Background(), s2.ID, 6); err != nil {
		t.Fatal(err)
	}
	if len(qb.added) != 1 {
		t.Fatalf("grabs: %d", len(qb.added))
	}
	choices, err := svc.ListChoices(context.Background(), s2.ID, 6)
	if err != nil || len(choices) != 1 || choices[0].Rejected {
		t.Fatalf("choices: %+v %v", choices, err)
	}
}

func TestTitleOKDropsYearAndSeasonWords(t *testing.T) {
	s3 := store.Show{ID: 5, TitleRomaji: "Ranma 1/2 (2024) 3rd Season", TitleEnglish: "Ranma1/2 (2024) Season 3", SeasonNumber: 3}
	s1 := store.Show{ID: 156, TitleRomaji: "Ranma 1/2 (2024)", TitleEnglish: "Ranma1/2 (2024)", SeasonNumber: 1}
	p := parser.Parse("[ToonsHub] Ranma 1-2 2024 S03E08 1080p NF WEB-DL DUAL AAC2.0 H.264 (Dual-Audio, Multi-Subs)")
	if !(numbering{}).titleOK(p, s3, 0.8) {
		t.Fatalf("S03 release should match season 3 (title %q)", p.Title)
	}
	if (numbering{}).titleOK(p, s1, 0.8) {
		t.Fatal("S03 release must not match season 1")
	}
}
