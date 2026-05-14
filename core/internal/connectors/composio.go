// Package connectors — Composio REST execute client.
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
// and tracing — none of which a cron poll needs. We keep this thin on
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
// Gmail/Calendar action does). UserID may be left empty — Composio defaults
// to the user the connected_account belongs to.
type ExecuteRequest struct {
	Slug               string         `json:"-"` // path param, not body
	ConnectedAccountID string         `json:"connected_account_id,omitempty"`
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
// from Composio with Successful=false is NOT treated as a Go error — the
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
