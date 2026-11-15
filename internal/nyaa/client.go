// Package nyaa searches Nyaa.si (or a mirror) over its public RSS feed.
package nyaa

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"animarr/internal/indexer"
)

// Client is a polite Nyaa RSS reader. URLs are tried in order when one fails.
type Client struct {
	URLs        []string
	Category    string
	UserAgent   string
	MinInterval time.Duration
	Backoff     time.Duration
	HTTP        *http.Client
	Log         *slog.Logger

	mu   sync.Mutex
	last time.Time
}

// New builds a client. urls fall back in order. category defaults to 1_2 (English-translated anime).
func New(urls []string, category, userAgent string, minInterval time.Duration) *Client {
	if category == "" {
		category = "1_2"
	}
	if userAgent == "" {
		userAgent = "animarr/0.1 (+homelab; anime tracker)"
	}
	if minInterval <= 0 {
		minInterval = 2 * time.Second
	}
	clean := make([]string, 0, len(urls))
	for _, u := range urls {
		u = strings.TrimRight(strings.TrimSpace(u), "/")
		if u != "" {
			clean = append(clean, u)
		}
	}
	if len(clean) == 0 {
		clean = []string{"https://nyaa.si"}
	}
	return &Client{
		URLs:        clean,
		Category:    category,
		UserAgent:   userAgent,
		MinInterval: minInterval,
		Backoff:     2 * time.Second,
		HTTP:        &http.Client{Timeout: 30 * time.Second},
	}
}

// Configure replaces mirrors, category, user agent, and the request gap.
// The next search uses them.
func (c *Client) Configure(urls []string, category, userAgent string, minInterval time.Duration) {
	if c == nil {
		return
	}
	clean := make([]string, 0, len(urls))
	for _, u := range urls {
		u = strings.TrimRight(strings.TrimSpace(u), "/")
		if u != "" {
			clean = append(clean, u)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(clean) > 0 {
		c.URLs = clean
	}
	if category != "" {
		c.Category = category
	}
	if strings.TrimSpace(userAgent) != "" {
		c.UserAgent = userAgent
	}
	if minInterval > 0 {
		c.MinInterval = minInterval
	}
}

func (c *Client) endpoints() (urls []string, ua string, hc *http.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.URLs...), c.UserAgent, c.HTTP
}

func (c *Client) Name() string { return "nyaa" }

func (c *Client) Search(ctx context.Context, q indexer.Query) ([]indexer.Release, error) {
	cat := q.Category
	if cat == "" {
		cat = c.category()
	}
	return c.fetch(ctx, q.Text, cat)
}

func (c *Client) Recent(ctx context.Context, category string) ([]indexer.Release, error) {
	if category == "" {
		category = c.category()
	}
	return c.fetch(ctx, "", category)
}

func (c *Client) category() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Category
}

func (c *Client) fetch(ctx context.Context, text, category string) ([]indexer.Release, error) {
	urls, _, _ := c.endpoints()
	var last error
	for i, base := range urls {
		u, err := rssURL(base, text, category)
		if err != nil {
			last = err
			continue
		}
		body, err := c.get(ctx, u)
		if err != nil {
			last = err
			c.logger().Warn("nyaa mirror failed", "url", base, "err", err)
			continue
		}
		rels, err := ParseRSS(body)
		if err != nil {
			last = err
			c.logger().Warn("nyaa mirror parse failed", "url", base, "err", err)
			continue
		}
		if i > 0 {
			c.logger().Info("nyaa used fallback mirror", "url", base)
		}
		return rels, nil
	}
	if last == nil {
		last = fmt.Errorf("no nyaa urls configured")
	}
	return nil, fmt.Errorf("all nyaa mirrors failed: %w", last)
}

func rssURL(base, text, category string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("page", "rss")
	q.Set("c", category)
	q.Set("f", "0")
	if strings.TrimSpace(text) != "" {
		q.Set("q", text)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	_, ua, hc := c.endpoints()
	if hc == nil {
		hc = http.DefaultClient
	}
	backoff := c.Backoff
	if backoff <= 0 {
		backoff = 2 * time.Second
	}
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			wait := backoff << (attempt - 1)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}
		if err := c.wait(ctx); err != nil {
			return nil, err
		}
		reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
		if err != nil {
			cancel()
			return nil, err
		}
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")
		resp, err := hc.Do(req)
		if err != nil {
			cancel()
			last = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		cancel()
		if readErr != nil {
			last = readErr
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			last = fmt.Errorf("%s: %s", rawURL, resp.Status)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s: %s", rawURL, resp.Status)
		}
		return body, nil
	}
	return nil, last
}

func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	wait := c.MinInterval - time.Since(c.last)
	if wait < 0 {
		wait = 0
	}
	// Reserve the slot so overlapping callers queue instead of bursting.
	c.last = time.Now().Add(wait)
	c.mu.Unlock()
	if wait == 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) logger() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.Default()
}
