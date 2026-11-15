// Package indexer is the search boundary. Nyaa implements it.
package indexer

import (
	"context"
	"strings"
	"time"
)

// Release is one torrent result. InfoHash is lowercase hex when known.
type Release struct {
	Title    string
	Link     string
	InfoHash string
	Magnet   string
	Size     int64
	Seeders  int
	Leechers int
	PubDate  time.Time
	Indexer  string
	Category string
}

// Query is a text search. Episode filtering happens after parsing, not here.
type Query struct {
	Text     string
	Category string
	Episode  int
}

// Indexer searches an anime source and reads its recent feed.
type Indexer interface {
	Name() string
	Search(ctx context.Context, q Query) ([]Release, error)
	Recent(ctx context.Context, category string) ([]Release, error)
}

// Dedupe merges releases and keeps the first copy of each infohash.
func Dedupe(lists ...[]Release) []Release {
	seen := map[string]struct{}{}
	var out []Release
	for _, list := range lists {
		for _, r := range list {
			key := strings.ToLower(strings.TrimSpace(r.InfoHash))
			if key == "" {
				key = r.Link
			}
			if key == "" {
				key = r.Title
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, r)
		}
	}
	return out
}
