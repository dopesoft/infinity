package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/tools"
)

// RegisterTools wires the three agent-facing self-extension tools:
//
//	extension_register — wire a new MCP server or REST-API tool, live
//	extension_list     — see what's registered
//	extension_remove   — disable an extension
//
// This file lives in package extensions (which imports tools), so the
// tools register themselves without a tools → extensions import cycle —
// the same pattern skills/registry_tools.go uses.
func RegisterTools(reg *tools.Registry, mgr *Manager) {
	if reg == nil || mgr == nil {
		return
	}
	reg.Register(&extensionRegisterTool{mgr: mgr})
	reg.Register(&extensionListTool{mgr: mgr})
	reg.Register(&extensionRemoveTool{mgr: mgr})
}

// ── extension_register ──────────────────────────────────────────────────────

type extensionRegisterTool struct{ mgr *Manager }

func (t *extensionRegisterTool) Name() string { return "extension_register" }
func (t *extensionRegisterTool) Description() string {
	return "Extend your own toolset at runtime. Wire a new capability and it's " +
		"live this session AND durable across restarts — no redeploy.\n\n" +
		"kind='http_tool': turn any REST endpoint into a named tool. config = " +
		"{method, url, headers?, body_template?, params?}. {{param}} placeholders " +
		"in url/headers/body_template are filled from the generated tool's call " +
		"args; declare each in `params` ([{name, description, required}]). The " +
		"generated tool is named ext_<name>.\n\n" +
		"kind='mcp': connect a remote MCP server. config = {url, transport " +
		"(sse|http), auth? (bearer|header|cloudflare_access), auth_token_env?, " +
		"auth_header_name?}. Auth references an env var NAME — never paste a " +
		"secret. Its tools register under <name>__<tool>.\n\n" +
		"Use this when a task needs a capability you don't have. Returns the " +
		"registration result."
}
func (t *extensionRegisterTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string", "description": "kebab-case identifier for the extension (e.g. 'openweather', 'linear-mcp')."},
			"kind":        map[string]any{"type": "string", "enum": []string{"http_tool", "mcp"}, "description": "http_tool = a REST endpoint as a tool · mcp = a remote MCP server."},
			"description": map[string]any{"type": "string", "description": "What the capability does + when to use it (becomes the tool description)."},
			"config":      map[string]any{"type": "object", "description": "Kind-specific config — see the tool description for the shape of each kind."},
		},
		"required": []string{"name", "kind", "config"},
	}
}
func (t *extensionRegisterTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	name := strings.TrimSpace(strString(in, "name"))
	if name == "" {
		return "", errors.New("extension_register: name is required")
	}
	kind := Kind(strings.TrimSpace(strString(in, "kind")))
	if !kind.Valid() {
		return "", fmt.Errorf("extension_register: invalid kind %q (want http_tool | mcp)", kind)
	}
	config, _ := in["config"].(map[string]any)
	if config == nil {
		config = map[string]any{}
	}
	ext := &Extension{
		Name:        name,
		Kind:        kind,
		Description: strString(in, "description"),
		Config:      config,
		Source:      "agent",
	}
	if err := t.mgr.Register(ctx, ext); err != nil {
		return "", err
	}
	result := map[string]any{
		"ok":     true,
		"name":   name,
		"kind":   string(kind),
		"status": string(ext.Status),
	}
	if kind == KindHTTPTool {
		result["tool_name"] = extToolName(name)
		result["message"] = fmt.Sprintf("Registered — the tool %q is live now.", extToolName(name))
	} else {
		result["message"] = fmt.Sprintf("MCP server %q connected — its tools are live now.", name)
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

// ── extension_list ──────────────────────────────────────────────────────────

type extensionListTool struct{ mgr *Manager }

func (t *extensionListTool) Name() string { return "extension_list" }
func (t *extensionListTool) Description() string {
	return "List every runtime-registered extension — MCP servers and HTTP tools — " +
		"with their kind, status, and any activation error. Check this before " +
		"registering a duplicate."
}
func (t *extensionListTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *extensionListTool) Execute(ctx context.Context, _ map[string]any) (string, error) {
	exts, err := t.mgr.List(ctx)
	if err != nil {
		return "", err
	}
	out := make([]map[string]any, 0, len(exts))
	for _, e := range exts {
		row := map[string]any{
			"name":        e.Name,
			"kind":        string(e.Kind),
			"description": e.Description,
			"enabled":     e.Enabled,
			"status":      string(e.Status),
		}
		if e.LastError != "" {
			row["last_error"] = e.LastError
		}
		if e.Kind == KindHTTPTool {
			row["tool_name"] = extToolName(e.Name)
		}
		out = append(out, row)
	}
	b, _ := json.MarshalIndent(map[string]any{"extensions": out}, "", "  ")
	return string(b), nil
}

// ── extension_remove ────────────────────────────────────────────────────────

type extensionRemoveTool struct{ mgr *Manager }

func (t *extensionRemoveTool) Name() string { return "extension_remove" }
func (t *extensionRemoveTool) Description() string {
	return "Disable a registered extension by name. An http_tool is unregistered " +
		"from the live tool registry immediately; an mcp server stops loading on " +
		"the next restart."
}
func (t *extensionRemoveTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"name": map[string]any{"type": "string"}},
		"required":   []string{"name"},
	}
}
func (t *extensionRemoveTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	name := strings.TrimSpace(strString(in, "name"))
	if name == "" {
		return "", errors.New("extension_remove: name is required")
	}
	if err := t.mgr.Remove(ctx, name); err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "name": name, "status": "disabled"})
	return string(out), nil
}

// strString pulls a trimmed string from a tool-arg map. Local copy so the
// extensions package doesn't depend on tool-package internals.
func strString(in map[string]any, key string) string {
	if v, ok := in[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
