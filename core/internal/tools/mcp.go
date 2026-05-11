// Package tools — MCP client wraps modelcontextprotocol/go-sdk and exposes
// each remote tool as a Tool in the central registry under the namespace
// "<server>.<tool>".
package tools

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dopesoft/infinity/core/config"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPServerConfig struct {
	Name         string   `yaml:"name"`
	Transport    string   `yaml:"transport"`
	Enabled      bool     `yaml:"enabled"`
	Command      []string `yaml:"command"`
	URL          string   `yaml:"url"`
	URLEnv       string   `yaml:"url_env"`
	// Auth: "bearer" | "cloudflare_access" | "" (none).
	Auth string `yaml:"auth"`
	// For auth=bearer: name of the env var holding the token.
	AuthTokenEnv string `yaml:"auth_token_env"`
	// For auth=cloudflare_access: env var names for the Service Token pair.
	CFClientIDEnv     string `yaml:"cf_client_id_env"`
	CFClientSecretEnv string `yaml:"cf_client_secret_env"`
}

// resolveURL prefers an explicit url; otherwise reads from $url_env. Returns
// empty if neither is set so the caller can fail with a clear message.
func (s MCPServerConfig) resolveURL() string {
	if v := strings.TrimSpace(s.URL); v != "" {
		return v
	}
	if s.URLEnv != "" {
		return strings.TrimSpace(os.Getenv(s.URLEnv))
	}
	return ""
}

// resolveAuthHeaders builds the headers map to attach to outbound MCP HTTP
// requests, based on the configured auth mode. Returns nil headers and nil
// error when no auth is configured.
func (s MCPServerConfig) resolveAuthHeaders() (map[string]string, error) {
	mode := strings.ToLower(strings.TrimSpace(s.Auth))
	if mode == "" || mode == "none" {
		return nil, nil
	}
	switch mode {
	case "bearer":
		if s.AuthTokenEnv == "" {
			return nil, fmt.Errorf("auth=bearer requires auth_token_env")
		}
		tok := strings.TrimSpace(os.Getenv(s.AuthTokenEnv))
		if tok == "" {
			return nil, fmt.Errorf("auth=bearer but $%s is empty", s.AuthTokenEnv)
		}
		return map[string]string{"Authorization": "Bearer " + tok}, nil
	case "cloudflare_access":
		if s.CFClientIDEnv == "" || s.CFClientSecretEnv == "" {
			return nil, fmt.Errorf("auth=cloudflare_access requires cf_client_id_env and cf_client_secret_env")
		}
		id := strings.TrimSpace(os.Getenv(s.CFClientIDEnv))
		secret := strings.TrimSpace(os.Getenv(s.CFClientSecretEnv))
		if id == "" || secret == "" {
			return nil, fmt.Errorf("auth=cloudflare_access but $%s or $%s is empty", s.CFClientIDEnv, s.CFClientSecretEnv)
		}
		return map[string]string{
			"CF-Access-Client-Id":     id,
			"CF-Access-Client-Secret": secret,
		}, nil
	default:
		return nil, fmt.Errorf("unknown auth mode %q (want bearer | cloudflare_access)", s.Auth)
	}
}

// headerRoundTripper injects a static set of headers on every outbound
// request. Used to authenticate to MCP servers behind a reverse proxy:
//   - auth=bearer            sets Authorization: Bearer <token>
//   - auth=cloudflare_access sets CF-Access-Client-Id + CF-Access-Client-Secret
type headerRoundTripper struct {
	headers map[string]string
	base    http.RoundTripper
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	for k, v := range h.headers {
		r.Header.Set(k, v)
	}
	rt := h.base
	if rt == nil {
		rt = http.DefaultTransport
	}
	return rt.RoundTrip(r)
}

type MCPConfig struct {
	Servers []MCPServerConfig `yaml:"servers"`
}

func LoadMCPConfig(path string) (*MCPConfig, error) {
	if path == "" {
		path = os.Getenv("MCP_CONFIG")
	}
	// Explicit path? Use it strictly. Empty? Try the on-disk well-known
	// location for local-dev, then fall back to the embedded copy that
	// ships in the binary so Railway (distroless, no source tree) still
	// gets the canonical config.
	var data []byte
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		data = raw
	} else {
		raw, err := os.ReadFile("core/config/mcp.yaml")
		switch {
		case err == nil:
			data = raw
		case errors.Is(err, os.ErrNotExist):
			data = config.MCPYAML // embedded fallback
		default:
			return nil, err
		}
	}
	if len(data) == 0 {
		return &MCPConfig{}, nil
	}
	var cfg MCPConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// MCPManager owns the MCP client sessions and registers their tools.
//
// Sessions can die silently — Cloudflare Tunnel idle-timeouts, mac-bridge
// restarts, or transient network blips drop the long-lived SSE stream and
// the underlying transport surfaces EOF only on the next call. We keep the
// original server config around so `Reconnect` can re-dial the same server
// and swap the session in place. Tools dispatch through the manager rather
// than holding a direct *mcp.ClientSession pointer, so a reconnect is
// transparent to callers.
type MCPManager struct {
	mu         sync.Mutex
	sessions   map[string]*mcp.ClientSession
	configs    map[string]MCPServerConfig
	statuses   map[string]MCPStatus
	reconnects map[string]*sync.Mutex
}

type MCPStatus struct {
	Name      string    `json:"name"`
	Connected bool      `json:"connected"`
	Tools     []string  `json:"tools"`
	Error     string    `json:"error,omitempty"`
	Tested    time.Time `json:"tested"`
}

func NewMCPManager() *MCPManager {
	return &MCPManager{
		sessions:   make(map[string]*mcp.ClientSession),
		configs:    make(map[string]MCPServerConfig),
		statuses:   make(map[string]MCPStatus),
		reconnects: make(map[string]*sync.Mutex),
	}
}

func (m *MCPManager) Statuses() []MCPStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MCPStatus, 0, len(m.statuses))
	for _, s := range m.statuses {
		out = append(out, s)
	}
	return out
}

// Connect connects all enabled servers from the config and registers their
// discovered tools into the provided registry.
func (m *MCPManager) Connect(ctx context.Context, cfg *MCPConfig, registry *Registry) error {
	if cfg == nil {
		return nil
	}
	for _, s := range cfg.Servers {
		if !s.Enabled {
			continue
		}
		if err := m.connectOne(ctx, s, registry); err != nil {
			m.recordStatus(MCPStatus{Name: s.Name, Connected: false, Error: err.Error(), Tested: time.Now().UTC()})
			fmt.Fprintf(os.Stderr, "mcp: %s failed: %v\n", s.Name, err)
			continue
		}
	}
	return nil
}

// dialSession opens a fresh MCP session for the given server. It performs
// the connect handshake only — listing tools and registering them is left
// to the caller so this can be reused by Reconnect.
func (m *MCPManager) dialSession(ctx context.Context, s MCPServerConfig) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "infinity-core", Version: "0.1.0"}, nil)

	var transport mcp.Transport
	switch s.Transport {
	case "stdio":
		if len(s.Command) == 0 {
			return nil, fmt.Errorf("stdio transport needs command")
		}
		transport = &mcp.CommandTransport{Command: exec.Command(s.Command[0], s.Command[1:]...)}
	case "sse":
		url := s.resolveURL()
		if url == "" {
			return nil, fmt.Errorf("sse transport needs url or url_env")
		}
		sse := &mcp.SSEClientTransport{Endpoint: url}
		headers, err := s.resolveAuthHeaders()
		if err != nil {
			return nil, err
		}
		if len(headers) > 0 {
			// CRITICAL: do NOT set http.Client.Timeout for SSE. That
			// timeout covers the *entire* request including reading the
			// response body — for SSE the body is a long-lived stream,
			// so any Timeout > 0 will kill the connection after N seconds
			// and surface as "client is closing: EOF" on the next call.
			// Connection establishment is bounded by the underlying
			// Transport (DialContext, TLS handshake, response headers).
			// Per-call deadlines are enforced via context.WithTimeout in
			// callers like keepAlive and CallTool.
			httpTransport := http.DefaultTransport.(*http.Transport).Clone()
			httpTransport.ResponseHeaderTimeout = 30 * time.Second
			sse.HTTPClient = &http.Client{
				Transport: &headerRoundTripper{
					headers: headers,
					base:    httpTransport,
				},
			}
		}
		transport = sse
	default:
		return nil, fmt.Errorf("unknown transport: %s", s.Transport)
	}

	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return sess, nil
}

func (m *MCPManager) connectOne(ctx context.Context, s MCPServerConfig, registry *Registry) error {
	sess, err := m.dialSession(ctx, s)
	if err != nil {
		return err
	}

	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	listed, err := sess.ListTools(listCtx, nil)
	if err != nil {
		_ = sess.Close()
		return fmt.Errorf("list tools: %w", err)
	}

	toolNames := make([]string, 0, len(listed.Tools))
	for _, t := range listed.Tools {
		// Anthropic's tool name regex is ^[a-zA-Z0-9_-]{1,128}$ — no dots.
		// Use a double underscore as the namespace separator and sanitise
		// each side so MCP servers with hyphenated/dotted names still work.
		name := sanitiseToolName(s.Name) + "__" + sanitiseToolName(t.Name)
		desc := t.Description
		schema := mapFromAny(t.InputSchema)
		// Tools dispatch through the manager (by server name) rather than
		// holding a raw session pointer, so Reconnect can swap sessions
		// without re-registering tools.
		registry.Register(&mcpTool{
			name:   name,
			desc:   desc,
			schema: schema,
			mgr:    m,
			server: s.Name,
			remote: t.Name,
		})
		toolNames = append(toolNames, name)
	}

	m.mu.Lock()
	m.sessions[s.Name] = sess
	m.configs[s.Name] = s
	if _, ok := m.reconnects[s.Name]; !ok {
		m.reconnects[s.Name] = &sync.Mutex{}
	}
	m.mu.Unlock()
	m.recordStatus(MCPStatus{Name: s.Name, Connected: true, Tools: toolNames, Tested: time.Now().UTC()})
	fmt.Printf("mcp: %s connected (%d tools)\n", s.Name, len(toolNames))

	// SSE sessions over a reverse proxy (Cloudflare Tunnel etc) get reaped
	// after ~100s of idle traffic. A cheap ListTools every 45s keeps the
	// stream warm so the next user-triggered CallTool doesn't have to pay
	// for a reconnect. We do this only for SSE; stdio is process-local.
	if s.Transport == "sse" {
		go m.keepAlive(s.Name)
	}
	return nil
}

// keepAlive pings the named MCP session every 45s with a lightweight
// ListTools call. On error we trigger a Reconnect and keep going so the
// loop is self-healing — without this the first real tool call after an
// idle stretch always fails with "client is closing: EOF".
func (m *MCPManager) keepAlive(server string) {
	ticker := time.NewTicker(45 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		sess := m.getSession(server)
		if sess == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := sess.ListTools(ctx, nil)
		cancel()
		if err != nil && isTransportDeadErr(err) {
			log.Printf("mcp: %s keepalive failed (%v) — reconnecting", server, err)
			rctx, rcancel := context.WithTimeout(context.Background(), 20*time.Second)
			if rerr := m.Reconnect(rctx, server); rerr != nil {
				log.Printf("mcp: %s keepalive reconnect failed: %v", server, rerr)
			}
			rcancel()
		}
	}
}

// getSession returns the current session for a server, or nil if absent.
func (m *MCPManager) getSession(server string) *mcp.ClientSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[server]
}

// Reconnect re-dials the given server and swaps in a fresh session. The
// per-server reconnect mutex prevents thundering-herd: many concurrent
// tool calls failing on the same dead session will queue here and only
// the first does the actual reconnect; the rest pick up the new session.
func (m *MCPManager) Reconnect(ctx context.Context, server string) error {
	m.mu.Lock()
	cfg, ok := m.configs[server]
	rcm := m.reconnects[server]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("mcp: no config for server %q", server)
	}
	if rcm == nil {
		rcm = &sync.Mutex{}
		m.mu.Lock()
		m.reconnects[server] = rcm
		m.mu.Unlock()
	}
	rcm.Lock()
	defer rcm.Unlock()

	// Best-effort close of the old session before re-dialling.
	m.mu.Lock()
	old := m.sessions[server]
	m.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}

	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	sess, err := m.dialSession(dialCtx, cfg)
	if err != nil {
		m.recordStatus(MCPStatus{Name: server, Connected: false, Error: err.Error(), Tested: time.Now().UTC()})
		return err
	}
	m.mu.Lock()
	m.sessions[server] = sess
	m.mu.Unlock()
	// Preserve the previously-discovered tool list in the status; we don't
	// re-list because the tool surface should be identical for the same
	// MCP server and a re-list would just add latency.
	m.mu.Lock()
	prev := m.statuses[server]
	m.mu.Unlock()
	m.recordStatus(MCPStatus{Name: server, Connected: true, Tools: prev.Tools, Tested: time.Now().UTC()})
	log.Printf("mcp: %s reconnected", server)
	return nil
}

// isTransportDeadErr matches the wrapper messages we see from the MCP SDK
// when the underlying SSE/stdio stream has died. Includes the literal
// "client is closing: EOF" produced by the go-sdk on a closed session.
func isTransportDeadErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for _, needle := range []string{
		"EOF",
		"client is closing",
		"connection closed",
		"broken pipe",
		"use of closed network connection",
		"connection reset",
	} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func (m *MCPManager) recordStatus(s MCPStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[s.Name] = s
}

func (m *MCPManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		_ = s.Close()
	}
}

type mcpTool struct {
	name   string
	desc   string
	schema map[string]any
	mgr    *MCPManager
	server string
	remote string
}

func (t *mcpTool) Name() string           { return t.name }
func (t *mcpTool) Description() string    { return t.desc }
func (t *mcpTool) Schema() map[string]any { return t.schema }

func (t *mcpTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	// Up to maxAttempts total. The Cloudflare Tunnel → mcp-proxy → claude
	// mcp serve stack is chatty: SSE sessions die silently and `tools/call`
	// hands back "EOF" before the keepalive has finished reconnecting.
	// One retry isn't enough — sometimes the reconnect itself races and
	// the second call also EOFs while the new session is still spinning
	// up. Three attempts with brief backoff catches the vast majority of
	// these without the boss seeing anything.
	const maxAttempts = 3
	var (
		res *mcp.CallToolResult
		err error
	)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		res, err = t.callOnce(ctx, input)
		if err == nil {
			break
		}
		if !isTransportDeadErr(err) {
			return "", err
		}
		log.Printf("mcp: %s.%s transport dead attempt %d/%d (%v) — reconnecting",
			t.server, t.remote, attempt, maxAttempts, err)
		if rerr := t.mgr.Reconnect(ctx, t.server); rerr != nil {
			// Reconnect itself failed. Bubble up only on the last
			// attempt; otherwise wait a beat and try again.
			if attempt == maxAttempts {
				return "", fmt.Errorf("reconnect %s: %w (orig: %v)", t.server, rerr, err)
			}
		}
		if attempt < maxAttempts {
			// Small backoff so the freshly-dialled SSE session has time
			// to register tools before we hit it again.
			select {
			case <-time.After(time.Duration(attempt) * 250 * time.Millisecond):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
	if err != nil {
		return "", err
	}
	if res.IsError {
		return collectText(res.Content), errors.New(collectText(res.Content))
	}
	return collectText(res.Content), nil
}

func (t *mcpTool) callOnce(ctx context.Context, input map[string]any) (*mcp.CallToolResult, error) {
	sess := t.mgr.getSession(t.server)
	if sess == nil {
		return nil, fmt.Errorf("mcp: no session for %s", t.server)
	}
	return sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      t.remote,
		Arguments: input,
	})
}

func collectText(content []mcp.Content) string {
	var b strings.Builder
	for _, c := range content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// sanitiseToolName forces a string to match Anthropic's tool name regex
// `^[a-zA-Z0-9_-]{1,128}$`. Anything outside that set is collapsed to `_`,
// runs of `_` are coalesced, and the result is truncated to 128 chars.
func sanitiseToolName(s string) string {
	if s == "" {
		return "_"
	}
	out := make([]byte, 0, len(s))
	prevUnderscore := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-'
		if !ok {
			if !prevUnderscore {
				out = append(out, '_')
				prevUnderscore = true
			}
			continue
		}
		out = append(out, c)
		prevUnderscore = c == '_'
	}
	if len(out) > 128 {
		out = out[:128]
	}
	if len(out) == 0 {
		return "_"
	}
	return string(out)
}

func mapFromAny(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{"type": "object"}
}
