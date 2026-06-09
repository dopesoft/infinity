// Package bridge abstracts "where does a filesystem / bash / git op
// actually run?" so the rest of Core never hardcodes Mac-vs-Cloud.
//
// Two implementations today:
//
//   MacBridge   - talks to the home Mac over Cloudflare Tunnel. Claude
//                 Code MCP is alive there too, which means Mac-bridge
//                 sessions get the bonus of Anthropic-Max-billed
//                 sub-agent edits via the claude_code__* tools.
//
//   CloudBridge - talks to the docker/workspace service on Railway
//                 private net. No sub-agent - Jarvis (whose cognition
//                 is ChatGPT subscription via openai_oauth) is the
//                 only brain. Primitives only.
//
// Both expose the same HTTP-ish API surface:
//
//   GET    /health
//   POST   /fs/save        {path, content}
//   POST   /fs/edit        {path, old_string, new_string, replace_all?}
//   GET    /fs/read?path=&start=&end=
//   GET    /fs/ls?path=
//   POST   /bash           {cmd, cwd?, timeout_sec?}
//   GET    /git/status?repo=
//   GET    /git/diff?repo=&path=&staged=
//   POST   /git/stage      {repo, files?}
//   POST   /git/commit     {repo, message}
//   POST   /git/push       {repo, remote?, branch?}
//   POST   /git/pull       {repo, remote?, branch?}
//
// The Router picks which one to use per session based on
// mem_sessions.bridge_preference + cached health. Default behaviour:
// Mac first, fall through to Cloud when Mac /health is unreachable.
package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Kind is the canonical identifier for a Bridge implementation.
type Kind string

const (
	KindMac   Kind = "mac"
	KindCloud Kind = "cloud"
)

// Bridge is the contract Core consumes. Implementations are responsible
// for their own auth (Cloudflare Access for Mac, bearer token for
// Cloud). Returns (body, status, ok). ok=false means the bridge is
// unreachable / misconfigured - the caller can decide whether to fall
// back to the other bridge or surface an error.
type Bridge interface {
	Name() Kind
	BaseURL() string
	Health(ctx context.Context) bool
	Get(ctx context.Context, path string) ([]byte, int, bool)
	Post(ctx context.Context, path string, body any) ([]byte, int, bool)
}

// ── shared HTTP plumbing ────────────────────────────────────────────────

var sharedClient = &http.Client{Timeout: 60 * time.Second}

func doRequest(ctx context.Context, method, url string, body any, hdr http.Header) ([]byte, int, bool) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, 0, false
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, 0, false
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := sharedClient.Do(req)
	if err != nil {
		return nil, 0, false
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, resp.StatusCode, false
	}
	return out, resp.StatusCode, true
}

// ── MacBridge ────────────────────────────────────────────────────────────

// MacBridge talks to the home-Mac bridge over Cloudflare Tunnel. The
// canonical Anthropic-Max-billed sub-agent path also lives on the Mac
// (Claude Code MCP); when this bridge is the active one for a session,
// Jarvis's system prompt overlay surfaces the claude_code__* toolset
// as available.
type MacBridge struct {
	base    string
	headers http.Header
}

func NewMacBridge(baseURL, cfClientID, cfClientSecret string) *MacBridge {
	hdr := http.Header{}
	if cfClientID != "" {
		hdr.Set("CF-Access-Client-Id", cfClientID)
	}
	if cfClientSecret != "" {
		hdr.Set("CF-Access-Client-Secret", cfClientSecret)
	}
	return &MacBridge{
		base:    strings.TrimSuffix(strings.TrimSuffix(baseURL, "/sse"), "/"),
		headers: hdr,
	}
}

func (m *MacBridge) Name() Kind      { return KindMac }
func (m *MacBridge) BaseURL() string { return m.base }

func (m *MacBridge) Health(ctx context.Context) bool {
	if m.base == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, status, ok := doRequest(ctx, http.MethodGet, m.base+"/health", nil, m.headers)
	// "Reachable" — not "200 on /health". Some Mac tunnels route /sse to the
	// MCP proxy but never wire /health to the bridge binary, so /health 404s
	// while /fs /bash /git work fine. A 404/4xx means the server ANSWERED (it's
	// up); only a transport failure or a 5xx means it's actually down. The
	// Router.Call failover is the real safety net if a specific op then 5xxs.
	return ok && status < 500
}

func (m *MacBridge) Get(ctx context.Context, path string) ([]byte, int, bool) {
	if m.base == "" {
		return nil, 0, false
	}
	return doRequest(ctx, http.MethodGet, m.base+path, nil, m.headers)
}

func (m *MacBridge) Post(ctx context.Context, path string, body any) ([]byte, int, bool) {
	if m.base == "" {
		return nil, 0, false
	}
	return doRequest(ctx, http.MethodPost, m.base+path, body, m.headers)
}

// ── CloudBridge ──────────────────────────────────────────────────────────

// CloudBridge talks to the docker/workspace service over Railway's
// private network. Bearer-token auth (WORKSPACE_BRIDGE_TOKEN). The
// Cloud bridge has no sub-agent - primitives only. Jarvis does all
// cognition via ChatGPT subscription OAuth.
type CloudBridge struct {
	base    string
	headers http.Header
}

func NewCloudBridge(baseURL, bearerToken string) *CloudBridge {
	hdr := http.Header{}
	if bearerToken != "" {
		hdr.Set("Authorization", "Bearer "+bearerToken)
	}
	return &CloudBridge{
		base:    strings.TrimSuffix(baseURL, "/"),
		headers: hdr,
	}
}

func (c *CloudBridge) Name() Kind      { return KindCloud }
func (c *CloudBridge) BaseURL() string { return c.base }

func (c *CloudBridge) Health(ctx context.Context) bool {
	if c.base == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second) // wake from sleep can take a beat
	defer cancel()
	_, status, ok := doRequest(ctx, http.MethodGet, c.base+"/health", nil, nil)
	return ok && status >= 200 && status < 300
}

func (c *CloudBridge) Get(ctx context.Context, path string) ([]byte, int, bool) {
	if c.base == "" {
		return nil, 0, false
	}
	return doRequest(ctx, http.MethodGet, c.base+path, nil, c.headers)
}

func (c *CloudBridge) Post(ctx context.Context, path string, body any) ([]byte, int, bool) {
	if c.base == "" {
		return nil, 0, false
	}
	return doRequest(ctx, http.MethodPost, c.base+path, body, c.headers)
}

// ── Router ───────────────────────────────────────────────────────────────

// Preference reflects mem_sessions.bridge_preference. "auto" means
// "prefer mac when healthy, fall back to cloud."
type Preference string

const (
	PrefAuto  Preference = "auto"
	PrefMac   Preference = "mac"
	PrefCloud Preference = "cloud"
)

// Status is a snapshot of which bridges are reachable RIGHT NOW. Cached
// per Router for short windows so per-call health checks don't burn
// 60ms apiece.
type Status struct {
	MacHealthy   bool      `json:"mac_healthy"`
	CloudHealthy bool      `json:"cloud_healthy"`
	MacURL       string    `json:"mac_url,omitempty"`
	CloudURL     string    `json:"cloud_url,omitempty"`
	CheckedAt    time.Time `json:"checked_at"`
}

// Router routes per-session calls to the right bridge. Health is cached
// for healthTTL so a burst of tool calls doesn't trigger N probes.
type Router struct {
	mac      Bridge
	cloud    Bridge
	healthTTL time.Duration

	mu        sync.RWMutex
	cached    Status
	cachedExp time.Time
}

func NewRouter(mac, cloud Bridge) *Router {
	return &Router{
		mac:       mac,
		cloud:     cloud,
		healthTTL: 5 * time.Second,
	}
}

// MacBridgeHealthy returns true when the Mac bridge is currently
// reachable. Lightweight accessor over the cached Status - safe to
// call from healers / detectors that want to filter out claude_code__*
// failures while the bridge is offline (those failures are expected
// when the boss is on cloud, and re-emitting "fs_read failed N times"
// curiosity questions for them is just noise). Nil Router returns
// false; nil mac bridge returns false.
func (r *Router) MacBridgeHealthy() bool {
	if r == nil || r.mac == nil {
		return false
	}
	st := r.Refresh(context.Background())
	return st.MacHealthy
}

// For returns the bridge that should serve a session given its
// preference. Returns (bridge, why, error). Why is a short reason
// string suitable for surfacing to the agent's system prompt.
func (r *Router) For(ctx context.Context, pref Preference) (Bridge, string, error) {
	st := r.Refresh(ctx)
	switch pref {
	case PrefMac:
		if !st.MacHealthy {
			return nil, "mac bridge offline (session pinned to 'mac')", errors.New("mac bridge offline")
		}
		return r.mac, "mac (pinned)", nil
	case PrefCloud:
		if !st.CloudHealthy {
			return nil, "cloud bridge offline (session pinned to 'cloud')", errors.New("cloud bridge offline")
		}
		return r.cloud, "cloud (pinned)", nil
	default: // PrefAuto
		if st.MacHealthy {
			return r.mac, "mac (auto - preferred when up)", nil
		}
		if st.CloudHealthy {
			return r.cloud, "cloud (auto - mac offline, fell back)", nil
		}
		return nil, "both bridges offline", errors.New("no bridge available")
	}
}

// Refresh returns the cached status, probing both bridges if the cache
// has expired. Cheap to call repeatedly.
func (r *Router) Refresh(ctx context.Context) Status {
	r.mu.RLock()
	if time.Now().Before(r.cachedExp) {
		st := r.cached
		r.mu.RUnlock()
		return st
	}
	r.mu.RUnlock()

	// Probe both in parallel.
	var (
		macUp   bool
		cloudUp bool
		wg      sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		if r.mac != nil {
			macUp = r.mac.Health(ctx)
		}
	}()
	go func() {
		defer wg.Done()
		if r.cloud != nil {
			cloudUp = r.cloud.Health(ctx)
		}
	}()
	wg.Wait()

	st := Status{
		MacHealthy:   macUp,
		CloudHealthy: cloudUp,
		CheckedAt:    time.Now().UTC(),
	}
	if r.mac != nil {
		st.MacURL = r.mac.BaseURL()
	}
	if r.cloud != nil {
		st.CloudURL = r.cloud.BaseURL()
	}

	r.mu.Lock()
	r.cached = st
	r.cachedExp = time.Now().Add(r.healthTTL)
	r.mu.Unlock()
	return st
}

// Snapshot returns the cached Status without probing.
func (r *Router) Snapshot() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cached
}

// Invalidate forces the next Refresh to re-probe. Useful after a
// known event (a session's preference changed, a manual UI refresh).
func (r *Router) Invalidate() {
	r.mu.Lock()
	r.cachedExp = time.Time{}
	r.mu.Unlock()
}

// ErrBothBridgesDown is returned by Call when the preferred bridge AND its
// alternate both fail at the bridge level (transport error or HTTP 5xx). The
// caller surfaces this as a "can't run, no bridge" event the agent must stop
// on — NOT a per-tool error it would retry to the iteration cap.
var ErrBothBridgesDown = errors.New("both bridges unavailable")

// alternate returns the other bridge of the mac/cloud pair, or nil if there
// isn't one configured.
func (r *Router) alternate(b Bridge) Bridge {
	if r == nil || b == nil {
		return nil
	}
	if b == r.mac {
		return r.cloud
	}
	if b == r.cloud {
		return r.mac
	}
	return nil
}

// Call runs fn against the preferred bridge and, on a BRIDGE-level failure,
// automatically retries fn once on the other bridge. This is the failover that
// keeps an autonomous run alive when the Mac flakes mid-run: a 3am self-improve
// pass that loses the Mac steps straight onto the cloud workspace instead of
// getting stuck (the night-after-night bug).
//
// What counts as a BRIDGE failure (→ fail over): a transport error (ok=false)
// or an HTTP 5xx. What does NOT (→ returned as-is, no failover): any 2xx, and
// any 4xx — a 4xx is the bridge answering with a command/param result (e.g. 400
// "repo required", a `go build` that compiled-and-failed), so retrying on the
// other bridge would just double the work and mask the real error.
//
// Returns the bridge that served the final attempt, its response, whether a
// failover happened (so the caller can annotate the result), and an error only
// when NO bridge could be reached (ErrBothBridgesDown).
func (r *Router) Call(ctx context.Context, pref Preference, fn func(Bridge) ([]byte, int, bool)) (served Bridge, body []byte, status int, failedOver bool, err error) {
	if r == nil {
		return nil, nil, 0, false, errors.New("bridge router not configured")
	}
	primary, _, perr := r.For(ctx, pref)
	if perr != nil {
		return nil, nil, 0, false, perr
	}
	body, status, ok := fn(primary)
	if ok && status < 500 {
		return primary, body, status, false, nil
	}
	// Primary failed at the bridge level. Try the alternate once.
	alt := r.alternate(primary)
	if alt == nil {
		return primary, body, status, false, ErrBothBridgesDown
	}
	// The primary's failure is fresh evidence its health is stale — re-probe so
	// the system prompt / next call reflect reality.
	r.Invalidate()
	abody, astatus, aok := fn(alt)
	if aok && astatus < 500 {
		return alt, abody, astatus, true, nil
	}
	// Both bridges failed at the bridge level.
	return alt, abody, astatus, true, ErrBothBridgesDown
}

// Describe is a short human/agent-readable summary of router state.
// Surfaces in the system prompt overlay so Jarvis always knows where
// his tools land. Format intentionally compact.
func (r *Router) Describe(active Bridge, pref Preference) string {
	st := r.Snapshot()
	parts := []string{}
	if active != nil {
		parts = append(parts, fmt.Sprintf("active=%s", active.Name()))
	}
	parts = append(parts, fmt.Sprintf("pref=%s", pref))
	parts = append(parts, fmt.Sprintf("mac=%s", healthyText(st.MacHealthy)))
	parts = append(parts, fmt.Sprintf("cloud=%s", healthyText(st.CloudHealthy)))
	return strings.Join(parts, " · ")
}

func healthyText(up bool) string {
	if up {
		return "up"
	}
	return "down"
}
