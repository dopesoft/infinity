package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/tools"
)

// Core as an MCP SERVER.
//
// Everywhere else in this codebase Infinity is an MCP *client* (tools/mcp.go
// dials Claude Code, GitHub, Composio). This is the other direction: it
// publishes Infinity's OWN tool registry - memory, surface, connectors,
// skills, everything the agent loop can call - as an MCP server over
// streamable HTTP, so an external harness can use them.
//
// It exists because the Claude Max brain (llm/claude_code.go) runs Claude
// Code's own agent loop rather than handing tool calls back to our loop. That
// is what makes it a genuinely capable coding/research brain, but on its own
// it would be a brain with no memory: it could read files and search the web
// and never write a surface item or recall a fact. Pointing that session's
// --mcp-config at this endpoint closes the gap, and it does so GENERICALLY -
// any future harness gets the same registry with no per-vendor wiring, and a
// tool added to the Registry appears here the same turn with no work at all.
//
// Executions go through the SAME Registry.Execute the agent loop uses, so
// gates, hooks and memory capture all fire exactly as they do in chat. There
// is no second execution path to keep in sync.
const (
	mcpProtocolVersion = "2025-06-18"
	mcpServerName      = "infinity"
)

// --- JSON-RPC 2.0 envelope ---------------------------------------------------

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

func rpcOK(id json.RawMessage, result any) jsonRPCResponse {
	return jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func rpcErr(id json.RawMessage, code int, msg string) jsonRPCResponse {
	return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &jsonRPCError{Code: code, Message: msg}}
}

// --- handler -----------------------------------------------------------------

// handleMCPServer answers MCP JSON-RPC over HTTP. Mounted at /api/mcp/server
// and authenticated by a minted bearer token rather than the app's own JWT:
// the caller is a headless Claude Code session, not a signed-in browser, and
// scoping it to a per-session token means a brain run can be cut off on its
// own without touching the boss's login.
func (s *Server) handleMCPServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// GET is the SSE half of streamable HTTP. We answer every request
		// inline, so there is no server-initiated stream to open - say so
		// rather than leaving a client hanging on a connection we will never
		// write to.
		w.Header().Set("Allow", "POST")
		http.Error(w, "this MCP server answers on POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.mcpTokens == nil || !s.mcpTokens.Valid(bearerToken(r)) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, rpcErr(nil, -32700, "parse error: "+err.Error()))
		return
	}
	// A JSON-RPC NOTIFICATION carries no id and must get no response body.
	// `notifications/initialized` is the one every MCP client sends right
	// after initialize; answering it with a result is a protocol violation
	// some clients treat as fatal.
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, s.dispatchMCP(r, req))
}

func (s *Server) dispatchMCP(r *http.Request, req jsonRPCRequest) jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return rpcOK(req.ID, map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": mcpServerName, "version": "1"},
		})
	case "ping":
		return rpcOK(req.ID, map[string]any{})
	case "tools/list":
		return rpcOK(req.ID, map[string]any{"tools": s.mcpToolList()})
	case "tools/call":
		return s.mcpToolCall(r, req)
	default:
		return rpcErr(req.ID, -32601, "method not found: "+req.Method)
	}
}

// mcpToolList renders the live registry in MCP's shape. It reads the same
// Registry the agent loop reads, so this can never drift from what the tools
// actually are.
func (s *Server) mcpToolList() []map[string]any {
	out := []map[string]any{}
	if s.loop == nil {
		return out
	}
	reg := s.loop.Tools()
	if reg == nil {
		return out
	}
	for _, name := range reg.Names() {
		t, ok := reg.Get(name)
		if !ok {
			continue
		}
		schema := t.Schema()
		if schema == nil {
			// MCP requires an object schema. A tool that takes no input
			// still needs one, or strict clients reject the whole list and
			// the brain silently loses every tool.
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{
			"name":        t.Name(),
			"description": t.Description(),
			"inputSchema": schema,
		})
	}
	return out
}

func (s *Server) mcpToolCall(r *http.Request, req jsonRPCRequest) jsonRPCResponse {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return rpcErr(req.ID, -32602, "invalid params: "+err.Error())
	}
	if strings.TrimSpace(params.Name) == "" {
		return rpcErr(req.ID, -32602, "invalid params: tool name is required")
	}
	if s.loop == nil || s.loop.Tools() == nil {
		return rpcErr(req.ID, -32603, "tool registry unavailable")
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}
	// Execute is the SAME entry point the agent loop uses, so the Trust gate,
	// the loop gate and every capture hook fire here exactly as they do in
	// chat. There is deliberately no second execution path.
	result, err := s.loop.Tools().Execute(r.Context(), llm.ToolCall{
		Name:  params.Name,
		Input: params.Arguments,
	})
	if err != nil {
		// A failed tool is a RESULT in MCP, not a transport error: the model
		// is supposed to see it and react. Returning a JSON-RPC error instead
		// hides the failure from the only party that can do anything about
		// it - the same empty-because-broken trap the self-healing rules
		// exist to prevent.
		return rpcOK(req.ID, map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
		})
	}
	return rpcOK(req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": result}},
	})
}

// bearerToken pulls the credential off the Authorization header.
func bearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if h == "" {
		return ""
	}
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return h
}

// ClaudeBrainStatusProbe is the Settings card's window onto the Claude Max
// Plan connection. Implemented by tools.ClaudeCodeRunner.
type ClaudeBrainStatusProbe interface {
	BrainStatus(ctx context.Context) tools.BrainStatus
}

// handleClaudeBrainStatus reports whether the Claude Max Plan brain can
// answer, and says so in the boss's words rather than an enum.
func (s *Server) handleClaudeBrainStatus(w http.ResponseWriter, r *http.Request) {
	if s.claudeBrain == nil {
		// No probe wired means the Mac coding path isn't running at all, and
		// this brain is launched by the same runner - say that plainly rather
		// than reporting a cheerful "not connected" that hides the reason.
		writeJSON(w, http.StatusOK, tools.BrainStatus{
			Detail: "The Mac connection isn't running on this deploy, so there's nothing for Claude to sign in through.",
		})
		return
	}
	writeJSON(w, http.StatusOK, s.claudeBrain.BrainStatus(r.Context()))
}

// claudeMaxTokenProvider is the credential id the cloud box's subscription
// token is stored under. Deliberately NOT one of llm.KeyableVendors: this is
// not an API key that constructs a provider, it is a sign-in the harness
// exports for one command. Putting it in that list would have BuildRegistry
// try to build a brain out of it at boot.
const claudeMaxTokenProvider = "claude_max"

// claudeMaxTokenSavedAtKey records when the token was pasted. A setup-token
// lasts a year and says so nowhere machine-readable, so this date is what the
// expiry warning counts from.
const claudeMaxTokenSavedAtKey = "claude_max.token_saved_at"

// handleClaudeMaxToken stores or clears the cloud box's Claude subscription
// token (the one `claude setup-token` prints).
//
// It gets its own endpoint rather than joining the provider-keys API because
// it is a different kind of thing: that API answers "which vendors can Core
// construct", and this credential constructs nothing. It only decides whether
// the cloud box can sign in as the boss.
func (s *Server) handleClaudeMaxToken(w http.ResponseWriter, r *http.Request) {
	if s.llmKeys == nil {
		http.Error(w, "credential storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodPut, http.MethodPost:
		var body struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "could not read the token", http.StatusBadRequest)
			return
		}
		token := strings.TrimSpace(body.Token)
		if token == "" {
			http.Error(w, "that came through empty", http.StatusBadRequest)
			return
		}
		// Refuse an API key outright. Pasting one here would look like it
		// worked and then quietly bill per token instead of the plan - the
		// exact swap this whole path exists to prevent.
		if strings.HasPrefix(token, "sk-ant-api") {
			http.Error(w, "That's an API key, which bills per token. I need the subscription token from `claude setup-token`.", http.StatusBadRequest)
			return
		}
		if err := s.llmKeys.Set(r.Context(), claudeMaxTokenProvider, token, "Claude subscription (cloud)"); err != nil {
			http.Error(w, "couldn't save that: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Record WHEN, so the heartbeat can warn a month before the one-year
		// expiry instead of the boss discovering it from a dead 3am build.
		// The token itself carries no readable expiry, so the save date is
		// the only clock we get.
		if s.settings != nil {
			_ = s.settings.Set(r.Context(), claudeMaxTokenSavedAtKey, time.Now().UTC().Format(time.RFC3339))
		}
		writeJSON(w, http.StatusOK, map[string]any{"stored": true})
	case http.MethodDelete:
		if _, err := s.llmKeys.Delete(r.Context(), claudeMaxTokenProvider); err != nil {
			http.Error(w, "couldn't remove that: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if s.settings != nil {
			_ = s.settings.Set(r.Context(), claudeMaxTokenSavedAtKey, "")
		}
		writeJSON(w, http.StatusOK, map[string]any{"stored": false})
	default:
		w.Header().Set("Allow", "PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
