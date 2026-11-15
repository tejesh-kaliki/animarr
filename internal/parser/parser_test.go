package parser

import "testing"

func TestParseSamples(t *testing.T) {
	tests := []struct {
		in      string
		group   string
		title   string
		season  int
		episode int
		version int
		res     string
		codec   string
		source  string
		dual    bool
		dub     bool
		repack  bool
		batch   bool
	}{
		{
			in:      "[ToonsHub] Title S01E07 1080p BILI WEB-DL AAC2.0 H.265 (Romaji alt title, Multi-Subs)",
			group:   "ToonsHub",
			title:   "Title",
			season:  1,
			episode: 7,
			version: 1,
			res:     "1080p",
			codec:   "hevc",
			source:  "WEB-DL",
		},
		{
			in:      "[Erai-raws] Title - 07 [1080p][Multiple Subtitle]",
			group:   "Erai-raws",
			title:   "Title",
			episode: 7,
			version: 1,
			res:     "1080p",
		},
		{
			in:      "[ASW] Title - 07 [1080p HEVC x265 10Bit][AAC]",
			group:   "ASW",
			title:   "Title",
			episode: 7,
			version: 1,
			res:     "1080p",
			codec:   "hevc",
		},
		{
			in:      "[SubsPlease] Title - 07 (1080p) [A1B2C3D4]",
			group:   "SubsPlease",
			title:   "Title",
			episode: 7,
			version: 1,
			res:     "1080p",
		},
		{
			in:      "Title.S01E07.1080p.WEB-DL.AAC2.0.H.265-GROUP",
			group:   "GROUP",
			title:   "Title",
			season:  1,
			episode: 7,
			version: 1,
			res:     "1080p",
			codec:   "hevc",
			source:  "WEB-DL",
		},
		{
			in:      "[Group] Title - 07v2",
			group:   "Group",
			title:   "Title",
			episode: 7,
			version: 2,
			repack:  true,
		},
		{
			in:      "[SubsPlease] Title - 01-12 (1080p)",
			group:   "SubsPlease",
			title:   "Title",
			episode: 1,
			version: 1,
			res:     "1080p",
			batch:   true,
		},
		{
			in:      "[Group] Title Batch [1080p]",
			group:   "Group",
			title:   "Title",
			version: 1,
			res:     "1080p",
			batch:   true,
		},
		{
			in:      "Title S01 Complete 1080p-GROUP",
			group:   "GROUP",
			title:   "Title S01",
			version: 1,
			res:     "1080p",
			batch:   true,
		},
		{
			in:      "[Erai-raws] Title - 01 ~ 12 [1080p]",
			group:   "Erai-raws",
			title:   "Title",
			episode: 1,
			version: 1,
			res:     "1080p",
			batch:   true,
		},
		{
			in:      "[Group] Show S01E01-E12 [1080p]",
			group:   "Group",
			title:   "Show",
			season:  1,
			episode: 1,
			version: 1,
			res:     "1080p",
			batch:   true,
		},
		{
			in:      "[Group] Title - 07 [1080p][Dual Audio][HEVC]",
			group:   "Group",
			title:   "Title",
			episode: 7,
			version: 1,
			res:     "1080p",
			codec:   "hevc",
			dual:    true,
		},
		{
			in:      "[Group] Title - 07 [1080p][English Dub]",
			group:   "Group",
			title:   "Title",
			episode: 7,
			version: 1,
			res:     "1080p",
			dub:     true,
		},
		{
			in:      "[Group] Title - 07 [1080p][Dual-Audio][Dub]",
			group:   "Group",
			title:   "Title",
			episode: 7,
			version: 1,
			res:     "1080p",
			dual:    true,
			dub:     false,
		},
		{
			in:      "[Exiled-Destiny] Spirited Away (Movie) [Dual Audio]",
			group:   "Exiled-Destiny",
			title:   "Spirited Away",
			version: 1,
			dual:    true,
		},
		{
			in:      "Spirited.Away.2001.1080p.BluRay.x264-GROUP",
			group:   "GROUP",
			title:   "Spirited Away",
			version: 1,
			res:     "1080p",
			codec:   "avc",
			source:  "BluRay",
		},
		{
			in:      "[Group] Title - 07 REPACK [720p]",
			group:   "Group",
			title:   "Title",
			episode: 7,
			version: 1,
			res:     "720p",
			repack:  true,
		},
		{
			in:      "[ASW] Title - 07 [1080p][AAC].mkv",
			group:   "ASW",
			title:   "Title",
			episode: 7,
			version: 1,
			res:     "1080p",
		},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := Parse(tt.in)
			if got.Group != tt.group || got.Title != tt.title || got.Season != tt.season || got.Episode != tt.episode || got.Version != tt.version {
				t.Fatalf("identity: group=%q title=%q season=%d ep=%d v=%d", got.Group, got.Title, got.Season, got.Episode, got.Version)
			}
			if got.Resolution != tt.res || got.Codec != tt.codec || got.Source != tt.source {
				t.Fatalf("quality: res=%q codec=%q source=%q", got.Resolution, got.Codec, got.Source)
			}
			if got.DualAudio != tt.dual || got.Dub != tt.dub || got.Repack != tt.repack || got.Batch != tt.batch {
				t.Fatalf("flags: dual=%v dub=%v repack=%v batch=%v", got.DualAudio, got.Dub, got.Repack, got.Batch)
			}
		})
	}
}

func TestHashIsNotEpisode(t *testing.T) {
	got := Parse("[SubsPlease] Title - 07 (1080p) [A1B2C3D4]")
	if got.Episode != 7 {
		t.Fatalf("episode %d", got.Episode)
	}
	if got.Batch {
		t.Fatal("hash looked like a batch")
	}
}
