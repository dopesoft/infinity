// Package browser is Core's client + tool surface for the cloud browser
// sidecar (docker/browser). It exposes the agent-robust observe → act →
// extract verb set as native tools, tracks each browser session as a
// mem_runs row, and relays the sidecar's live CDP screencast to Studio so
// the boss watches Jarvis drive in real time.
//
// Per Rule #1 (CLAUDE.md): this is a generic building block. There is no
// per-site logic and no embedded prompt — the "how to browse" judgment
// lives in the seeded web-browsing skill, not here.
package browser

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the browser sidecar over its private-network HTTP API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New returns a client for the sidecar at baseURL (e.g.
// http://browser.railway.internal:8080) authenticated with the shared
// bearer token. Returns nil when baseURL is empty so callers can treat a
// nil client as "browser disabled".
func New(baseURL, token string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &Client{
		baseURL: baseURL,
		token:   strings.TrimSpace(token),
		// Generous timeout: a navigate + settle can legitimately take
		// ~10s on a heavy page, and cold-start (App Sleeping wake) adds
		// more. The screencast stream uses its own no-timeout client.
		http: &http.Client{Timeout: 90 * time.Second},
	}
}

// ── wire types (mirror docker/browser/main.go) ──────────────────────────────

type SessionInfo struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Element struct {
	Idx         int    `json:"idx"`
	Tag         string `json:"tag"`
	Role        string `json:"role,omitempty"`
	Text        string `json:"text,omitempty"`
	Type        string `json:"type,omitempty"`
	Name        string `json:"name,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Href        string `json:"href,omitempty"`
	Value       string `json:"value,omitempty"`
}

type ObserveResult struct {
	Title    string    `json:"title"`
	URL      string    `json:"url"`
	Text     string    `json:"text"`
	Elements []Element `json:"elements"`
}

type ActRequest struct {
	Index  int    `json:"index"`
	Action string `json:"action"`
	Value  string `json:"value,omitempty"`
}

type ActResult struct {
	OK      bool   `json:"ok"`
	URL     string `json:"url"`
	Title   string `json:"title,omitempty"`
	Retried bool   `json:"retried,omitempty"`
	Error   string `json:"error,omitempty"`
}

type NavResult struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Error string `json:"error,omitempty"`
}

type ExtractResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

type ShotResult struct {
	URL     string `json:"url"`
	DataURL string `json:"data_url"`
}

// InputEvent is a raw, coordinate-based human interaction forwarded from
// Studio when the boss takes over the live browser by hand (clicks the
// screencast, types, scrolls). Mirrors docker/browser inputRequest. Distinct
// from ActRequest, which is the agent's index-based verb set.
type InputEvent struct {
	Type   string  `json:"type"`             // click | move | scroll | text | key
	X      float64 `json:"x,omitempty"`      // viewport px
	Y      float64 `json:"y,omitempty"`      // viewport px
	Button string  `json:"button,omitempty"` // left (default) | right | middle
	DeltaX float64 `json:"delta_x,omitempty"`
	DeltaY float64 `json:"delta_y,omitempty"`
	Text   string  `json:"text,omitempty"`
	Key    string  `json:"key,omitempty"`
	Width  int64   `json:"width,omitempty"`  // for type=resize
	Height int64   `json:"height,omitempty"` // for type=resize
}

// Frame is one screencast frame relayed from the sidecar SSE stream. URL is
// not sent by the sidecar — the registry fills it from the session's last
// known location so the live tab's toolbar stays accurate.
type Frame struct {
	Seq       int    `json:"seq"`
	Frame     string `json:"frame"`         // data:image/jpeg;base64,...
	URL       string `json:"url,omitempty"` // filled by the registry, not the wire
	BrowserID string `json:"-"`             // filled by the registry; the live session id
}

// ── calls ────────────────────────────────────────────────────────────────

func (c *Client) Health(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("browser sidecar health: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) CreateSession(ctx context.Context, url string) (*SessionInfo, error) {
	var out SessionInfo
	if err := c.post(ctx, "/session", map[string]any{"url": url}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Navigate(ctx context.Context, sessionID, url string) (*NavResult, error) {
	var out NavResult
	if err := c.post(ctx, "/session/"+sessionID+"/navigate", map[string]any{"url": url}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Observe(ctx context.Context, sessionID string) (*ObserveResult, error) {
	var out ObserveResult
	if err := c.post(ctx, "/session/"+sessionID+"/observe", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Act(ctx context.Context, sessionID string, req ActRequest) (*ActResult, error) {
	var out ActResult
	if err := c.post(ctx, "/session/"+sessionID+"/act", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Extract(ctx context.Context, sessionID, format string) (*ExtractResult, error) {
	var out ExtractResult
	if err := c.post(ctx, "/session/"+sessionID+"/extract", map[string]any{"format": format}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Screenshot(ctx context.Context, sessionID string) (*ShotResult, error) {
	var out ShotResult
	if err := c.post(ctx, "/session/"+sessionID+"/screenshot", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Input(ctx context.Context, sessionID string, ev InputEvent) error {
	return c.post(ctx, "/session/"+sessionID+"/input", ev, nil)
}

func (c *Client) Close(ctx context.Context, sessionID string) error {
	return c.post(ctx, "/session/"+sessionID+"/close", map[string]any{}, nil)
}

// SubscribeScreencast opens the sidecar SSE stream and returns a channel of
// frames. The channel closes when ctx is cancelled or the stream ends. The
// caller owns ctx cancellation (used by the registry's relay goroutine,
// whose lifetime equals the browser session).
func (c *Client) SubscribeScreencast(ctx context.Context, sessionID string) (<-chan Frame, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/session/"+sessionID+"/screencast", nil)
	if err != nil {
		return nil, err
	}
	c.auth(req)
	req.Header.Set("Accept", "text/event-stream")
	// No client timeout for the long-lived stream.
	streamClient := &http.Client{}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("screencast subscribe: HTTP %d", resp.StatusCode)
	}

	out := make(chan Frame, 8)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), 4<<20) // frames are base64 JPEG, can be large
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue // event:/comment/blank lines
			}
			var f Frame
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &f); err != nil {
				continue
			}
			select {
			case out <- f:
			case <-ctx.Done():
				return
			default:
				// Drop frames a slow relay can't keep up with rather than
				// stall the stream — the next frame supersedes it anyway.
			}
		}
	}()
	return out, nil
}

// ── transport ──────────────────────────────────────────────────────────────

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error != "" {
			return fmt.Errorf("browser sidecar: %s", e.Error)
		}
		return fmt.Errorf("browser sidecar: HTTP %d", resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) auth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}
