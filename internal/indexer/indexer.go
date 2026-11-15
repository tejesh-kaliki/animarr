// Package indexer is the search boundary. Nyaa implements it.
// AnimeTosho and Torznab are stubs so a later client can satisfy the same interface.
package indexer

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ErrNotImplemented is returned by indexers that are only sketched.
var ErrNotImplemented = errors.New("indexer not implemented")

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

// AnimeTosho will query animetosho.org. It is not implemented.
type AnimeTosho struct{}

func (AnimeTosho) Name() string { return "animetosho" }

func (AnimeTosho) Search(context.Context, Query) ([]Release, error) {
	return nil, ErrNotImplemented
}

func (AnimeTosho) Recent(context.Context, string) ([]Release, error) {
	return nil, ErrNotImplemented
}

// Torznab will speak the Torznab API. BaseURL and APIKey are reserved for that client.
type Torznab struct {
	BaseURL string
	APIKey  string
}

func (t Torznab) Name() string {
	if t.BaseURL != "" {
		return "torznab"
	}
	return "torznab"
}

func (Torznab) Search(context.Context, Query) ([]Release, error) {
	return nil, ErrNotImplemented
}

func (Torznab) Recent(context.Context, string) ([]Release, error) {
	return nil, ErrNotImplemented
}
