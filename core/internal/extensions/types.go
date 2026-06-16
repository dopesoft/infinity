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
	KindCLI      Kind = "cli"       // a binary installed into the cloud workspace, run via bash_run
)

func (k Kind) Valid() bool { return k == KindMCP || k == KindHTTPTool || k == KindCLI }

// Status is the activation state of an extension.
type Status string

const (
	StatusActive   Status = "active"
	StatusError    Status = "error"
	StatusDisabled Status = "disabled"
	// StatusPendingAuth - a cli extension is installed but its check command
	// reports it isn't authenticated yet. AuthURL/AuthInstructions hold what
	// the boss must do; the ExtensionAuthChecklist re-probes until it passes.
	StatusPendingAuth Status = "pending_auth"
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
	// Human-in-the-loop auth state (cli kind). AuthURL is the device-login
	// URL Jarvis hands the boss; AuthInstructions is the human-readable ask;
	// ResumeIntent is what Jarvis should do once auth completes (surfaced in
	// the Finding the heartbeat fires when the check finally passes).
	AuthURL          string     `json:"authUrl,omitempty"`
	AuthInstructions string     `json:"authInstructions,omitempty"`
	ResumeIntent     string     `json:"resumeIntent,omitempty"`
	LastCheckedAt    *time.Time `json:"lastCheckedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

// MCPConfig is the `config` shape for kind=mcp. Auth fields reference env
// var NAMES - the actual token stays in the environment, never the DB.
type MCPConfig struct {
	URL            string `json:"url"`
	Transport      string `json:"transport"`                  // sse | http | streamable_http
	Auth           string `json:"auth,omitempty"`             // bearer | header | cloudflare_access | ""
	AuthTokenEnv   string `json:"auth_token_env,omitempty"`   // env var holding the token
	AuthHeaderName string `json:"auth_header_name,omitempty"` // for auth=header
	// AuthURL is the provider's sign-in / consent page for MCPs that need an
	// interactive OAuth login (not just a static token). When connecting fails
	// and this is set, the extension parks status=pending_auth with this URL so
	// the same CanvasAuthCard the CLI tools use surfaces it; the boss signs in
	// once in their own browser and the next reconnect succeeds. Generic: zero
	// per-provider code - the URL is config, the verify is "reconnect works".
	AuthURL string `json:"auth_url,omitempty"`
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

// CLIConfig is the `config` shape for kind=cli. A CLI extension is a binary
// Jarvis installs into the persistent cloud workspace and then runs via
// bash_run. Auth env-var NAMES (never values) are listed in AuthEnvs - the
// secret stays in Railway, same rule as mcp.
type CLIConfig struct {
	Install  string   `json:"install,omitempty"`   // bash to install the binary (e.g. "curl -fsSL …/install.sh | sh")
	Binary   string   `json:"binary"`              // command name used for `command -v` (e.g. "higgsfield")
	CheckCmd string   `json:"check_cmd,omitempty"` // exit 0 ⇒ installed AND ready (authed). e.g. "higgsfield account status"
	AuthCmd  string   `json:"auth_cmd,omitempty"`  // run when CheckCmd fails to start auth; its stdout should contain a device-login URL
	AuthEnvs []string `json:"auth_envs,omitempty"` // env var NAMES the CLI needs (informational; set on Railway)
	Usage    string   `json:"usage,omitempty"`     // how Jarvis should invoke it - injected into the system prompt
	Cwd      string   `json:"cwd,omitempty"`       // optional working dir for the commands
}

// parseCLIConfig decodes the generic config map into a typed CLIConfig.
func parseCLIConfig(raw map[string]any) (CLIConfig, error) {
	var cfg CLIConfig
	if err := remarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("extensions: invalid cli config: %w", err)
	}
	if strings.TrimSpace(cfg.Binary) == "" {
		return cfg, fmt.Errorf("extensions: cli config requires `binary`")
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
