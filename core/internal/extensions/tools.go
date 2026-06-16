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
//	extension_register - wire a new MCP server or REST-API tool, live
//	extension_list     - see what's registered
//	extension_remove   - disable an extension
//
// This file lives in package extensions (which imports tools), so the
// tools register themselves without a tools → extensions import cycle -
// the same pattern skills/registry_tools.go uses.
func RegisterTools(reg *tools.Registry, mgr *Manager) {
	if reg == nil || mgr == nil {
		return
	}
	reg.Register(&extensionRegisterTool{mgr: mgr})
	reg.Register(&extensionListTool{mgr: mgr})
	reg.Register(&extensionRemoveTool{mgr: mgr})
	reg.Register(&extensionCheckTool{mgr: mgr})
}

// ── extension_register ──────────────────────────────────────────────────────

type extensionRegisterTool struct{ mgr *Manager }

func (t *extensionRegisterTool) Name() string { return "extension_register" }
func (t *extensionRegisterTool) Description() string {
	return "Extend your own toolset at runtime. Wire a new capability and it's " +
		"live this session AND durable across restarts - no redeploy.\n\n" +
		"kind='http_tool': turn any REST endpoint into a named tool. config = " +
		"{method, url, headers?, body_template?, params?}. {{param}} placeholders " +
		"in url/headers/body_template are filled from the generated tool's call " +
		"args; declare each in `params` ([{name, description, required}]). The " +
		"generated tool is named ext_<name>.\n\n" +
		"kind='mcp': connect a remote MCP server. config = {url, transport " +
		"(sse|http), auth? (bearer|header|cloudflare_access), auth_token_env?, " +
		"auth_header_name?}. Auth references an env var NAME - never paste a " +
		"secret. Its tools register under <name>__<tool>.\n\n" +
		"kind='cli': install a command-line tool into the persistent cloud " +
		"workspace and run it via bash_run. config = {binary (required, the " +
		"command name), install? (bash to install it, e.g. 'curl -fsSL " +
		".../install.sh | sh'), check_cmd? (exit 0 ⇒ installed AND " +
		"authenticated, e.g. 'mycli status'), auth_cmd? (run when check_cmd " +
		"fails; its output should contain a device-login URL), auth_envs? " +
		"([env var NAMES the tool needs]), usage? (how to invoke it - shown to " +
		"you each turn), cwd?}. The tool installs cloud-side so it works on a " +
		"schedule even when the Mac is offline. If it needs interactive auth, " +
		"this returns status='pending_auth' with an auth_url - do NOT hand the " +
		"boss that URL (he can't open it himself); call browser_open with it so " +
		"the sign-in page is live in his Preview pane and he signs in there. Then " +
		"I'll detect when he's done and you can continue (or use extension_check " +
		"to verify within this session).\n\n" +
		"Optional `resume_intent`: a short note of what to do once a " +
		"pending_auth tool becomes ready (surfaced back to you automatically " +
		"when auth completes).\n\n" +
		"Use this when a task needs a capability you don't have. Returns the " +
		"registration result."
}
func (t *extensionRegisterTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":          map[string]any{"type": "string", "description": "kebab-case identifier for the extension (e.g. 'openweather', 'linear-mcp', 'higgsfield')."},
			"kind":          map[string]any{"type": "string", "enum": []string{"http_tool", "mcp", "cli"}, "description": "http_tool = a REST endpoint · mcp = a remote MCP server · cli = a command-line tool installed in the cloud workspace."},
			"description":   map[string]any{"type": "string", "description": "What the capability does + when to use it (becomes the tool description)."},
			"config":        map[string]any{"type": "object", "description": "Kind-specific config - see the tool description for the shape of each kind."},
			"resume_intent": map[string]any{"type": "string", "description": "Optional. For cli tools that need auth: what to do once it's authenticated."},
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
		Name:         name,
		Kind:         kind,
		Description:  strString(in, "description"),
		Config:       config,
		Source:       "agent",
		ResumeIntent: strString(in, "resume_intent"),
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
	switch {
	case kind == KindHTTPTool:
		result["tool_name"] = extToolName(name)
		result["message"] = fmt.Sprintf("Registered - the tool %q is live now.", extToolName(name))
	case kind == KindCLI && ext.Status == StatusPendingAuth:
		result["auth_url"] = ext.AuthURL
		result["auth_instructions"] = ext.AuthInstructions
		result["next_action"] = "browser_open"
		result["message"] = fmt.Sprintf(
			"%q is installed in the cloud workspace but needs sign-in. Do NOT hand the boss a raw URL "+
				"— he cannot open it himself. YOU open it in his Preview pane: call browser_open with %s "+
				"so the live sign-in page appears in front of him, then tell him to sign in right there in "+
				"the preview. I'll keep checking and continue automatically once he's done (or call "+
				"extension_check to verify now).",
			name, ext.AuthURL)
	case kind == KindCLI:
		result["message"] = fmt.Sprintf(
			"%q is installed and ready in the cloud workspace. Run it via bash_run "+
				"(source %s first so it uses the persistent install + credentials).",
			name, EnvFilePath)
	default:
		result["message"] = fmt.Sprintf("MCP server %q connected - its tools are live now.", name)
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

// ── extension_list ──────────────────────────────────────────────────────────

type extensionListTool struct{ mgr *Manager }

func (t *extensionListTool) Name() string { return "extension_list" }
func (t *extensionListTool) Description() string {
	return "List every runtime-registered extension - MCP servers and HTTP tools - " +
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
		if e.Status == StatusPendingAuth {
			row["auth_url"] = e.AuthURL
			row["auth_instructions"] = e.AuthInstructions
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

// ── extension_check ───────────────────────────────────────────────────────

type extensionCheckTool struct{ mgr *Manager }

func (t *extensionCheckTool) Name() string   { return "extension_check" }
func (t *extensionCheckTool) ReadOnly() bool { return true }
func (t *extensionCheckTool) Description() string {
	return "Re-check whether a cli extension is now ready - run this after you've " +
		"asked the boss to authenticate (e.g. he opened the device-login URL). " +
		"Returns status='active' once its check command passes, or still " +
		"'pending_auth' if he hasn't finished. Once active, you can run the tool " +
		"via bash_run and continue what you were doing."
}
func (t *extensionCheckTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"name": map[string]any{"type": "string"}},
		"required":   []string{"name"},
	}
}
func (t *extensionCheckTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	name := strString(in, "name")
	if name == "" {
		return "", errors.New("extension_check: name is required")
	}
	ext, err := t.mgr.store.GetByName(ctx, name)
	if err != nil {
		return "", err
	}
	if ext == nil {
		return "", fmt.Errorf("extension_check: no extension named %q", name)
	}
	if ext.Kind != KindCLI {
		out, _ := json.Marshal(map[string]any{"name": name, "kind": string(ext.Kind), "status": string(ext.Status), "message": "only cli extensions have an auth check"})
		return string(out), nil
	}
	ready, perr := t.mgr.ProbeCLIReady(ctx, ext)
	if perr != nil {
		return "", perr
	}
	res := map[string]any{"name": name, "ready": ready}
	if ready {
		_ = t.mgr.CompleteAuth(ctx, name)
		res["status"] = string(StatusActive)
		res["message"] = fmt.Sprintf("%q is authenticated and ready. Run it via bash_run (source %s first).", name, EnvFilePath)
		if ext.ResumeIntent != "" {
			res["resume_intent"] = ext.ResumeIntent
		}
	} else {
		t.mgr.touchChecked(ctx, name)
		res["status"] = string(StatusPendingAuth)
		res["auth_url"] = ext.AuthURL
		res["next_action"] = "browser_open"
		res["message"] = fmt.Sprintf(
			"Still not authenticated. Open the sign-in page in his Preview pane yourself with "+
				"browser_open (%s) if it isn't already up, and ask him to finish signing in right there "+
				"in the preview — do not send him a URL to open. Then check again.", ext.AuthURL)
	}
	out, _ := json.Marshal(res)
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
