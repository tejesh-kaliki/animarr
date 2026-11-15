package encode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Job is a claim from GET /api/encode/next.
type Job struct {
	ID           int64  `json:"id"`
	EpisodeID    int64  `json:"episodeId"`
	FilePath     string `json:"filePath"`
	LeaseSeconds int    `json:"leaseSeconds"`
}

// Heartbeat is the body of POST /api/encode/{id}/heartbeat.
type Heartbeat struct {
	Encoder string  `json:"encoder"`
	Percent float64 `json:"percent"`
	FPS     float64 `json:"fps"`
	Speed   float64 `json:"speed"`
}

// Report is the body of POST /api/encode/{id}/done.
// Skipped keeps the original file and does not refresh Jellyfin.
type Report struct {
	FilePath string  `json:"filePath"`
	Codec    string  `json:"codec"`
	Encoder  string  `json:"encoder"`
	SrcSize  int64   `json:"srcSize"`
	DstSize  int64   `json:"dstSize"`
	Skipped  bool    `json:"skipped"`
	Speed    float64 `json:"speed"`
	Error    string  `json:"error,omitempty"`
}

// Client talks to the animarr encode API. The API has no login.
type Client struct {
	Base string
	HTTP *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) endpoint(path string) string {
	return strings.TrimRight(c.Base, "/") + path
}

// Next claims one job. ok is false when the queue is empty or encoding is off (204).
func (c *Client) Next(ctx context.Context) (Job, bool, error) {
	var job Job
	code, err := c.do(ctx, http.MethodGet, "/api/encode/next", nil, &job)
	if err != nil {
		return Job{}, false, err
	}
	if code == http.StatusNoContent {
		return Job{}, false, nil
	}
	return job, true, nil
}

// Heartbeat extends the lease and stores progress for the status page.
func (c *Client) Heartbeat(ctx context.Context, id int64, beat Heartbeat) error {
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/encode/%d/heartbeat", id), beat, nil)
	return err
}

// Done reports a finished or skipped encode.
func (c *Client) Done(ctx context.Context, id int64, rep Report) error {
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/encode/%d/done", id), rep, nil)
	return err
}

// Fail marks the job failed. The server keeps the original path.
func (c *Client) Fail(ctx context.Context, id int64, reason string) error {
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/encode/%d/fail", id), map[string]string{"error": reason}, nil)
	return err
}

// Release returns a running job to pending.
func (c *Client) Release(ctx context.Context, id int64) error {
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/encode/%d/release", id), nil, nil)
	return err
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), body)
	if err != nil {
		return 0, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = resp.Status
		}
		return resp.StatusCode, fmt.Errorf("%s %s: %s", method, path, msg)
	}
	if out != nil && resp.StatusCode != http.StatusNoContent && len(bytes.TrimSpace(b)) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s %s: %w", method, path, err)
		}
	}
	return resp.StatusCode, nil
}
