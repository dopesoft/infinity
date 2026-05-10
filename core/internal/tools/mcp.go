// Package tools — MCP client wraps modelcontextprotocol/go-sdk and exposes
// each remote tool as a Tool in the central registry under the namespace
// "<server>.<tool>".
package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPServerConfig struct {
	Name         string   `yaml:"name"`
	Transport    string   `yaml:"transport"`
	Enabled      bool     `yaml:"enabled"`
	Command      []string `yaml:"command"`
	URL          string   `yaml:"url"`
	Auth         string   `yaml:"auth"`
	AuthTokenEnv string   `yaml:"auth_token_env"`
}

type MCPConfig struct {
	Servers []MCPServerConfig `yaml:"servers"`
}

func LoadMCPConfig(path string) (*MCPConfig, error) {
	if path == "" {
		path = os.Getenv("MCP_CONFIG")
	}
	if path == "" {
		path = "core/config/mcp.yaml"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &MCPConfig{}, nil
		}
		return nil, err
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
		if s.URL == "" {
			return fmt.Errorf("sse transport needs url")
		}
		transport = &mcp.SSEClientTransport{Endpoint: s.URL}
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
		name := s.Name + "." + t.Name
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

func mapFromAny(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{"type": "object"}
}
