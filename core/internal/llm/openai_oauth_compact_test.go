package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildResponsesInputPreservesRawResponseItem(t *testing.T) {
	raw := json.RawMessage(`{"id":"ci_123","type":"message","role":"user","content":[{"type":"input_text","text":"canonical"}]}`)
	msg := WithRawResponseItem(Message{Role: RoleUser, Content: "flattened fallback"}, raw)

	input := buildResponsesInput([]Message{msg})
	if len(input) != 1 {
		t.Fatalf("expected one input item, got %d", len(input))
	}

	encoded, err := json.Marshal(input[0])
	if err != nil {
		t.Fatalf("marshal preserved input: %v", err)
	}
	got := string(encoded)
	if !strings.Contains(got, `"id":"ci_123"`) {
		t.Fatalf("raw response item id was not preserved: %s", got)
	}
	if strings.Contains(got, "flattened fallback") {
		t.Fatalf("raw response item should win over flattened message fields: %s", got)
	}
}

func TestResponseItemToMessagePreservesRawOutputMessage(t *testing.T) {
	raw := json.RawMessage(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"compact answer"}]}`)

	msg := responseItemToMessage(raw)
	if msg.Role != RoleAssistant {
		t.Fatalf("expected assistant role, got %q", msg.Role)
	}
	if msg.Content != "compact answer" {
		t.Fatalf("expected output text, got %q", msg.Content)
	}

	preserved, ok := RawResponseItem(msg)
	if !ok {
		t.Fatal("expected raw response item metadata")
	}
	if string(preserved) != string(raw) {
		t.Fatalf("raw item changed:\nwant %s\n got %s", raw, preserved)
	}
}

func TestResponseItemToMessagePreservesReasoningOnlyItem(t *testing.T) {
	raw := json.RawMessage(`{"id":"rs_1","type":"reasoning","summary":[]}`)

	msg := responseItemToMessage(raw)
	if msg.Role != RoleSystem {
		t.Fatalf("expected system placeholder role, got %q", msg.Role)
	}
	if msg.Content != "" {
		t.Fatalf("reasoning placeholder should not invent content, got %q", msg.Content)
	}
	if _, ok := RawResponseItem(msg); !ok {
		t.Fatal("expected raw response item metadata")
	}
}
