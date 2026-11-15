// Package jellyfin calls the Jellyfin API to refresh a library after an import.
package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client is optional. Enabled is false until both URL and API key are set.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client

	mu sync.Mutex
}

// Configure replaces the base URL and API key. The next call uses them.
func (c *Client) Configure(baseURL, apiKey string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.BaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	c.APIKey = strings.TrimSpace(apiKey)
}

func (c *Client) snapshot() (base, key string, hc *http.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	hc = c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	return c.BaseURL, c.APIKey, hc
}

// New returns a client. Empty URL or key leaves it disabled.
func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:  strings.TrimSpace(apiKey),
		HTTP:    &http.Client{Timeout: 20 * time.Second},
	}
}

// Enabled reports whether a refresh will be attempted.
func (c *Client) Enabled() bool {
	if c == nil {
		return false
	}
	base, key, _ := c.snapshot()
	return base != "" && key != ""
}

// Test reads /System/Info.
func (c *Client) Test(ctx context.Context) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("jellyfin is not configured")
	}
	body, err := c.do(ctx, http.MethodGet, "/System/Info")
	if err != nil {
		return "", err
	}
	var info struct {
		ServerName string `json:"ServerName"`
		Version    string `json:"Version"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("jellyfin info: %w", err)
	}
	if info.ServerName == "" {
		return info.Version, nil
	}
	if info.Version == "" {
		return info.ServerName, nil
	}
	return info.ServerName + " " + info.Version, nil
}

// Refresh asks Jellyfin to rescan libraries.
func (c *Client) Refresh(ctx context.Context) error {
	if !c.Enabled() {
		return fmt.Errorf("jellyfin is not configured")
	}
	_, err := c.do(ctx, http.MethodPost, "/Library/Refresh")
	return err
}

func (c *Client) do(ctx context.Context, method, path string) ([]byte, error) {
	base, key, hc := c.snapshot()
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", `MediaBrowser Client="animarr", Device="animarr", DeviceId="animarr", Version="0.1", Token="`+key+`"`)
	req.Header.Set("X-Emby-Token", key)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jellyfin %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("jellyfin %s: %s", path, resp.Status)
	}
	return body, nil
}
