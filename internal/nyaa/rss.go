package nyaa

import (
	"encoding/xml"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"animarr/internal/indexer"
)

const nyaaNS = "https://nyaa.si/xmlns/nyaa"

type rssFeed struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title    string `xml:"title"`
	Link     string `xml:"link"`
	PubDate  string `xml:"pubDate"`
	Seeders  string `xml:"https://nyaa.si/xmlns/nyaa seeders"`
	Leechers string `xml:"https://nyaa.si/xmlns/nyaa leechers"`
	InfoHash string `xml:"https://nyaa.si/xmlns/nyaa infoHash"`
	Category string `xml:"https://nyaa.si/xmlns/nyaa categoryId"`
	Size     string `xml:"https://nyaa.si/xmlns/nyaa size"`
}

// ParseRSS reads a Nyaa RSS document.
func ParseRSS(b []byte) ([]indexer.Release, error) {
	var feed rssFeed
	if err := xml.Unmarshal(b, &feed); err != nil {
		return nil, fmt.Errorf("nyaa rss: %w", err)
	}
	out := make([]indexer.Release, 0, len(feed.Channel.Items))
	for _, it := range feed.Channel.Items {
		hash := strings.ToLower(strings.TrimSpace(it.InfoHash))
		rel := indexer.Release{
			Title:    strings.TrimSpace(it.Title),
			Link:     strings.TrimSpace(it.Link),
			InfoHash: hash,
			Size:     parseSize(it.Size),
			Seeders:  atoi(it.Seeders),
			Leechers: atoi(it.Leechers),
			Indexer:  "nyaa",
			Category: strings.TrimSpace(it.Category),
		}
		if hash != "" {
			rel.Magnet = magnet(hash, rel.Title)
		}
		if it.PubDate != "" {
			if t, err := time.Parse(time.RFC1123Z, strings.TrimSpace(it.PubDate)); err == nil {
				rel.PubDate = t.UTC()
			} else if t, err := time.Parse(time.RFC1123, strings.TrimSpace(it.PubDate)); err == nil {
				rel.PubDate = t.UTC()
			}
		}
		out = append(out, rel)
	}
	return out, nil
}

func magnet(hash, title string) string {
	return "magnet:?xt=urn:btih:" + hash + "&dn=" + url.QueryEscape(title)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// parseSize reads Nyaa sizes such as "1.4 GiB" or "850.2 MiB".
func parseSize(s string) int64 {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) == 0 {
		return 0
	}
	n, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	unit := ""
	if len(fields) > 1 {
		unit = strings.ToUpper(fields[1])
	}
	var mult float64 = 1
	switch unit {
	case "TIB", "TB":
		mult = 1 << 40
	case "GIB", "GB":
		mult = 1 << 30
	case "MIB", "MB":
		mult = 1 << 20
	case "KIB", "KB":
		mult = 1 << 10
	}
	return int64(n * mult)
}
