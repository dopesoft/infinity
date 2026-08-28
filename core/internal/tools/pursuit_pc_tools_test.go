package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/pursuits/pc"
)

// Everything this tool writes is the boss's own first-person writing: his
// identity, his evidence, his proof actions, his review. An unattended turn
// (cron, heartbeat, sub-agent) has nobody who said any of it, so anything it
// wrote would be invented on his behalf and then shown back to him as his own
// words. The skill body says "only write what he actually decided", but prose
// is droppable by the model, so the refusal has to hold in code.
//
// The pool is deliberately nil: if the guard were ever moved below the store
// call, this test would panic instead of passing.
func TestPursuitPCWriteRefusesUnattendedTurns(t *testing.T) {
	tool := &pursuitPCWrite{pool: nil}
	ctx := WithAutonomous(context.Background())

	for _, action := range pc.WriteActions() {
		out, err := tool.Execute(ctx, map[string]any{
			"pursuit_id": "11111111-1111-1111-1111-111111111111",
			"action":     action,
			"kind":       "morning",
			"body":       "something nobody said",
			"title":      "a memory he never banked",
			"label":      "a proof he never pledged",
		})
		if err == nil {
			t.Fatalf("action %q was allowed on an unattended turn (returned %q)", action, out)
		}
		if out != "" {
			t.Fatalf("a refused write must return no result, got %q", out)
		}
		// The refusal has to read as a reason, not a category, and point at
		// what to do instead.
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "live chat") {
			t.Fatalf("action %q refusal does not say where to raise it: %v", action, err)
		}
	}
}

// The same guard must NOT block an ordinary interactive turn - that is the
// whole point of the cockpit and a coaching chat staying in sync. Here the nil
// pool proves the guard let the call through: it gets past the refusal and
// fails somewhere else.
func TestPursuitPCWriteAllowsAttendedTurns(t *testing.T) {
	tool := &pursuitPCWrite{pool: nil}

	defer func() {
		// A nil pool panics inside the store. Reaching a panic means the
		// autonomous guard did not fire, which is what this asserts.
		_ = recover()
	}()
	_, err := tool.Execute(context.Background(), map[string]any{
		"pursuit_id": "11111111-1111-1111-1111-111111111111",
		"action":     pc.ActionEvidence,
		"kind":       "evidence",
		"body":       "Held the price on the intro call.",
	})
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unattended") {
		t.Fatalf("an interactive turn must not be refused as unattended: %v", err)
	}
}

// Both required arguments are validated before anything else, so a malformed
// call can never reach a write. Again asserted with a nil pool.
func TestPursuitPCWriteValidatesItsArguments(t *testing.T) {
	tool := &pursuitPCWrite{pool: nil}
	ctx := context.Background()

	if _, err := tool.Execute(ctx, map[string]any{"action": pc.ActionEvidence}); err == nil {
		t.Fatal("a call with no pursuit_id must be refused")
	} else if !strings.Contains(err.Error(), "pursuit_id") {
		t.Fatalf("error should name the missing field, got %v", err)
	}

	if _, err := tool.Execute(ctx, map[string]any{"pursuit_id": "x"}); err == nil {
		t.Fatal("a call with no action must be refused")
	} else if !strings.Contains(err.Error(), "action") {
		t.Fatalf("error should name the missing field, got %v", err)
	}
}

// The tool's action enum is what the model picks from, and the HTTP route uses
// the same strings as path suffixes. Generating the enum from pc.WriteActions
// is what keeps them in step - a hand-written copy would drift silently.
func TestPursuitPCWriteSchemaEnumMatchesTheStore(t *testing.T) {
	schema := (&pursuitPCWrite{}).Schema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties object")
	}
	action, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatal("schema has no action property")
	}
	enum, ok := action["enum"].([]string)
	if !ok {
		t.Fatalf("action enum is %T, want []string", action["enum"])
	}
	if len(enum) != len(pc.WriteActions()) {
		t.Fatalf("enum has %d actions, the store accepts %d", len(enum), len(pc.WriteActions()))
	}
	for _, a := range enum {
		if !pc.IsWriteAction(a) {
			t.Fatalf("the model can choose action %q, which the store rejects", a)
		}
	}

	required, ok := schema["required"].([]string)
	if !ok || len(required) != 2 {
		t.Fatalf("required = %v, want pursuit_id and action", schema["required"])
	}
}

// pursuit_pc_state is a pure read. The registry's name heuristic does not
// recognise the "_state" suffix, so without the explicit declaration this read
// would be bucketed with the mutating tools and gated like one.
func TestPursuitPCStateDeclaresItselfReadOnly(t *testing.T) {
	if !(&pursuitPCState{}).ReadOnly() {
		t.Fatal("pursuit_pc_state must declare ReadOnly, the name heuristic will not infer it")
	}
	if (&pursuitPCState{}).Name() != "pursuit_pc_state" {
		t.Fatalf("name = %q", (&pursuitPCState{}).Name())
	}
	if (&pursuitPCWrite{}).Name() != "pursuit_pc_write" {
		t.Fatalf("name = %q", (&pursuitPCWrite{}).Name())
	}
}

// A nil pool must leave the coached tools unregistered rather than registering
// tools that panic the moment the model picks one.
func TestRegisterPursuitPCToolsIsSafeWithoutAPool(t *testing.T) {
	r := NewRegistry()
	RegisterPursuitPCTools(r, nil)
	for _, name := range []string{"pursuit_pc_state", "pursuit_pc_write"} {
		if _, ok := r.Get(name); ok {
			t.Fatalf("%s registered without a database behind it", name)
		}
	}
	RegisterPursuitPCTools(nil, nil) // must not panic
}
