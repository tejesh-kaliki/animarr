// Package match compares a parsed release title with AniList titles and synonyms.
// Comparison is case- and punctuation-insensitive. Close season/film variants
// stay under the threshold so one cour is not grabbed for another.
package match

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Candidate is one show the release might belong to.
type Candidate struct {
	Key    int64
	Titles []string
}

// Normalize folds case and punctuation. Apostrophes are removed so
// "Journey's" and "Journeys" meet. A leading "the" is dropped.
func Normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		switch r {
		case '\'', '\u2019', '\u2018':
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevSpace = false
			continue
		}
		if !prevSpace {
			b.WriteByte(' ')
			prevSpace = true
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimPrefix(out, "the ")
	return out
}

// ScoreTitles returns the best similarity between release and any title.
func ScoreTitles(release string, titles []string) float64 {
	best := 0.0
	for _, t := range titles {
		if s := scorePair(release, t); s > best {
			best = s
		}
	}
	return best
}

// Best returns the candidate at or above threshold.
// Two candidates within 0.03 of each other are ambiguous unless one is an exact match.
func Best(release string, cands []Candidate, threshold float64) (Candidate, float64, bool) {
	type hit struct {
		c Candidate
		s float64
	}
	var hits []hit
	for _, c := range cands {
		s := ScoreTitles(release, c.Titles)
		if s >= threshold {
			hits = append(hits, hit{c, s})
		}
	}
	if len(hits) == 0 {
		return Candidate{}, 0, false
	}
	for i := 1; i < len(hits); i++ {
		if hits[i].s > hits[0].s {
			hits[0], hits[i] = hits[i], hits[0]
		}
	}
	if len(hits) == 1 {
		return hits[0].c, hits[0].s, true
	}
	second := hits[1].s
	for _, h := range hits[2:] {
		if h.s > second {
			second = h.s
		}
	}
	if hits[0].s >= 1 && second < 1 {
		return hits[0].c, hits[0].s, true
	}
	if hits[0].s-second >= 0.03 {
		return hits[0].c, hits[0].s, true
	}
	return Candidate{}, hits[0].s, false
}

var reOther = regexp.MustCompile(`(?i)\b(?:season|part|film|movie|ova|oad|special|cour|final|2nd|3rd|4th|5th|viii|vii|iii|ii|iv|vi|ix|v)\b`)
var reOnlyNum = regexp.MustCompile(`^\d+$`)

var kindTokens = map[string]bool{"movie": true, "film": true, "ova": true, "oad": true}

func scorePair(release, candidate string) float64 {
	a := Normalize(release)
	b := Normalize(candidate)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	// "Road Trip" and "Roadtrip" are the same title once spaces are removed.
	if ca, cb := strings.ReplaceAll(a, " ", ""), strings.ReplaceAll(b, " ", ""); ca != "" && ca == cb {
		return 1
	}
	if sa, sb := stripKind(a), stripKind(b); sa != "" && sa == sb {
		return 0.98
	}
	shorter, longer := a, b
	if utf8.RuneCountInString(shorter) > utf8.RuneCountInString(longer) {
		shorter, longer = longer, shorter
	}
	if strings.Contains(longer, shorter) {
		extra := strings.TrimSpace(strings.Replace(longer, shorter, "", 1))
		extra = strings.TrimSpace(extra)
		if extra == "" {
			return 1
		}
		if otherWork(extra) {
			return 0.45
		}
		if float64(utf8.RuneCountInString(shorter))/float64(utf8.RuneCountInString(longer)) >= 0.85 {
			return 0.9
		}
		return 0.55
	}
	return ratio(a, b)
}

func otherWork(extra string) bool {
	if reOther.MatchString(extra) {
		return true
	}
	return reOnlyNum.MatchString(strings.TrimSpace(extra))
}

func stripKind(s string) string {
	parts := strings.Fields(s)
	for len(parts) > 1 && kindTokens[parts[len(parts)-1]] {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, " ")
}

func ratio(a, b string) float64 {
	if a == b {
		return 1
	}
	ar := []rune(a)
	br := []rune(b)
	if len(ar) == 0 || len(br) == 0 {
		return 0
	}
	if len(ar) > 200 {
		ar = ar[:200]
	}
	if len(br) > 200 {
		br = br[:200]
	}
	d := levenshtein(ar, br)
	m := len(ar)
	if len(br) > m {
		m = len(br)
	}
	return 1 - float64(d)/float64(m)
}

func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := cur[j-1] + 1
			sub := prev[j-1] + cost
			cur[j] = del
			if ins < cur[j] {
				cur[j] = ins
			}
			if sub < cur[j] {
				cur[j] = sub
			}
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
