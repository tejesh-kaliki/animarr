// Package qbit is a small qBittorrent Web API v2 client.
// Login is skipped when both username and password are empty (localhost bypass).
package qbit

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is a torrent hash the client does not have.
var ErrNotFound = errors.New("torrent not found")

// Torrent is the subset of /torrents/info animarr uses.
type Torrent struct {
	Hash         string  `json:"hash"`
	Name         string  `json:"name"`
	Progress     float64 `json:"progress"`
	State        string  `json:"state"`
	SavePath     string  `json:"save_path"`
	ContentPath  string  `json:"content_path"`
	AmountLeft   int64   `json:"amount_left"`
	CompletionOn int64   `json:"completion_on"`
	Size         int64   `json:"size"`
}

// File is one file inside a torrent. Name is relative to SavePath.
type File struct {
	Name     string  `json:"name"`
	Size     int64   `json:"size"`
	Progress float64 `json:"progress"`
}

// AddRequest is a magnet or URL plus where qBittorrent should put it.
type AddRequest struct {
	URL      string
	Category string
	SavePath string
	Tags     []string
}

// Client talks to one WebUI.
type Client struct {
	BaseURL  string
	Username string
	Password string
	HTTP     *http.Client

	mu sync.Mutex
}

// New builds a client. An empty username and password skip login.
func New(baseURL, username, password string) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Client{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Username: username,
		Password: password,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			Jar:     jar,
		},
	}, nil
}

// Configure replaces the WebUI address and credentials.
// The next request uses them. The cookie jar is kept.
func (c *Client) Configure(baseURL, username, password string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.BaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	c.Username = username
	c.Password = password
}

func (c *Client) snapshot() (base, user, pass string, hc *http.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.BaseURL, c.Username, c.Password, c.HTTP
}

// Test logs in when needed and reads the app version.
func (c *Client) Test(ctx context.Context) (string, error) {
	if err := c.login(ctx); err != nil {
		return "", err
	}
	body, err := c.do(ctx, http.MethodGet, "/api/v2/app/version", nil, false)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

// EnsureCategory creates the category. An existing category is not an error.
func (c *Client) EnsureCategory(ctx context.Context, name, savePath string) error {
	if err := c.login(ctx); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("category", name)
	if savePath != "" {
		form.Set("savePath", savePath)
	}
	_, err := c.do(ctx, http.MethodPost, "/api/v2/torrents/createCategory", form, true)
	if err != nil && !isAlready(err) {
		return err
	}
	return nil
}

// addResponse is the qBittorrent 5 body for POST /api/v2/torrents/add.
// Older versions send the plain text "Ok." or "Fails." instead.
type addResponse struct {
	AddedTorrentIDs []string `json:"added_torrent_ids"`
	FailureCount    int      `json:"failure_count"`
	PendingCount    int      `json:"pending_count"`
	SuccessCount    int      `json:"success_count"`
}

// Add sends a magnet or URL.
// The returned hash is the one qBittorrent reported in added_torrent_ids, or
// the hash of a torrent that was already present. A torrent that is already
// in qBittorrent (any category) is success: the existing torrent is adopted
// and nothing is added again.
func (c *Client) Add(ctx context.Context, req AddRequest) (string, error) {
	if err := c.login(ctx); err != nil {
		return "", err
	}
	if req.Category != "" {
		if err := c.EnsureCategory(ctx, req.Category, req.SavePath); err != nil {
			return "", err
		}
	}
	form := url.Values{}
	form.Set("urls", req.URL)
	if req.Category != "" {
		form.Set("category", req.Category)
	}
	if req.SavePath != "" {
		form.Set("savepath", req.SavePath)
	}
	if len(req.Tags) > 0 {
		form.Set("tags", strings.Join(req.Tags, ","))
	}
	body, err := c.do(ctx, http.MethodPost, "/api/v2/torrents/add", form, false)
	if err != nil {
		if isAlready(err) {
			return c.adoptExisting(ctx, req, err)
		}
		return "", err
	}
	hash, addErr := classifyAdd(body)
	if addErr != nil {
		return c.adoptExisting(ctx, req, addErr)
	}
	if hash == "" {
		hash = magnetHash(req.URL)
	}
	return hash, nil
}

// classifyAdd accepts "Ok." and a v5 JSON success. "Fails." and a JSON body
// with failures and no successes are errors. The hash is the first
// added_torrent_ids entry when that list is present.
func classifyAdd(body []byte) (string, error) {
	msg := strings.TrimSpace(string(body))
	if msg == "" || strings.EqualFold(msg, "Ok.") || strings.EqualFold(msg, "Ok") {
		return "", nil
	}
	if strings.EqualFold(msg, "Fails.") || strings.EqualFold(msg, "Fails") || strings.EqualFold(msg, "Fail.") {
		return "", fmt.Errorf("qbit add: %s", msg)
	}
	if strings.HasPrefix(msg, "{") {
		var parsed addResponse
		if err := json.Unmarshal([]byte(msg), &parsed); err != nil {
			return "", fmt.Errorf("qbit add: %s", msg)
		}
		if parsed.FailureCount == 0 && parsed.SuccessCount+parsed.PendingCount > 0 {
			for _, id := range parsed.AddedTorrentIDs {
				id = strings.ToLower(strings.TrimSpace(id))
				if id != "" {
					return id, nil
				}
			}
			return "", nil
		}
		if parsed.FailureCount > 0 && parsed.SuccessCount == 0 {
			return "", fmt.Errorf("qbit add: %s", msg)
		}
		return "", fmt.Errorf("qbit add: %s", msg)
	}
	return "", fmt.Errorf("qbit add: %s", msg)
}

// adoptExisting treats an add failure as success when that infohash is already
// in qBittorrent. The lookup is by hash only, so the category does not matter.
func (c *Client) adoptExisting(ctx context.Context, req AddRequest, cause error) (string, error) {
	hash := magnetHash(req.URL)
	if hash == "" {
		return "", cause
	}
	tor, err := c.Get(ctx, hash)
	if err != nil || !strings.EqualFold(tor.Hash, hash) {
		return "", cause
	}
	if strings.TrimSpace(tor.Hash) == "" {
		return hash, nil
	}
	return strings.ToLower(tor.Hash), nil
}

// magnetHash reads the btih infohash from a magnet URI.
// A 32-character base32 hash is converted to the 40-character hex form
// qBittorrent uses in the Web API.
func magnetHash(raw string) string {
	low := strings.ToLower(raw)
	const key = "urn:btih:"
	i := strings.Index(low, key)
	if i < 0 {
		return ""
	}
	h := raw[i+len(key):]
	if cut := strings.IndexAny(h, "&? \t\r\n"); cut >= 0 {
		h = h[:cut]
	}
	return normalizeInfoHash(h)
}

func normalizeInfoHash(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return ""
	}
	if len(h) == 40 && isHex(h) {
		return strings.ToLower(h)
	}
	if len(h) == 32 {
		enc := base32.StdEncoding.WithPadding(base32.NoPadding)
		b, err := enc.DecodeString(strings.ToUpper(h))
		if err == nil && len(b) == 20 {
			return hex.EncodeToString(b)
		}
	}
	return strings.ToLower(h)
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return s != ""
}

// Get returns one torrent by hash.
func (c *Client) Get(ctx context.Context, hash string) (Torrent, error) {
	if err := c.login(ctx); err != nil {
		return Torrent{}, err
	}
	q := url.Values{}
	q.Set("hashes", strings.ToLower(hash))
	body, err := c.do(ctx, http.MethodGet, "/api/v2/torrents/info?"+q.Encode(), nil, false)
	if err != nil {
		return Torrent{}, err
	}
	var list []Torrent
	if err := json.Unmarshal(body, &list); err != nil {
		return Torrent{}, fmt.Errorf("qbit info: %w", err)
	}
	if len(list) == 0 {
		return Torrent{}, ErrNotFound
	}
	return list[0], nil
}

// Files lists the torrent contents.
func (c *Client) Files(ctx context.Context, hash string) ([]File, error) {
	if err := c.login(ctx); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("hash", strings.ToLower(hash))
	body, err := c.do(ctx, http.MethodGet, "/api/v2/torrents/files?"+q.Encode(), nil, false)
	if err != nil {
		return nil, err
	}
	var list []File
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("qbit files: %w", err)
	}
	return list, nil
}

// Complete reports a torrent that has finished downloading and is seeding or paused.
func Complete(t Torrent) bool {
	if t.Progress < 0.999 && t.AmountLeft > 0 {
		return false
	}
	switch strings.ToLower(t.State) {
	case "uploading", "stalledup", "pausedup", "queuedup", "forcedup", "checkingup":
		return true
	case "error", "missingfiles", "unknown":
		return false
	default:
		return t.Progress >= 0.999 && t.CompletionOn > 0
	}
}

// Failed reports a torrent qBittorrent has given up on.
func Failed(t Torrent) bool {
	switch strings.ToLower(t.State) {
	case "error", "missingfiles":
		return true
	default:
		return false
	}
}

func (c *Client) login(ctx context.Context) error {
	_, user, pass, _ := c.snapshot()
	if user == "" && pass == "" {
		return nil
	}
	form := url.Values{}
	form.Set("username", user)
	form.Set("password", pass)
	body, err := c.do(ctx, http.MethodPost, "/api/v2/auth/login", form, true)
	if err != nil {
		return err
	}
	msg := strings.TrimSpace(string(body))
	if msg != "" && !strings.EqualFold(msg, "Ok.") && !strings.EqualFold(msg, "Ok") {
		return fmt.Errorf("qbit login: %s", msg)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, form url.Values, tolerateConflict bool) ([]byte, error) {
	var reader io.Reader
	if form != nil {
		reader = strings.NewReader(form.Encode())
	}
	base, _, _, hc := c.snapshot()
	if hc == nil {
		hc = http.DefaultClient
	}
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, base+path, reader)
	if err != nil {
		return nil, err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qbit %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("qbit %s: %s", path, resp.Status)
	}
	if tolerateConflict && (resp.StatusCode == http.StatusConflict || isAlready(fmt.Errorf("%s", body))) {
		return body, fmt.Errorf("already exists: %s", strings.TrimSpace(string(body)))
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("qbit %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func isAlready(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "already") || strings.Contains(s, "conflict") || strings.Contains(s, "409")
}
