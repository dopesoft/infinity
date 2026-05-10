// Package tools — MCP client wraps modelcontextprotocol/go-sdk and exposes
// each remote tool as a Tool in the central registry under the namespace
// "<server>.<tool>".
package tools

import (
	"context"
	"errors"
	"fmt"
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
type MCPManager struct {
	mu       sync.Mutex
	sessions map[string]*mcp.ClientSession
	statuses map[string]MCPStatus
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
		sessions: make(map[string]*mcp.ClientSession),
		statuses: make(map[string]MCPStatus),
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

func (m *MCPManager) connectOne(ctx context.Context, s MCPServerConfig, registry *Registry) error {
	client := mcp.NewClient(&mcp.Implementation{Name: "infinity-core", Version: "0.1.0"}, nil)

	var transport mcp.Transport
	switch s.Transport {
	case "stdio":
		if len(s.Command) == 0 {
			return fmt.Errorf("stdio transport needs command")
		}
		transport = &mcp.CommandTransport{Command: exec.Command(s.Command[0], s.Command[1:]...)}
	case "sse":
		url := s.resolveURL()
		if url == "" {
			return fmt.Errorf("sse transport needs url or url_env")
		}
		sse := &mcp.SSEClientTransport{Endpoint: url}
		headers, err := s.resolveAuthHeaders()
		if err != nil {
			return err
		}
		if len(headers) > 0 {
			sse.HTTPClient = &http.Client{
				Timeout:   60 * time.Second,
				Transport: &headerRoundTripper{headers: headers},
			}
		}
		transport = sse
	default:
		return fmt.Errorf("unknown transport: %s", s.Transport)
	}

	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
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
		registry.Register(&mcpTool{
			name:    name,
			desc:    desc,
			schema:  schema,
			session: sess,
			remote:  t.Name,
		})
		toolNames = append(toolNames, name)
	}

	m.mu.Lock()
	m.sessions[s.Name] = sess
	m.mu.Unlock()
	m.recordStatus(MCPStatus{Name: s.Name, Connected: true, Tools: toolNames, Tested: time.Now().UTC()})
	fmt.Printf("mcp: %s connected (%d tools)\n", s.Name, len(toolNames))
	return nil
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
	name    string
	desc    string
	schema  map[string]any
	session *mcp.ClientSession
	remote  string
}

func (t *mcpTool) Name() string                 { return t.name }
func (t *mcpTool) Description() string          { return t.desc }
func (t *mcpTool) Schema() map[string]any       { return t.schema }
func (t *mcpTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	res, err := t.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      t.remote,
		Arguments: input,
	})
	if err != nil {
		return "", err
	}
	if res.IsError {
		return collectText(res.Content), errors.New(collectText(res.Content))
	}
	return collectText(res.Content), nil
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
