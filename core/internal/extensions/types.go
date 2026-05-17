// Package extensions implements runtime self-extension - Phase 3 of the
// assembly substrate.
//
// The agent extends its own toolset at runtime: it wires a new MCP server
// or registers a REST API as a named tool, and that capability is live
// this session AND durable across restarts (re-activated from
// mem_extensions on the next boot). No rebuild of the embedded mcp.yaml,
// no redeploy.
//
// Secrets never land in the DB - MCP auth references env var NAMES, never
// values. The agent registers through the extension_* tools, never raw SQL.
package extensions

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Kind is the type of capability an extension provides.
type Kind string

const (
	KindMCP      Kind = "mcp"       // a remote MCP server
	KindHTTPTool Kind = "http_tool" // a single REST endpoint as a named tool
)

func (k Kind) Valid() bool { return k == KindMCP || k == KindHTTPTool }

// Status is the activation state of an extension.
type Status string

const (
	StatusActive   Status = "active"
	StatusError    Status = "error"
	StatusDisabled Status = "disabled"
)

// Extension is one runtime-registered capability provider.
type Extension struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Kind        Kind           `json:"kind"`
	Description string         `json:"description"`
	Config      map[string]any `json:"config"`
	Enabled     bool           `json:"enabled"`
	Source      string         `json:"source"`
	Status      Status         `json:"status"`
	LastError   string         `json:"lastError,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

// MCPConfig is the `config` shape for kind=mcp. Auth fields reference env
// var NAMES - the actual token stays in the environment, never the DB.
type MCPConfig struct {
	URL            string `json:"url"`
	Transport      string `json:"transport"` // sse | http | streamable_http
	Auth           string `json:"auth,omitempty"`             // bearer | header | cloudflare_access | ""
	AuthTokenEnv   string `json:"auth_token_env,omitempty"`   // env var holding the token
	AuthHeaderName string `json:"auth_header_name,omitempty"` // for auth=header
}

// HTTPParam declares one input parameter on a generated http_tool.
type HTTPParam struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// HTTPToolConfig is the `config` shape for kind=http_tool. String values
// in URL / Headers / BodyTemplate may contain {{param}} placeholders,
// filled from the generated tool's call args.
type HTTPToolConfig struct {
	Method       string            `json:"method"` // GET | POST | PUT | PATCH | DELETE
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers,omitempty"`
	BodyTemplate string            `json:"body_template,omitempty"`
	Params       []HTTPParam       `json:"params,omitempty"`
}

// parseMCPConfig decodes the generic config map into a typed MCPConfig.
func parseMCPConfig(raw map[string]any) (MCPConfig, error) {
	var cfg MCPConfig
	if err := remarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("extensions: invalid mcp config: %w", err)
	}
	if strings.TrimSpace(cfg.URL) == "" {
		return cfg, fmt.Errorf("extensions: mcp config requires `url`")
	}
	if cfg.Transport == "" {
		cfg.Transport = "http"
	}
	return cfg, nil
}

// parseHTTPToolConfig decodes the generic config map into a typed config.
func parseHTTPToolConfig(raw map[string]any) (HTTPToolConfig, error) {
	var cfg HTTPToolConfig
	if err := remarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("extensions: invalid http_tool config: %w", err)
	}
	if strings.TrimSpace(cfg.URL) == "" {
		return cfg, fmt.Errorf("extensions: http_tool config requires `url`")
	}
	return cfg, nil
}

func remarshal(in any, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// sanitizeName lowercases and replaces non-alphanumeric runes with '_' so a
// generated tool name is always a clean identifier.
func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_")
}
