package extensions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/httpx"
)

// HTTPTool is a generic native tool built from an HTTPToolConfig - the
// payoff of runtime self-extension. The agent registers any REST endpoint
// via extension_register (kind=http_tool) and it becomes a first-class
// tool: {{param}} placeholders in the URL / headers / body are filled from
// the call args, the request fires, the response comes back.
//
// It satisfies tools.Tool structurally (Name / Description / Schema /
// Execute), so the extensions Manager can register it without this package
// needing to name the tools.Tool interface.
type HTTPTool struct {
	toolName    string
	description string
	cfg         HTTPToolConfig
	client      *http.Client
}

// NewHTTPTool validates a config and builds the tool.
func NewHTTPTool(toolName, description string, cfg HTTPToolConfig) (*HTTPTool, error) {
	cfg.Method = strings.ToUpper(strings.TrimSpace(cfg.Method))
	if cfg.Method == "" {
		cfg.Method = http.MethodGet
	}
	switch cfg.Method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, fmt.Errorf("http_tool: unsupported method %q", cfg.Method)
	}
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("http_tool: url is required")
	}
	if strings.TrimSpace(toolName) == "" {
		return nil, fmt.Errorf("http_tool: tool name is required")
	}
	return &HTTPTool{
		toolName:    toolName,
		description: description,
		cfg:         cfg,
		// Guarded: both halves of this URL are model-chosen — the template came
		// from a registration the agent authored, and the {{param}} values come
		// from the call itself. It had no network boundary at all before,
		// because the only SSRF policy in the tree was unexported inside
		// package tools and unreachable from here.
		client: httpx.GuardedClient(toolName, 30*time.Second),
	}, nil
}

func (t *HTTPTool) Name() string        { return t.toolName }
func (t *HTTPTool) Description() string { return t.description }

func (t *HTTPTool) Schema() map[string]any {
	props := map[string]any{}
	var required []string
	for _, p := range t.cfg.Params {
		props[p.Name] = map[string]any{
			"type":        "string",
			"description": p.Description,
		}
		if p.Required {
			required = append(required, p.Name)
		}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// Execute fills {{param}} placeholders from the call args, sends the
// request, and returns {status, body} as JSON. Non-2xx responses are NOT
// Go errors - the agent gets the status + body and decides. Only a
// transport failure returns an error.
func (t *HTTPTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	subst := func(s string) string {
		for k, v := range in {
			s = strings.ReplaceAll(s, "{{"+k+"}}", fmt.Sprint(v))
		}
		return s
	}

	var body io.Reader
	if t.cfg.BodyTemplate != "" {
		body = bytes.NewBufferString(subst(t.cfg.BodyTemplate))
	}
	// Check AFTER substitution: a template like https://host/{{path}} is only
	// as safe as the value the model just put in it, and a template of {{url}}
	// is entirely the model's choice.
	target := subst(t.cfg.URL)
	if err := httpx.CheckTarget(target); err != nil {
		return "", fmt.Errorf("%s: %w", t.toolName, err)
	}
	req, err := http.NewRequestWithContext(ctx, t.cfg.Method, target, body)
	if err != nil {
		return "", fmt.Errorf("%s: build request: %w", t.toolName, err)
	}
	for k, v := range t.cfg.Headers {
		req.Header.Set(k, subst(v))
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: request failed: %w", t.toolName, err)
	}
	defer resp.Body.Close()

	const maxBody = 16 * 1024
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	out, _ := json.Marshal(map[string]any{
		"status": resp.StatusCode,
		"body":   string(raw),
	})
	return string(out), nil
}
