package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The contract these pin: within one turn, every LLM call renders the SAME
// bytes up to the newest message. The per-turn context is attached to the
// user message that opened the turn (Message.Volatile) and never moves, so
// a tool round trip only appends. Before this, the block trailed the whole
// request and re-wrote the turn's tool traffic on every call.

func turnMessages(withToolRound bool) []Message {
	msgs := []Message{
		{Role: RoleUser, Content: "earlier question"},
		{Role: RoleAssistant, Content: "earlier answer"},
		{Role: RoleUser, Content: "do the thing", Volatile: "<current_time>now</current_time>\n\nretrieved: fact one"},
	}
	if withToolRound {
		msgs = append(msgs,
			Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "web_search", Input: map[string]any{"q": "x"}}}},
			Message{Role: RoleTool, ToolCallID: "c1", ToolName: "web_search", Content: "result"},
		)
	}
	return msgs
}

func TestResponsesPinnedContextKeepsThePrefixAcrossIterations(t *testing.T) {
	first := buildResponsesRequest("gpt-5.6-sol", "soul", "", "k", "", turnMessages(false), nil)
	second := buildResponsesRequest("gpt-5.6-sol", "soul", "", "k", "", turnMessages(true), nil)
	a := first["input"].([]any)
	b := second["input"].([]any)
	if len(b) <= len(a) {
		t.Fatalf("second call should only append: %d vs %d items", len(a), len(b))
	}
	for i := range a {
		ja, _ := json.Marshal(a[i])
		jb, _ := json.Marshal(b[i])
		if string(ja) != string(jb) {
			t.Fatalf("item %d changed between calls:\n%s\n%s", i, ja, jb)
		}
	}
	ju, _ := json.Marshal(a[len(a)-1])
	if !strings.Contains(string(ju), "retrieved: fact one") || !strings.Contains(string(ju), "do the thing") {
		t.Fatalf("pinned context must ride the user message itself:\n%s", ju)
	}
}

func TestAnthropicPinnedContextKeepsThePrefixAcrossIterations(t *testing.T) {
	a := &Anthropic{model: "claude-opus-5"}
	sys := SystemPrompt{Stable: "soul"}
	p1 := a.buildParams(context.Background(), "", sys, turnMessages(false), nil)
	p2 := a.buildParams(context.Background(), "", sys, turnMessages(true), nil)
	if len(p2.Messages) <= len(p1.Messages) {
		t.Fatalf("second call should only append")
	}
	for i := range p1.Messages {
		ja, _ := json.Marshal(p1.Messages[i])
		jb, _ := json.Marshal(p2.Messages[i])
		// The walking cache breakpoint legitimately moves off the last
		// message; strip it before comparing bytes.
		sa := strings.ReplaceAll(string(ja), `,"cache_control":{"type":"ephemeral"}`, "")
		sb := strings.ReplaceAll(string(jb), `,"cache_control":{"type":"ephemeral"}`, "")
		if sa != sb {
			t.Fatalf("message %d changed between calls:\n%s\n%s", i, sa, sb)
		}
	}
	ju, _ := json.Marshal(p1.Messages[len(p1.Messages)-1])
	if !strings.Contains(string(ju), "retrieved: fact one") {
		t.Fatalf("pinned context must ride the user message:\n%s", ju)
	}
}

func TestChatCompletionsPinnedContextRidesTheUserMessage(t *testing.T) {
	o := &OpenAI{model: "gpt-5"}
	m := o.userMessage(Message{Role: RoleUser, Content: "do the thing", Volatile: "retrieved: fact one"})
	j, _ := json.Marshal(m)
	if !strings.Contains(string(j), "do the thing") || !strings.Contains(string(j), "retrieved: fact one") {
		t.Fatalf("expected text + pinned context in one user message:\n%s", j)
	}
	if !strings.Contains(string(j), volatileMessageCaption) {
		t.Fatalf("pinned block must be captioned as reference:\n%s", j)
	}
}

func TestVolatileBlockEmptyWhenNothingPinned(t *testing.T) {
	if got := (Message{Role: RoleUser, Content: "hi"}).VolatileBlock(); got != "" {
		t.Fatalf("got %q", got)
	}
}
