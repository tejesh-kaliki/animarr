// Package migrate reads an existing Sonarr v3 library and records files in place.
package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Sonarr is a small v3 API client.
type Sonarr struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// Series is the subset of /api/v3/series used for matching.
type Series struct {
	ID              int    `json:"id"`
	Title           string `json:"title"`
	SortTitle       string `json:"sortTitle"`
	CleanTitle      string `json:"cleanTitle"`
	Year            int    `json:"year"`
	Path            string `json:"path"`
	AlternateTitles []struct {
		Title string `json:"title"`
	} `json:"alternateTitles"`
}

// Episode is one Sonarr episode row.
type Episode struct {
	ID            int    `json:"id"`
	SeriesID      int    `json:"seriesId"`
	EpisodeFileID int    `json:"episodeFileId"`
	SeasonNumber  int    `json:"seasonNumber"`
	EpisodeNumber int    `json:"episodeNumber"`
	Title         string `json:"title"`
	HasFile       bool   `json:"hasFile"`
}

// EpisodeFile is one file on disk. Path is kept as Sonarr reports it.
type EpisodeFile struct {
	ID           int    `json:"id"`
	SeriesID     int    `json:"seriesId"`
	SeasonNumber int    `json:"seasonNumber"`
	Path         string `json:"path"`
	RelativePath string `json:"relativePath"`
}

// NewSonarr returns a client. An empty URL is rejected by calls, not by New.
func NewSonarr(baseURL, apiKey string) *Sonarr {
	return &Sonarr{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:  strings.TrimSpace(apiKey),
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// SeriesList loads every series.
func (c *Sonarr) SeriesList(ctx context.Context) ([]Series, error) {
	var out []Series
	if err := c.get(ctx, "/api/v3/series", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Episodes loads episodes for one series.
func (c *Sonarr) Episodes(ctx context.Context, seriesID int) ([]Episode, error) {
	var out []Episode
	path := "/api/v3/episode?seriesId=" + url.QueryEscape(fmt.Sprint(seriesID))
	if err := c.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// EpisodeFiles loads files for one series.
func (c *Sonarr) EpisodeFiles(ctx context.Context, seriesID int) ([]EpisodeFile, error) {
	var out []EpisodeFile
	path := "/api/v3/episodefile?seriesId=" + url.QueryEscape(fmt.Sprint(seriesID))
	if err := c.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Sonarr) get(ctx context.Context, path string, dest any) error {
	if c.BaseURL == "" {
		return fmt.Errorf("SONARR_URL is not set")
	}
	if c.APIKey == "" {
		return fmt.Errorf("SONARR_API_KEY is not set")
	}
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	req.Header.Set("Accept", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sonarr %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("sonarr decode: %w", err)
	}
	return nil
}
