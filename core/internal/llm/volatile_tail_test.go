package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// The volatile segment is per-turn content (retrieved memory, tool catalog,
// current time). It MUST NOT sit anywhere inside the cacheable prefix, because
// OpenAI prices a cache read at a tenth of an uncached token and the prefix is
// matched from byte zero. These tests pin the invariant that actually saves the
// money: two turns whose volatile content differs must still present a
// byte-identical prefix (instructions + tools + prior history).

func volatileTailText(t *testing.T, item any) string {
	t.Helper()
	m, ok := item.(map[string]any)
	if !ok {
		t.Fatalf("tail item is %T, want map", item)
	}
	if m["role"] != "developer" {
		t.Fatalf("tail role = %v, want developer", m["role"])
	}
	content, ok := m["content"].([]map[string]any)
	if !ok || len(content) != 1 {
		t.Fatalf("tail content = %#v, want one block", m["content"])
	}
	if content[0]["type"] != "input_text" {
		t.Fatalf("tail block type = %v, want input_text", content[0]["type"])
	}
	return content[0]["text"].(string)
}

func TestResponsesPrefixIsIdenticalAcrossTurnsWithDifferentVolatile(t *testing.T) {
	stable := "SOUL: you are Jarvis."
	history := []Message{
		{Role: RoleUser, Content: "turn one"},
		{Role: RoleAssistant, Content: "answer one"},
	}
	tools := []ToolDef{{Name: "bash_run", Description: "run a command"}}

	a := buildResponsesRequest("gpt-5.6-sol", stable,
		SystemPrompt{Stable: stable, Volatile: "<current_time>09:00</current_time>"}.VolatileTail(),
		"sess-1", "", history, tools)
	b := buildResponsesRequest("gpt-5.6-sol", stable,
		SystemPrompt{Stable: stable, Volatile: "<current_time>11:47</current_time>"}.VolatileTail(),
		"sess-1", "", history, tools)

	// instructions leads the prefix and cannot carry a breakpoint: a volatile
	// byte here re-prices the system prompt, every tool schema and all history.
	if a["instructions"] != stable || b["instructions"] != stable {
		t.Fatalf("instructions must be the stable segment alone; got %q and %q",
			a["instructions"], b["instructions"])
	}
	at, _ := json.Marshal(a["tools"])
	bt, _ := json.Marshal(b["tools"])
	if string(at) != string(bt) {
		t.Errorf("tool schemas diverged across turns")
	}

	ai, bi := a["input"].([]any), b["input"].([]any)
	if len(ai) != len(history)+1 || len(bi) != len(history)+1 {
		t.Fatalf("input lengths = %d/%d, want history+1 tail", len(ai), len(bi))
	}
	// Everything ahead of the tail is the cacheable prefix.
	for i := range history {
		x, _ := json.Marshal(ai[i])
		y, _ := json.Marshal(bi[i])
		if string(x) != string(y) {
			t.Errorf("history item %d diverged across turns: %s vs %s", i, x, y)
		}
	}
	// ...and the thing that differs is the LAST item, where it costs nothing.
	if volatileTailText(t, ai[len(ai)-1]) == volatileTailText(t, bi[len(bi)-1]) {
		t.Errorf("volatile tail did not carry the per-turn content")
	}
	if !strings.Contains(volatileTailText(t, ai[len(ai)-1]), "09:00") {
		t.Errorf("volatile content missing from the tail item")
	}
}

func TestVolatileTailOmittedWhenEmpty(t *testing.T) {
	if got := (SystemPrompt{Stable: "s"}).VolatileTail(); got != "" {
		t.Errorf("VolatileTail() = %q, want empty", got)
	}
	body := buildResponsesRequest("gpt-5.6-sol", "s", "", "k", "", []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if n := len(body["input"].([]any)); n != 1 {
		t.Errorf("input has %d items, want 1 (no empty tail item)", n)
	}
}

func TestVolatileTailIsCaptionedSoTheModelDoesNotAnswerIt(t *testing.T) {
	// A trailing block of retrieved memory is the LAST thing the model reads.
	// Without framing it can be taken as the live request.
	got := SystemPrompt{Volatile: "<memory>x</memory>"}.VolatileTail()
	if !strings.HasPrefix(got, volatileTailCaption) {
		t.Errorf("volatile tail is not captioned: %q", got)
	}
	if !strings.Contains(got, "<memory>x</memory>") {
		t.Errorf("caption dropped the volatile content")
	}
}

func TestDeveloperRoleRejectionDetectorStaysNarrow(t *testing.T) {
	// Must fire only for the tail item, so an unrelated 400 still surfaces as a
	// real error rather than being silently retried into a different shape.
	for _, s := range []string{
		`{"error":{"message":"Invalid value: 'developer'. Supported values are: 'system', 'user'","param":"input[2].role"}}`,
		`{"error":{"message":"Unsupported role: developer"}}`,
	} {
		if !looksLikeDeveloperRoleRejection(s) {
			t.Errorf("should detect tail rejection: %s", s)
		}
	}
	for _, s := range []string{
		`{"error":{"message":"Unsupported tool type: web_search_preview"}}`,
		`{"error":{"message":"The 'gpt-5' model is not supported when using Codex with a ChatGPT account."}}`,
		`{"error":{"message":"Invalid value: 'xhigh' for reasoning.effort"}}`,
	} {
		if looksLikeDeveloperRoleRejection(s) {
			t.Errorf("must NOT swallow unrelated 400: %s", s)
		}
	}
}

// TestChatCompletionsPutsVolatileLast covers the Chat Completions path, which
// serves OpenAI AND DeepSeek (deepseek.go returns this same struct).
func TestChatCompletionsPutsVolatileLast(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := &OpenAI{
		client: openai.NewClient(option.WithAPIKey("test"), option.WithBaseURL(srv.URL)),
		model:  "gpt-4o-mini",
		name:   "openai",
	}
	out := make(chan StreamEvent, 64)
	go func() {
		for range out {
		}
	}()
	_, err := p.StreamCached(t.Context(), "gpt-4o-mini",
		SystemPrompt{Stable: "SOUL", Volatile: "<current_time>11:47</current_time>"},
		[]Message{{Role: RoleUser, Content: "hi"}}, nil, out)
	close(out)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(captured, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("got %d messages, want stable + user + volatile tail", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "SOUL" {
		t.Errorf("leading system message must be the stable segment alone, got %v", req.Messages[0])
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "system" {
		t.Errorf("tail role = %q, want system (DeepSeek does not document `developer`)", last.Role)
	}
	if s, _ := last.Content.(string); !strings.Contains(s, "11:47") {
		t.Errorf("volatile content is not in the tail message: %v", last.Content)
	}
}

// Anthropic hashes the prefix tools -> system -> messages, so a volatile block
// in the system slot invalidates the walking message breakpoint every turn:
// tools and stable system still read from cache, but the whole transcript is
// rewritten at full price. It has to ride past the breakpoint instead.
func TestAnthropicVolatileRidesPastTheWalkingBreakpoint(t *testing.T) {
	a := NewAnthropic("test-key", "claude-opus-5")
	sys := SystemPrompt{Stable: "SOUL", Volatile: "<current_time>11:47</current_time>"}
	msgs := []Message{
		{Role: RoleUser, Content: "turn one"},
		{Role: RoleAssistant, Content: "answer one"},
		{Role: RoleUser, Content: "turn two"},
	}
	params := a.buildParams(t.Context(), "claude-opus-5", sys, msgs, []ToolDef{{Name: "bash_run"}})

	if len(params.System) != 1 {
		t.Fatalf("system has %d blocks, want stable alone (volatile moved onto the transcript)", len(params.System))
	}
	if params.System[0].Text != "SOUL" || params.System[0].CacheControl.Type == "" {
		t.Errorf("stable system block must carry the breakpoint, got %#v", params.System[0])
	}

	last := params.Messages[len(params.Messages)-1]
	blocks := last.Content
	if len(blocks) != 2 {
		t.Fatalf("last user message has %d blocks, want the turn text + the volatile tail", len(blocks))
	}
	// The breakpoint must sit on the REAL content, with volatile after it, or
	// the cached prefix would include this turn's changing bytes.
	if cc := blocks[0].GetCacheControl(); cc == nil || cc.Type == "" {
		t.Errorf("walking breakpoint is not on the real content block")
	}
	if cc := blocks[1].GetCacheControl(); cc != nil && cc.Type != "" {
		t.Errorf("volatile block must NOT carry the breakpoint")
	}
	if got := blocks[1].OfText; got == nil || !strings.Contains(got.Text, "11:47") {
		t.Errorf("volatile content is not on the trailing block: %#v", blocks[1])
	}
}

// When there is no user message to carry it, volatile stays in the system slot:
// appending to an assistant message would read as words the model just said.
func TestAnthropicVolatileStaysInSystemWithoutAUserMessage(t *testing.T) {
	a := NewAnthropic("test-key", "claude-opus-5")
	params := a.buildParams(t.Context(), "claude-opus-5",
		SystemPrompt{Stable: "SOUL", Volatile: "VOL"},
		[]Message{{Role: RoleAssistant, Content: "only an assistant turn"}}, nil)
	if len(params.System) != 2 || params.System[1].Text != "VOL" {
		t.Errorf("volatile should fall back to the system slot, got %#v", params.System)
	}
}
