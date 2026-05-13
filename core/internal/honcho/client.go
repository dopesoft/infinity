// Package honcho is a thin client for plastic-labs/honcho — an open-source
// dialectic peer-modelling system Infinity treats as a complement to its own
// memory store. Honcho derives "who the boss is" from interaction traces.
// Infinity's mem_* tables remain the source of truth for facts, provenance
// and audit; Honcho contributes a continually-updated peer representation
// the agent injects into the system prompt as user-context.
//
// Wire this in only when HONCHO_BASE_URL is set. Without it, the package
// returns a no-op client and the agent loop runs unchanged.
package honcho

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// HTTP client timeout — outer ceiling for any single request. The
	// per-call deadlines below are tighter; this just prevents a stuck
	// socket from leaking a goroutine for minutes if Honcho hangs.
	defaultTimeout = 30 * time.Second

	// postTimeout bounds a single PostMessage. Mirror writes happen on
	// every user/assistant turn via the hook pipeline; if Honcho is
	// slow we'd rather skip than queue up 30s-long goroutines that
	// spam `context deadline exceeded` in the logs and starve the
	// pipeline. Honcho's deriver runs async on its side, so dropping a
	// mirror just means the peer representation lags one turn.
	postTimeout = 4 * time.Second

	// askTimeout bounds a dialectic Ask. Synchronous in the read path
	// (provider wraps with an even tighter 2s ctx for the on-turn
	// fetch), but kept loose here for offline/admin callers.
	askTimeout = 10 * time.Second

	defaultPeerName  = "boss"
	defaultWorkspace = "infinity"
)

// Client talks to a Honcho deployment via its REST API. Configure with
// HONCHO_BASE_URL (e.g. https://honcho.your-domain.com) and HONCHO_API_KEY.
// HONCHO_WORKSPACE / HONCHO_PEER override the default identifiers.
type Client struct {
	base       string
	apiKey     string
	workspace  string
	peer       string
	httpClient *http.Client

	cacheMu  sync.RWMutex
	repCache string
	repAt    time.Time

	// Simple in-process circuit breaker. Honcho on Railway's free tier
	// can wedge for minutes at a time — when it does, every turn we'd
	// otherwise pay the 2-second read deadline AND log a 4-second hook
	// timeout on every user/assistant message. The breaker tracks
	// consecutive failures; once it trips, all calls short-circuit
	// returning the cached/empty value (no I/O, no logs) until the
	// cooldown elapses.
	cbMu              sync.Mutex
	consecutiveErrors int
	openUntil         time.Time
}

const (
	// Breaker trips after this many consecutive failures.
	breakerFailureThreshold = 3
	// Once tripped, calls short-circuit for this long before retrying.
	breakerOpenDuration = 5 * time.Minute
)

// FromEnv returns a Client when HONCHO_BASE_URL is set, otherwise nil. Callers
// should treat a nil return as "Honcho disabled" and not call any methods on
// it — every method already nil-guards but skipping the call avoids the
// allocations.
func FromEnv() *Client {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("HONCHO_BASE_URL")), "/")
	if base == "" {
		return nil
	}
	c := &Client{
		base:      base,
		apiKey:    strings.TrimSpace(os.Getenv("HONCHO_API_KEY")),
		workspace: envOr("HONCHO_WORKSPACE", defaultWorkspace),
		peer:      envOr("HONCHO_PEER", defaultPeerName),
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
	return c
}

// Enabled reports whether the client has a base URL.
func (c *Client) Enabled() bool { return c != nil && c.base != "" }

// Workspace, Peer expose the resolved identifiers for diagnostics.
func (c *Client) Workspace() string { if c == nil { return "" }; return c.workspace }
func (c *Client) Peer() string      { if c == nil { return "" }; return c.peer }

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// Message is a single observation pushed to Honcho. The peer-id identifies
// the speaker. session_id ties messages together so Honcho's reasoning
// pipeline can summarise per session as well as across.
type Message struct {
	PeerID    string `json:"peer_id"`
	SessionID string `json:"session_id,omitempty"`
	Content   string `json:"content"`
	Role      string `json:"role,omitempty"` // "user" | "assistant"
}

// honchoMessageBatch matches Honcho v3's POST messages body shape — an
// envelope wrapping a list of {peer_id, content, ...}.
type honchoMessageBatch struct {
	Messages []honchoMessage `json:"messages"`
}

type honchoMessage struct {
	PeerID  string `json:"peer_id"`
	Content string `json:"content"`
}

// breakerTripped reports whether the circuit is currently open. Open ==
// short-circuit all I/O until the cooldown elapses.
func (c *Client) breakerTripped() bool {
	c.cbMu.Lock()
	defer c.cbMu.Unlock()
	if c.openUntil.IsZero() {
		return false
	}
	if time.Now().Before(c.openUntil) {
		return true
	}
	// Cooldown elapsed — half-open: let the next request try, reset the
	// timestamps. If it fails we trip again immediately.
	c.openUntil = time.Time{}
	c.consecutiveErrors = 0
	return false
}

// markResult lets the breaker advance state based on whether the last
// call succeeded. Wrap every network call with this.
func (c *Client) markResult(err error) {
	c.cbMu.Lock()
	defer c.cbMu.Unlock()
	if err == nil {
		c.consecutiveErrors = 0
		c.openUntil = time.Time{}
		return
	}
	c.consecutiveErrors++
	if c.consecutiveErrors >= breakerFailureThreshold && c.openUntil.IsZero() {
		c.openUntil = time.Now().Add(breakerOpenDuration)
	}
}

// PostMessage writes a single observation to Honcho. v3 batches messages
// under sessions, so a session_id is required — when none is supplied we
// fall back to a default per-peer bucket so messages still land somewhere
// indexed for the deriver to pick up.
func (c *Client) PostMessage(ctx context.Context, m Message) error {
	if !c.Enabled() {
		return nil
	}
	// Breaker open — silently drop. The hook pipeline will see no error;
	// Honcho's deriver lags one turn (or many), Studio chat unaffected.
	if c.breakerTripped() {
		return nil
	}
	peerID := m.PeerID
	if peerID == "" {
		peerID = c.peer
	}
	sessionID := strings.TrimSpace(m.SessionID)
	if sessionID == "" {
		sessionID = "default"
	}
	url := fmt.Sprintf("%s/v3/workspaces/%s/sessions/%s/messages",
		c.base, c.workspace, sessionID)
	body, err := json.Marshal(honchoMessageBatch{Messages: []honchoMessage{{
		PeerID:  peerID,
		Content: m.Content,
	}}})
	if err != nil {
		return err
	}
	// Tight per-call deadline so a slow/dead Honcho can't park hook
	// goroutines for the full http.Client timeout. The hook pipeline
	// fires this on every turn — dropping a mirror is preferable to
	// 30s of log spam and goroutine pileup.
	postCtx, cancel := context.WithTimeout(ctx, postTimeout)
	defer cancel()
	err = c.do(postCtx, http.MethodPost, url, body, nil)
	c.markResult(err)
	return err
}

// DialecticQuery asks Honcho's reasoning endpoint a natural-language
// question about the peer ("what does the boss prefer when X?") and returns
// the LLM-generated answer. Used by the memory provider to fold a peer
// snapshot into the system prompt.
type DialecticQuery struct {
	Queries string `json:"queries"`
}

type DialecticResponse struct {
	Content string `json:"content"`
}

// Ask runs a dialectic chat query against the configured peer. POST to
// /v3/workspaces/<ws>/peers/<peer>/chat returns the LLM-curated answer
// over the peer's accumulated representation.
func (c *Client) Ask(ctx context.Context, q string) (string, error) {
	if !c.Enabled() {
		return "", nil
	}
	path := envOr("HONCHO_DIALECTIC_PATH",
		fmt.Sprintf("/v3/workspaces/%s/peers/%s/chat", c.workspace, c.peer))
	url := c.base + path
	body, _ := json.Marshal(DialecticQuery{Queries: q})
	var resp DialecticResponse
	askCtx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()
	if err := c.do(askCtx, http.MethodPost, url, body, &resp); err != nil {
		return "", err
	}
	return resp.Content, nil
}

// Representation returns the cached peer context, refreshing it from
// Honcho if older than ttl. Used by the memory provider on every system
// prefix build — keeps the representation hot while bounding traffic.
//
// Honcho v3 splits this across /representation (POST, curated subset) and
// /context (GET, peer-card + representation rolled into one). We use
// /context because it's the lightest fetch and matches the "fold the
// boss-state into the system prompt" use case best.
func (c *Client) Representation(ctx context.Context, ttl time.Duration) (string, error) {
	if !c.Enabled() {
		return "", nil
	}
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	c.cacheMu.RLock()
	cached, at := c.repCache, c.repAt
	c.cacheMu.RUnlock()
	if cached != "" && time.Since(at) < ttl {
		return cached, nil
	}
	// Breaker open — return whatever's cached (possibly empty) without
	// hitting the wire. The on-turn read path treats "" as "no peer
	// representation this turn" and continues with Infinity's own RRF.
	if c.breakerTripped() {
		return cached, nil
	}

	path := envOr("HONCHO_REPRESENTATION_PATH",
		fmt.Sprintf("/v3/workspaces/%s/peers/%s/context", c.workspace, c.peer))
	url := c.base + path
	var raw struct {
		Representation string `json:"representation"`
		Card           string `json:"card,omitempty"`
		Summary        string `json:"summary,omitempty"`
		Content        string `json:"content,omitempty"` // fallback for shape drift
	}
	err := c.do(ctx, http.MethodGet, url, nil, &raw)
	c.markResult(err)
	if err != nil {
		return cached, err // return last good if refresh fails
	}
	picked := strings.TrimSpace(raw.Representation)
	if picked == "" {
		picked = strings.TrimSpace(raw.Summary)
	}
	if picked == "" {
		picked = strings.TrimSpace(raw.Content)
	}
	c.cacheMu.Lock()
	c.repCache = picked
	c.repAt = time.Now()
	c.cacheMu.Unlock()
	return picked, nil
}

func (c *Client) do(ctx context.Context, method, url string, body []byte, decodeInto any) error {
	if !c.Enabled() {
		return errors.New("honcho: client disabled")
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("honcho %s %s: %d %s", method, url, resp.StatusCode, string(raw))
	}
	if decodeInto == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(decodeInto)
}
