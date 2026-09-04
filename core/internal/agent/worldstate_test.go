package agent

import (
	"strings"
	"testing"
)

func TestSplitWorldSectionsLiftsTaggedBlocks(t *testing.T) {
	text := "<current_time>now</current_time>\n\n<tool_catalog>\n- a\n</tool_catalog>\n\n## Explicit\nfacts\n\n<bridge>\n## Bridge\nStatus: ok\n</bridge>"
	rest, sections, order := splitWorldSections(text)
	if _, ok := sections["tool_catalog"]; !ok {
		t.Fatalf("catalog not lifted: %v", sections)
	}
	if _, ok := sections["bridge"]; !ok {
		t.Fatalf("bridge not lifted: %v", sections)
	}
	if strings.Contains(rest, "tool_catalog") || strings.Contains(rest, "## Bridge") {
		t.Fatalf("lifted blocks must leave the per-turn text:\n%s", rest)
	}
	if !strings.Contains(rest, "<current_time>") || !strings.Contains(rest, "facts") {
		t.Fatalf("per-turn text lost:\n%s", rest)
	}
	if strings.Join(order, ",") != "tool_catalog,bridge" {
		t.Fatalf("order: %v", order)
	}
}

func TestWorldStateMessageFullThenDiffThenSilent(t *testing.T) {
	snap := &worldSnapshot{}
	sec := map[string]string{"tool_catalog": "<tool_catalog>\n- a\n</tool_catalog>", "bridge": "<bridge>mac</bridge>"}
	first := worldStateMessage(snap, sec)
	if !strings.HasPrefix(first, "<world_state>") || !strings.Contains(first, "- a") || !strings.Contains(first, "mac") {
		t.Fatalf("first send must be the full block:\n%s", first)
	}
	if again := worldStateMessage(snap, sec); again != "" {
		t.Fatalf("unchanged world must be silent, got:\n%s", again)
	}
	sec["bridge"] = "<bridge>cloud</bridge>"
	diff := worldStateMessage(snap, sec)
	if !strings.HasPrefix(diff, "<world_state_update>") || !strings.Contains(diff, "cloud") || strings.Contains(diff, "- a") {
		t.Fatalf("diff must carry only the changed section:\n%s", diff)
	}
	delete(sec, "bridge")
	gone := worldStateMessage(snap, sec)
	if !strings.Contains(gone, `<bridge removed="true"/>`) {
		t.Fatalf("a vanished section must be announced:\n%s", gone)
	}
	if again := worldStateMessage(snap, sec); again != "" {
		t.Fatalf("silent again after the removal, got:\n%s", again)
	}
}

func TestWorldStateMessageEmptyWorldStaysSilent(t *testing.T) {
	snap := &worldSnapshot{}
	if got := worldStateMessage(snap, map[string]string{}); got != "" {
		t.Fatalf("nothing to say, got %q", got)
	}
	if snap.sent {
		t.Fatal("an empty world is not a send")
	}
}
