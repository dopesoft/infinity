// Package connectors - Composio REST execute client.
//
// Counterpart to cache.go (which lists connected accounts). This file
// exposes the *action* surface: pick an action slug, point it at a
// connected_account_id, hand in arguments → get the upstream response.
//
// The agent already reaches Composio actions via MCP. This Go-side
// client exists so deterministic flows (cron polling, sentinels,
// skill runtimes) can call the same actions WITHOUT booting the LLM.
//
// Endpoint: POST https://backend.composio.dev/api/v3/tools/execute/{slug}
//   Headers: x-api-key + Authorization: Bearer …
//   Body:    { "connected_account_id": "ca_...", "user_id": "...",
//              "arguments": {...} }
//
// The full SDK supports custom_auth_params, modifiers, file handling,
// and tracing - none of which a cron poll needs. We keep this thin on
// purpose; expand it when a concrete caller actually needs the extra
// surface area.

package connectors

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

const composioExecuteBase = "https://backend.composio.dev/api/v3"

// ExecuteRequest is what callers hand to Client.Execute. ConnectedAccountID
// is required for any action that hits an authenticated upstream (every
// Gmail/Calendar action does). UserID may be left empty - Composio defaults
// to the user the connected_account belongs to.
type ExecuteRequest struct {
	Slug               string         `json:"-"` // path param, not body
	ConnectedAccountID string         `json:"connected_account_id,omitempty"`
	EntityID           string         `json:"entity_id,omitempty"`
	UserID             string         `json:"user_id,omitempty"`
	Arguments          map[string]any `json:"arguments,omitempty"`
}

// ExecuteResponse mirrors Composio's tools.execute envelope. We keep
// `Data` as a raw json.RawMessage so callers can decode it into the
// action-specific shape (gmail messages, calendar events, etc.) without
// us pre-defining every payload.
type ExecuteResponse struct {
	Successful bool            `json:"successful"`
	Error      string          `json:"error,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	LogID      string          `json:"log_id,omitempty"`
	SessionInfo any            `json:"session_info,omitempty"`
}

// ExecuteClient calls Composio's tools.execute endpoint.
//
// Unlike cache.New which takes a key getter, this client takes a getter too
// so a Railway env hot-swap propagates without restart. Same admin-key
// preference as connectors_api.go: COMPOSIO_ADMIN_API_KEY first, fallback
// to COMPOSIO_API_KEY.
type ExecuteClient struct {
	keyFn      func() string
	httpClient *http.Client
	baseURL    string
}

// NewExecuteClient builds the client. The key function is invoked on every
// call so a `railway variables --set` propagates without a process restart
// (mirrors the pattern in cache.go / connectors_api.go).
func NewExecuteClient(keyFn func() string) *ExecuteClient {
	return &ExecuteClient{
		keyFn:      keyFn,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    composioExecuteBase,
	}
}

// Execute fires one POST /api/v3/tools/execute/{slug} call. Returns the
// parsed envelope OR a transport/decoding error. A successful response
// from Composio with Successful=false is NOT treated as a Go error - the
// caller inspects `resp.Successful` + `resp.Error` and decides whether
// to retry, surface, or capture.
func (c *ExecuteClient) Execute(ctx context.Context, req ExecuteRequest) (*ExecuteResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("composio execute client not configured")
	}
	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		return nil, fmt.Errorf("execute: action slug required")
	}
	key := strings.TrimSpace(c.keyFn())
	if key == "" {
		return nil, fmt.Errorf("execute: no Composio API key (set COMPOSIO_ADMIN_API_KEY)")
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("execute: marshal: %w", err)
	}
	url := c.baseURL + "/tools/execute/" + slug
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("execute: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", key)
	httpReq.Header.Set("Authorization", "Bearer "+key)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute: do: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("composio execute %s: %d %s", slug, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out ExecuteResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("execute: decode: %w (body=%q)", err, truncate(string(raw), 200))
	}
	return &out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ── Proxy: raw HTTP through Composio's auth layer ────────────────────────────
//
// Composio's POST /api/v3/tools/proxy lets us call ANY upstream HTTP
// endpoint with the connected_account's OAuth token automatically
// attached + refreshed server-side. We use this for the native Google
// Calendar sync so the agent never touches the access token directly
// (Composio holds it) AND we still get the full Google API surface,
// including events.list?syncToken which their packaged
// GOOGLECALENDAR_EVENTS_LIST action does not expose.
//
// Per CLAUDE.md privacy invariant: no token ever crosses into our
// logs - Composio refreshes, we just see the upstream JSON response.

// ProxyRequest mirrors Composio's tools.proxy v3 body shape. The
// `Parameters` slice carries query-string + path params; `Body` is
// the raw JSON body for write methods. Everything optional except
// ConnectedAccountID + Endpoint + Method.
type ProxyRequest struct {
	ConnectedAccountID string         `json:"connected_account_id"`
	Endpoint           string         `json:"endpoint"`           // upstream path, e.g. "/calendar/v3/calendars/primary/events"
	Method             string         `json:"method"`             // "GET" | "POST" | "PATCH" | "PUT" | "DELETE"
	Parameters         []ProxyParam   `json:"parameters,omitempty"`
	Body               map[string]any `json:"body,omitempty"`
}

// ProxyParam: one query, path, or header parameter. In = "query" | "path"
// | "header". Composio templates path params into Endpoint by name.
type ProxyParam struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	In    string `json:"in"` // "query" | "path" | "header"
}

// ProxyResponse: Composio's envelope around the upstream call. Successful
// reflects HTTP <400 on the upstream. Data carries the raw upstream JSON
// response so the caller decodes it into its own shape (Google Calendar
// event lists, etc.).
type ProxyResponse struct {
	Successful bool            `json:"successful"`
	Status     int             `json:"status,omitempty"`
	Error      string          `json:"error,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	Headers    map[string]any  `json:"headers,omitempty"`
}

// Proxy fires a single tools.proxy call. Returns the parsed envelope OR
// a transport-level error. Upstream non-2xx becomes Successful=false +
// Error populated; that's a domain error for the caller, not a Go error.
func (c *ExecuteClient) Proxy(ctx context.Context, req ProxyRequest) (*ProxyResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("composio execute client not configured")
	}
	if strings.TrimSpace(req.ConnectedAccountID) == "" {
		return nil, fmt.Errorf("proxy: connected_account_id required")
	}
	if strings.TrimSpace(req.Endpoint) == "" {
		return nil, fmt.Errorf("proxy: endpoint required")
	}
	if strings.TrimSpace(req.Method) == "" {
		req.Method = http.MethodGet
	}
	key := strings.TrimSpace(c.keyFn())
	if key == "" {
		return nil, fmt.Errorf("proxy: no Composio API key (set COMPOSIO_ADMIN_API_KEY)")
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("proxy: marshal: %w", err)
	}
	url := c.baseURL + "/tools/proxy"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("proxy: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", key)
	httpReq.Header.Set("Authorization", "Bearer "+key)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("proxy: do: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("composio proxy %s %s: %d %s", req.Method, req.Endpoint, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out ProxyResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("proxy: decode: %w (body=%q)", err, truncate(string(raw), 200))
	}
	return &out, nil
}