package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// decodeChain unmarshals the action_chain JSON buildActionChain produced.
func decodeChain(t *testing.T, raw json.RawMessage) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("action_chain is not valid JSON: %v (%s)", err, string(raw))
	}
	return out
}

// action_prompt is the primary, documented path — it must produce an
// agent_turn carrying the instruction verbatim, so "ping me when X" runs
// through the agent loop with full tool access.
func TestBuildActionChain_ActionPrompt(t *testing.T) {
	raw, err := buildActionChain(map[string]any{
		"action_prompt": "Notify the boss that the site is down.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	chain := decodeChain(t, raw)
	if len(chain) != 1 || chain[0]["kind"] != "agent_turn" {
		t.Fatalf("expected one agent_turn action, got %s", string(raw))
	}
	args, _ := chain[0]["args"].(map[string]any)
	if p, _ := args["prompt"].(string); p != "Notify the boss that the site is down." {
		t.Errorf("prompt not carried through: %q", p)
	}
}

// action_skill must ALSO route through agent_turn (not a raw "skill" action),
// because the agent loop runs every skill type whereas the direct runner path
// only executes code skills. This guards the choice that prevents the
// instruction-only-skill footgun.
func TestBuildActionChain_ActionSkill_WrapsInAgentTurn(t *testing.T) {
	raw, err := buildActionChain(map[string]any{"action_skill": "inbox-triage"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	chain := decodeChain(t, raw)
	if chain[0]["kind"] != "agent_turn" {
		t.Fatalf("action_skill should wrap in agent_turn, got %s", string(raw))
	}
	args, _ := chain[0]["args"].(map[string]any)
	if p, _ := args["prompt"].(string); !strings.Contains(p, "inbox-triage") {
		t.Errorf("prompt should name the skill to run: %q", p)
	}
}

// A raw action_chain is for power users / executable code skills and must pass
// through untouched.
func TestBuildActionChain_RawChainPassthrough(t *testing.T) {
	raw, err := buildActionChain(map[string]any{
		"action_chain": []any{
			map[string]any{"kind": "skill", "args": map[string]any{"name": "deploy-watch"}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	chain := decodeChain(t, raw)
	if chain[0]["kind"] != "skill" {
		t.Errorf("raw chain should pass through verbatim, got %s", string(raw))
	}
}

// An actionless sentinel does nothing useful — the tool must refuse it rather
// than create a silent no-op.
func TestBuildActionChain_NoAction_Errors(t *testing.T) {
	if _, err := buildActionChain(map[string]any{}); err == nil {
		t.Fatal("expected an error when no action is provided")
	}
}
