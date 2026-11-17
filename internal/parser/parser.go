// Package parser splits anime release titles into group, episode, and quality.
// Season packs and batch ranges are flagged and left for the caller to skip.
package parser

import (
	"regexp"
	"strconv"
	"strings"
)

// Parsed is one release title.
type Parsed struct {
	Raw        string
	Group      string
	Title      string
	Season     int
	Episode    int
	EpisodeEnd int // last episode of a batch range; 0 when the title names one episode or no range
	Version    int
	Resolution string
	Codec      string // "hevc", "avc", or ""
	Source     string
	DualAudio  bool
	Dub        bool
	Repack     bool
	Batch      bool
}

var (
	reExt          = regexp.MustCompile(`(?i)\.(?:mkv|mp4|avi|m4v)$`)
	reBatchWord    = regexp.MustCompile(`(?i)\b(?:batch|complete)\b`)
	reRange        = regexp.MustCompile(`(?i)(?:\bS\d{1,2}E\d{1,4}\s*[-~～]\s*E?\d{1,4}\b|\bE\d{1,4}\s*[-~～]\s*E\d{1,4}\b|(?:^|[\s\[\]\(\)._])\d{1,4}\s*[-~～]\s*\d{1,4}(?:$|[\s\[\]\(\)._])|\bseasons?\s*\d+\s*[-~～]\s*\d+\b)`)
	reSeasonWord   = regexp.MustCompile(`(?i)season`)
	reEpSpan       = regexp.MustCompile(`(?i)S(\d{1,2})E(\d{1,4})\s*[-~～]\s*E?(\d{1,4})|E(\d{1,4})\s*[-~～]\s*E(\d{1,4})|(?:^|[\s\[\]\(\)._])(\d{1,4})\s*[-~～]\s*(\d{1,4})(?:$|[\s\[\]\(\)._])`)
	reBracketGroup = regexp.MustCompile(`^\[([^\]]+)\]\s*`)
	reSceneGroup   = regexp.MustCompile(`-([A-Za-z][A-Za-z0-9]*)$`)
	reAudio        = regexp.MustCompile(`(?i)\b(?:AAC|FLAC|OPUS|AC3|EAC3|DDP|DD)?\d\.\d\b|\b(?:5\.1|2\.0|7\.1)\b`)
	reHash         = regexp.MustCompile(`(?i)\[[0-9A-F]{8}\]`)
	reSxE          = regexp.MustCompile(`(?i)\bS(\d{1,2})\s*E(\d{1,4})(?:v(\d+))?\b`)
	reDashEp       = regexp.MustCompile(`(?i)\s-\s(\d{1,4})(?:v(\d+))?\b`)
	reRes          = regexp.MustCompile(`(?i)\b(2160|1080|720|480|360)p\b|\b4K\b`)
	reHEVC         = regexp.MustCompile(`(?i)\b(?:H[\.\s]?265|HEVC|x265)\b`)
	reAVC          = regexp.MustCompile(`(?i)\b(?:H[\.\s]?264|AVC|x264)\b`)
	reSource       = regexp.MustCompile(`(?i)\b(WEB[\-\s]?DL|WEBRip|Blu-?Ray|BDRip|BD\s*Remux|Remux|HDTV)\b`)
	reDual         = regexp.MustCompile(`(?i)\b(?:dual[\-\s]?audio|multi[\-\s]?audio)\b`)
	reDub          = regexp.MustCompile(`(?i)(?:\[|\(|\b)(?:(?:english|eng)[\-\s]?dub|dubbed|dub)(?:\]|\)|\b)`)
	reRepack       = regexp.MustCompile(`(?i)\b(?:repack|proper)\b`)
	reBrackets     = regexp.MustCompile(`\[[^\]]*\]`)
	reParens       = regexp.MustCompile(`\([^)]*\)`)
	reTech         = regexp.MustCompile(`(?i)\b(?:2160p|1080p|720p|480p|360p|4k|x265|x264|h[\.\s]*265|h[\.\s]*264|hevc|avc|10\s*bit|8\s*bit|hdr10|hdr|web-?dl|webrip|bluray|bdrip|bdremux|remux|hdtv|aac\d*|flac|opus|ac3|eac3|ddp|multiple|subtitle|multi-?subs?|multisubs|bili|amzn|dsnp|10bit|8bit)\b`)
	reYear         = regexp.MustCompile(`\b(?:19|20)\d{2}\b`)
	reTrailGroup   = regexp.MustCompile(`(?i)\s*-\s*([A-Za-z][A-Za-z0-9]*)$`)
	reRepackWord   = regexp.MustCompile(`(?i)\b(?:repack|proper)\b`)
	reRangeFirst   = regexp.MustCompile(`(?i)S(\d{1,2})E(\d{1,4})|(\d{1,4})`)
	reSpace        = regexp.MustCompile(`\s+`)
	// rePartNum finds "Part 2 - 06": the 2 names a cour, not the start of a range.
	rePartNum = regexp.MustCompile(`(?i)\b((?:part|cour|vol(?:ume)?|season)\s*)(\d{1,2})(\s+-\s+\d)`)
)

// maskPartNumber hides the number in "Part 2 - 06" so range detection does not read
// "2 - 06" as episodes 2 to 6. The result has the same length as s.
func maskPartNumber(s string) string {
	return rePartNum.ReplaceAllStringFunc(s, func(m string) string {
		sm := rePartNum.FindStringSubmatch(m)
		return sm[1] + strings.Repeat("X", len(sm[2])) + sm[3]
	})
}

// Parse reads a release title. Version defaults to 1. Batch is true for ranges,
// "Batch", and "Complete"; those releases are not grabbed.
func Parse(raw string) Parsed {
	p := Parsed{Raw: strings.TrimSpace(raw), Version: 1}
	s := p.Raw
	s = reExt.ReplaceAllString(s, "")
	if reBatchWord.MatchString(s) || reRange.MatchString(maskPartNumber(s)) {
		p.Batch = true
	}
	if m := reRes.FindString(s); m != "" {
		p.Resolution = normRes(m)
	}
	if reHEVC.MatchString(s) {
		p.Codec = "hevc"
	} else if reAVC.MatchString(s) {
		p.Codec = "avc"
	}
	if m := reSource.FindString(s); m != "" {
		p.Source = normSource(m)
	}
	if reDual.MatchString(s) {
		p.DualAudio = true
	}
	if reDub.MatchString(s) && !p.DualAudio {
		p.Dub = true
	}
	if reRepack.MatchString(s) {
		p.Repack = true
	}

	scene := strings.Count(s, ".") >= 2 && !strings.Contains(s, " ")
	if m := reBracketGroup.FindStringSubmatch(s); m != nil {
		p.Group = strings.TrimSpace(m[1])
		s = strings.TrimSpace(s[len(m[0]):])
	} else if scene {
		if m := reSceneGroup.FindStringSubmatch(s); m != nil {
			p.Group = m[1]
			s = strings.TrimSpace(strings.TrimSuffix(s, "-"+m[1]))
		}
	}

	s = reAudio.ReplaceAllString(s, " ")
	if scene {
		s = strings.ReplaceAll(s, ".", " ")
		s = strings.ReplaceAll(s, "_", " ")
	}
	s = reHash.ReplaceAllString(s, " ")

	if loc := reRange.FindStringIndex(maskPartNumber(s)); loc != nil {
		p.Batch = true
		frag := s[loc[0]:loc[1]]
		if !reSeasonWord.MatchString(frag) {
			if season, a, b, ok := episodeSpan(frag); ok {
				p.Season = season
				p.Episode = a
				p.EpisodeEnd = b
			}
		}
		if p.Episode == 0 {
			if sm := reRangeFirst.FindStringSubmatch(frag); sm != nil {
				if sm[1] != "" {
					p.Season, _ = strconv.Atoi(sm[1])
					p.Episode, _ = strconv.Atoi(sm[2])
				} else if sm[3] != "" {
					p.Episode, _ = strconv.Atoi(sm[3])
				}
			}
		}
		s = s[:loc[0]] + " " + s[loc[1]:]
	}

	if p.Episode == 0 {
		if m := reSxE.FindStringSubmatch(s); m != nil {
			p.Season, _ = strconv.Atoi(m[1])
			p.Episode, _ = strconv.Atoi(m[2])
			if m[3] != "" {
				if v, err := strconv.Atoi(m[3]); err == nil && v > 0 {
					p.Version = v
				}
			}
			s = reSxE.ReplaceAllString(s, " ")
		} else if m := reDashEp.FindStringSubmatch(s); m != nil {
			p.Episode, _ = strconv.Atoi(m[1])
			if m[2] != "" {
				if v, err := strconv.Atoi(m[2]); err == nil && v > 0 {
					p.Version = v
				}
			}
			s = reDashEp.ReplaceAllString(s, " ")
		}
	}
	if p.Version >= 2 {
		p.Repack = true
	}

	s = reBrackets.ReplaceAllString(s, " ")
	s = reParens.ReplaceAllString(s, " ")
	s = reTech.ReplaceAllString(s, " ")
	s = reYear.ReplaceAllString(s, " ")
	s = reBatchWord.ReplaceAllString(s, " ")
	s = reRepackWord.ReplaceAllString(s, " ")
	s = reSpace.ReplaceAllString(s, " ")
	if p.Group == "" {
		if m := reTrailGroup.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
			p.Group = m[1]
			s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), m[0]))
		}
	}
	p.Title = strings.Trim(reSpace.ReplaceAllString(s, " "), " -_.")
	return p
}

func episodeSpan(frag string) (int, int, int, bool) {
	m := reEpSpan.FindStringSubmatch(frag)
	if m == nil {
		return 0, 0, 0, false
	}
	season := 0
	var a, b string
	switch {
	case m[2] != "":
		season, _ = strconv.Atoi(m[1])
		a, b = m[2], m[3]
	case m[4] != "":
		a, b = m[4], m[5]
	default:
		a, b = m[6], m[7]
	}
	ai, err1 := strconv.Atoi(a)
	bi, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || ai <= 0 || bi < ai {
		return 0, 0, 0, false
	}
	return season, ai, bi, true
}

func normRes(s string) string {
	u := strings.ToLower(strings.TrimSpace(s))
	if u == "4k" {
		return "2160p"
	}
	if !strings.HasSuffix(u, "p") {
		u += "p"
	}
	return u
}

func normSource(s string) string {
	u := strings.ToLower(strings.ReplaceAll(s, " ", ""))
	u = strings.ReplaceAll(u, "-", "")
	switch {
	case strings.Contains(u, "webdl"):
		return "WEB-DL"
	case strings.Contains(u, "webrip"):
		return "WEBRip"
	case strings.Contains(u, "hdtv"):
		return "HDTV"
	default:
		return "BluRay"
	}
}
