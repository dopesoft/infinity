package tools

import (
	"context"
	"strings"
	"testing"
)

// A refused mem_* call must carry its own correction. The 2026-09-04 turn:
// ChatGPT called mem_act on a table that does not exist with no ids, got a
// bare "does not exist", and retried the identical call until the loop guard
// blocked it. Every refusal here is judged before the database is touched
// (the pool is nil on purpose: a check that slid below the DB call would
// panic instead of passing) and says what would have been right.

func TestMemAct_NoIDsIsRefusedBeforeTheDatabaseAndSaysWhereIDsComeFrom(t *testing.T) {
	tool := &memAct{pool: nil}
	out, err := tool.Execute(context.Background(), map[string]any{"table": "mem_followups", "action": "done", "ids": []any{}})
	if err == nil || out != "" {
		t.Fatalf("expected a refusal, got out=%q err=%v", out, err)
	}
	if !strings.Contains(err.Error(), "mem_list") || !strings.Contains(err.Error(), "mem_followups") {
		t.Fatalf("the refusal must say to read ids with mem_list on that table; got %q", err)
	}
}

func TestMemAct_NoActionIsRefusedBeforeTheDatabaseAndNamesActionList(t *testing.T) {
	tool := &memAct{pool: nil}
	_, err := tool.Execute(context.Background(), map[string]any{"table": "mem_followups", "ids": []any{"a"}})
	if err == nil || !strings.Contains(err.Error(), "action_list") {
		t.Fatalf("the refusal must point at action_list; got %v", err)
	}
}

// A single id sent as a string IS an id. Refusing it for "no ids" is the
// refusal a model retries unchanged.
func TestMemAct_AStringIDGetsPastTheIDsCheck(t *testing.T) {
	tool := &memAct{pool: nil}
	defer func() {
		if recover() == nil {
			t.Fatal("the call never reached the database, so the ids check refused a real id")
		}
	}()
	_, err := tool.Execute(context.Background(), map[string]any{"table": "mem_followups", "action": "done", "ids": "0b1c-2d3e"})
	t.Fatalf("expected the nil pool to be reached (panic), got err=%v", err)
}

func TestIDsFromInput(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{[]any{"a", " b ", ""}, 2},
		{[]string{"a"}, 1},
		{"a", 1},
		{"a, b  c\nd", 4},
		{"", 0},
		{nil, 0},
		{42, 0},
	}
	for _, c := range cases {
		if got := len(idsFromInput(c.in)); got != c.want {
			t.Fatalf("idsFromInput(%v) = %d ids, want %d", c.in, got, c.want)
		}
	}
}

func TestActionRegister_BadOpIsRefusedBeforeTheDatabase(t *testing.T) {
	tool := &actionRegister{pool: nil}
	_, err := tool.Execute(context.Background(), map[string]any{"table": "mem_followups", "action": "x", "op": "delete", "column": "status"})
	if err == nil || !strings.Contains(err.Error(), "set_status|set_timestamp|set_null|set_bool") {
		t.Fatalf("a bad op must list the valid ops before touching the pool; got %v", err)
	}
}

func TestRefusalMessagesCarryTheCorrection(t *testing.T) {
	if e := missingTableError("mem_unused", []string{"mem_followups", "mem_surface_items"}); !strings.Contains(e.Error(), "mem_followups, mem_surface_items") {
		t.Fatalf("missing table must list the tables: %v", e)
	}
	if e := missingTableError("mem_unused", nil); !strings.Contains(e.Error(), "system_map") {
		t.Fatalf("with no list the refusal must still say where to look: %v", e)
	}
	if e := unknownColumnError("mem_followups", "stat", []string{"id", "status"}); !strings.Contains(e.Error(), "id, status") {
		t.Fatalf("unknown column must list the columns: %v", e)
	}
	if e := emptyIDsError("mem_followups"); !strings.Contains(e.Error(), `mem_list({table:"mem_followups"})`) {
		t.Fatalf("empty ids must name the exact mem_list call: %v", e)
	}
}
