// Package anilist reads public anime lists and search from the AniList GraphQL API.
// Public lists need no token. ANILIST_USERNAME selects the list.
package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultEndpoint = "https://graphql.anilist.co"

// Client talks to GraphQL. Endpoint is overridable in tests.
// Pace is the minimum gap between requests. New sets it. Tests set it to 0.
type Client struct {
	Endpoint  string
	UserAgent string
	Token     string
	HTTP      *http.Client
	Backoff   time.Duration
	Pace      time.Duration

	mu   sync.Mutex
	next time.Time
}

// Configure sets the User-Agent and optional bearer token used on the next request.
func (c *Client) Configure(userAgent, token string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(userAgent) != "" {
		c.UserAgent = userAgent
	}
	c.Token = strings.TrimSpace(token)
}

func (c *Client) credentials() (userAgent, token string) {
	if c == nil {
		return "", ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.UserAgent, c.Token
}

// New returns a client for the public API.
func New(userAgent string) *Client {
	if userAgent == "" {
		userAgent = "animarr/0.1 (+homelab; anime tracker)"
	}
	return &Client{
		Endpoint:  defaultEndpoint,
		UserAgent: userAgent,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		Backoff:   2 * time.Second,
		Pace:      350 * time.Millisecond,
	}
}

// Title is the three AniList title scripts.
type Title struct {
	Romaji  string `json:"romaji"`
	English string `json:"english"`
	Native  string `json:"native"`
}

// Media is one anime. Episode totals use EpisodeTotal, which treats a movie as one episode.
type Media struct {
	ID                int      `json:"id"`
	Title             Title    `json:"title"`
	Synonyms          []string `json:"synonyms"`
	Format            string   `json:"format"`
	Status            string   `json:"status"`
	Episodes          *int     `json:"episodes"`
	NextAiringEpisode *struct {
		Episode  int   `json:"episode"`
		AiringAt int64 `json:"airingAt"`
	} `json:"nextAiringEpisode"`
	AiringSchedule struct {
		Nodes []struct {
			Episode  int   `json:"episode"`
			AiringAt int64 `json:"airingAt"`
		} `json:"nodes"`
	} `json:"airingSchedule"`
	CoverImage struct {
		ExtraLarge string `json:"extraLarge"`
		Large      string `json:"large"`
		Medium     string `json:"medium"`
	} `json:"coverImage"`
	Season     string `json:"season"`
	SeasonYear *int   `json:"seasonYear"`
	StartDate  struct {
		Year  *int `json:"year"`
		Month *int `json:"month"`
		Day   *int `json:"day"`
	} `json:"startDate"`
	Description string   `json:"description"`
	Genres      []string `json:"genres"`
	Studios     struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"studios"`
	Relations struct {
		Edges []RelationEdge `json:"edges"`
	} `json:"relations"`
}

// RelationEdge is one AniList relation. Episode count and start date are filled when the query asks for them.
type RelationEdge struct {
	RelationType string `json:"relationType"`
	Node         struct {
		ID        int    `json:"id"`
		Type      string `json:"type"`
		Format    string `json:"format"`
		Status    string `json:"status"`
		Episodes  *int   `json:"episodes"`
		Title     Title  `json:"title"`
		StartDate struct {
			Year  *int `json:"year"`
			Month *int `json:"month"`
			Day   *int `json:"day"`
		} `json:"startDate"`
	} `json:"node"`
}

// Entry is one list row.
type Entry struct {
	Status string `json:"status"`
	Media  Media  `json:"media"`
}

// DisplayTitle prefers English, then romaji, then native.
func (m Media) DisplayTitle() string {
	if strings.TrimSpace(m.Title.English) != "" {
		return m.Title.English
	}
	if strings.TrimSpace(m.Title.Romaji) != "" {
		return m.Title.Romaji
	}
	return m.Title.Native
}

// Names is every title and synonym, empties removed.
func (m Media) Names() []string {
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		for _, e := range out {
			if strings.EqualFold(e, s) {
				return
			}
		}
		out = append(out, s)
	}
	add(m.Title.Romaji)
	add(m.Title.English)
	add(m.Title.Native)
	for _, s := range m.Synonyms {
		add(s)
	}
	return out
}

// CoverURL is the largest available poster.
func (m Media) CoverURL() string {
	if m.CoverImage.ExtraLarge != "" {
		return m.CoverImage.ExtraLarge
	}
	if m.CoverImage.Large != "" {
		return m.CoverImage.Large
	}
	return m.CoverImage.Medium
}

// StudioName is the first studio, or empty.
func (m Media) StudioName() string {
	if len(m.Studios.Nodes) == 0 {
		return ""
	}
	return strings.TrimSpace(m.Studios.Nodes[0].Name)
}

// StartTime is midnight UTC on the AniList start date. Missing parts become 1. Zero when the year is missing.
func (m Media) StartTime() time.Time {
	if m.StartDate.Year == nil {
		return time.Time{}
	}
	month, day := 1, 1
	if m.StartDate.Month != nil && *m.StartDate.Month > 0 {
		month = *m.StartDate.Month
	}
	if m.StartDate.Day != nil && *m.StartDate.Day > 0 {
		day = *m.StartDate.Day
	}
	return time.Date(*m.StartDate.Year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

// StartYear is 0 when AniList has no year.
func (m Media) StartYear() int {
	if m.StartDate.Year == nil {
		return 0
	}
	return *m.StartDate.Year
}

// StartMonth is 0 when AniList has no month.
func (m Media) StartMonth() int {
	if m.StartDate.Month == nil || *m.StartDate.Month < 1 || *m.StartDate.Month > 12 {
		return 0
	}
	return *m.StartDate.Month
}

// StartDay is 0 when AniList has no day.
func (m Media) StartDay() int {
	if m.StartDate.Day == nil || *m.StartDate.Day < 1 || *m.StartDate.Day > 31 {
		return 0
	}
	return *m.StartDate.Day
}

// SeasonYearValue is 0 when unset.
func (m Media) SeasonYearValue() int {
	if m.SeasonYear == nil {
		return 0
	}
	return *m.SeasonYear
}

// EpisodeTotal is the number of episode rows to keep.
// Movies with a null count are one episode. Airing shows extend to the next episode.
func (m Media) EpisodeTotal() int {
	n := 0
	if m.Episodes != nil {
		n = *m.Episodes
	}
	if strings.EqualFold(m.Format, "MOVIE") && n == 0 {
		n = 1
	}
	if m.NextAiringEpisode != nil && m.NextAiringEpisode.Episode > n {
		n = m.NextAiringEpisode.Episode
	}
	for _, node := range m.AiringSchedule.Nodes {
		if node.Episode > n {
			n = node.Episode
		}
	}
	return n
}

// AirTimes maps episode number to air time. The next airing episode wins over the schedule page.
func (m Media) AirTimes() map[int]time.Time {
	out := map[int]time.Time{}
	for _, node := range m.AiringSchedule.Nodes {
		if node.Episode > 0 && node.AiringAt > 0 {
			out[node.Episode] = time.Unix(node.AiringAt, 0).UTC()
		}
	}
	if m.NextAiringEpisode != nil && m.NextAiringEpisode.Episode > 0 && m.NextAiringEpisode.AiringAt > 0 {
		out[m.NextAiringEpisode.Episode] = time.Unix(m.NextAiringEpisode.AiringAt, 0).UTC()
	}
	return out
}

// NextEpisode is 0 when nothing is scheduled.
func (m Media) NextEpisode() int {
	if m.NextAiringEpisode == nil {
		return 0
	}
	return m.NextAiringEpisode.Episode
}

// RelationsJSON is a compact list kept for later, not used to guess seasons.
func (m Media) RelationsJSON() string {
	type rel struct {
		Type   string `json:"type"`
		ID     int    `json:"id"`
		Format string `json:"format"`
		Romaji string `json:"romaji"`
	}
	var list []rel
	for _, e := range m.Relations.Edges {
		if e.Node.ID == 0 {
			continue
		}
		list = append(list, rel{Type: e.RelationType, ID: e.Node.ID, Format: e.Node.Format, Romaji: e.Node.Title.Romaji})
	}
	if list == nil {
		list = []rel{}
	}
	b, err := json.Marshal(list)
	if err != nil {
		return "[]"
	}
	return string(b)
}

const mediaFields = `
  id
  title { romaji english native }
  synonyms
  format
  status
  episodes
  nextAiringEpisode { episode airingAt }
  airingSchedule(notYetAired: false, perPage: 50) { nodes { episode airingAt } }
  coverImage { extraLarge large medium }
  season
  seasonYear
  description(asHtml: false)
  genres
  studios { nodes { name } }
  startDate { year month day }
  relations { edges { relationType node { id type format status episodes title { romaji english } startDate { year month day } } } }
`

// ListCollection loads CURRENT and PLANNING anime for a public username.
func (c *Client) ListCollection(ctx context.Context, username string) ([]Entry, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("anilist username is empty")
	}
	var data struct {
		MediaListCollection struct {
			Lists []struct {
				Status  string  `json:"status"`
				Entries []Entry `json:"entries"`
			} `json:"lists"`
		} `json:"MediaListCollection"`
	}
	q := `query ($userName: String) {
  MediaListCollection(userName: $userName, type: ANIME, status_in: [CURRENT, PLANNING]) {
    lists {
      status
      entries { status media {` + mediaFields + `} }
    }
  }
}`
	if err := c.query(ctx, q, map[string]any{"userName": username}, &data); err != nil {
		return nil, err
	}
	var out []Entry
	for _, list := range data.MediaListCollection.Lists {
		for _, e := range list.Entries {
			if e.Status == "" {
				e.Status = list.Status
			}
			if e.Media.ID == 0 {
				continue
			}
			out = append(out, e)
		}
	}
	return out, nil
}

// Search returns one page of anime matches.
func (c *Client) Search(ctx context.Context, text string) ([]Media, error) {
	text = strings.TrimSpace(text)
	if len(text) < 2 {
		return nil, fmt.Errorf("search query is too short")
	}
	var data struct {
		Page struct {
			Media []Media `json:"media"`
		} `json:"Page"`
	}
	q := `query ($search: String) {
  Page(page: 1, perPage: 20) {
    media(search: $search, type: ANIME, sort: SEARCH_MATCH) {` + mediaFields + `}
  }
}`
	if err := c.query(ctx, q, map[string]any{"search": text}, &data); err != nil {
		return nil, err
	}
	return data.Page.Media, nil
}

// Get fetches one anime by id.
func (c *Client) Get(ctx context.Context, id int) (Media, error) {
	if id <= 0 {
		return Media{}, fmt.Errorf("anilist id is empty")
	}
	var data struct {
		Media Media `json:"Media"`
	}
	q := `query ($id: Int) { Media(id: $id, type: ANIME) {` + mediaFields + `} }`
	if err := c.query(ctx, q, map[string]any{"id": id}, &data); err != nil {
		return Media{}, err
	}
	if data.Media.ID == 0 {
		return Media{}, fmt.Errorf("anilist media %d not found", id)
	}
	return data.Media, nil
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (c *Client) query(ctx context.Context, query string, vars any, dest any) error {
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	backoff := c.Backoff
	if backoff <= 0 {
		backoff = 2 * time.Second
	}
	if err := c.waitPace(ctx); err != nil {
		return err
	}
	var last error
	var wait time.Duration
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			if wait <= 0 {
				wait = backoff << (attempt - 1)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			wait = 0
		}
		reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			cancel()
			return err
		}
		ua, token := c.credentials()
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", ua)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		hc := c.HTTP
		if hc == nil {
			hc = http.DefaultClient
		}
		resp, err := hc.Do(req)
		if err != nil {
			cancel()
			last = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		resp.Body.Close()
		cancel()
		if readErr != nil {
			last = readErr
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			last = fmt.Errorf("anilist %s", resp.Status)
			// AniList rate limits per minute; honour Retry-After (capped).
			if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
				wait = time.Duration(secs) * time.Second
				if wait > 70*time.Second {
					wait = 70 * time.Second
				}
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("anilist %s", resp.Status)
		}
		var wrap gqlResponse
		if err := json.Unmarshal(body, &wrap); err != nil {
			return fmt.Errorf("anilist decode: %w", err)
		}
		if len(wrap.Errors) > 0 && (len(wrap.Data) == 0 || string(wrap.Data) == "null") {
			return fmt.Errorf("anilist: %s", wrap.Errors[0].Message)
		}
		if err := json.Unmarshal(wrap.Data, dest); err != nil {
			return fmt.Errorf("anilist data: %w", err)
		}
		return nil
	}
	return last
}

func (c *Client) waitPace(ctx context.Context) error {
	if c == nil || c.Pace <= 0 {
		return nil
	}
	c.mu.Lock()
	now := time.Now()
	wait := time.Duration(0)
	if c.next.After(now) {
		wait = time.Until(c.next)
	}
	c.next = now.Add(wait + c.Pace)
	c.mu.Unlock()
	if wait <= 0 {
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
