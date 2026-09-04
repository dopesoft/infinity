package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/tools"
)

// fatTool returns a 200KB, many-line result: the shape of a full build log
// or a file dump handed straight to the brain.
type fatTool struct{}

func (fatTool) Name() string           { return "fat_tool" }
func (fatTool) Description() string    { return "returns a very large result" }
func (fatTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (fatTool) Execute(context.Context, map[string]any) (string, error) {
	var b strings.Builder
	for i := 0; b.Len() < 200_000; i++ {
		b.WriteString(strings.Repeat("x", 90))
		b.WriteString("\n")
	}
	return b.String(), nil
}

func newMCPTestServer(t *testing.T) *Server {
	t.Helper()
	reg := tools.NewRegistry()
	reg.Register(fatTool{})
	return &Server{loop: agent.New(agent.Config{Tools: reg})}
}

// Claude Code calls tools through here and never through the agent loop, so
// the loop's trim did not apply: a 200KB result went into its transcript
// whole and was re-read on every one of its own calls. The cap has to live
// at this chokepoint.
func TestMCPToolCall_TrimsAnOversizedResultAtTheChokepoint(t *testing.T) {
	t.Setenv("INFINITY_TOOL_RESULT_MAX_TOKENS", "")
	srv := newMCPTestServer(t)
	req := httptest.NewRequest("POST", "/mcp", nil)
	resp := srv.mcpToolCall(req, jsonRPCRequest{
		ID:     json.RawMessage(`1`),
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"fat_tool","arguments":{}}`),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected rpc error: %+v", resp.Error)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result has the wrong shape: %T", resp.Result)
	}
	content := result["content"].([]map[string]any)
	text := content[0]["text"].(string)
	max := agent.ToolResultMaxChars()
	if len(text) > max+2_000 {
		t.Fatalf("the result was not trimmed: %d chars against a %d budget", len(text), max)
	}
	if !strings.Contains(text, "elided") {
		t.Fatal("a trimmed result must carry the elision marker so the model knows to re-fetch a narrower slice")
	}
}

// The cap is advertised on the tool list too, so Claude Code knows the
// budget before it calls, and the number it sees is the one enforced.
func TestMCPToolList_AdvertisesTheResultCap(t *testing.T) {
	t.Setenv("INFINITY_TOOL_RESULT_MAX_TOKENS", "")
	srv := newMCPTestServer(t)
	list := srv.mcpToolList()
	if len(list) == 0 {
		t.Fatal("no tools listed")
	}
	for _, entry := range list {
		meta, ok := entry["_meta"].(map[string]any)
		if !ok {
			t.Fatalf("tool %v carries no _meta", entry["name"])
		}
		if got := meta["anthropic/maxResultSizeChars"]; got != agent.ToolResultMaxChars() {
			t.Fatalf("tool %v advertises %v, enforced cap is %d", entry["name"], got, agent.ToolResultMaxChars())
		}
	}
}
