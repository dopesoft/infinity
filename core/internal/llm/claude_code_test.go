package llm

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// fakeBrain records what the harness was asked to run.
type fakeBrain struct {
	turns []BrainTurn
	reply string
	// session is the Claude Code session id the harness "discovers" mid-run.
	session string
}

func (f *fakeBrain) Converse(ctx context.Context, turn BrainTurn, out chan<- StreamEvent) (Response, error) {
	if f.session != "" && turn.OnSession != nil {
		turn.OnSession(f.session)
	}
	f.turns = append(f.turns, turn)
	return Response{Text: f.reply, StopReason: "end_turn"}, nil
}

// memStore is a BrainSessionStore that lives for one test.
type memStore map[string]string

func (m memStore) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := m[key]
	return v, ok, nil
}
func (m memStore) Set(_ context.Context, key, value string) error {
	m[key] = value
	return nil
}

// The invariant this whole provider exists to protect: every turn of one
// conversation resumes the SAME Claude Code session, and a resumed turn sends
// ONLY the new message.
//
// If this breaks, nothing errors and no test elsewhere fails - the boss just
// silently starts paying full price for his entire history on every message,
// because a cold start re-reads it all instead of hitting the subscription's
// one-hour prompt cache. That is why it is asserted directly.
func TestClaudeCodeResumesOneSessionPerConversation(t *testing.T) {
	brain := &fakeBrain{reply: "done", session: "claude-abc"}
	store := memStore{}
	p := NewClaudeCode(brain, store, "opus")

	ctx := WithCacheKey(context.Background(), "session-1")
	sys := SystemPrompt{Stable: "SOUL", Volatile: "<current_time>now</current_time>"}

	// Turn one: nothing to resume, so the system prompt has to travel.
	if _, err := p.StreamCached(ctx, "", sys, []Message{{Role: RoleUser, Content: "first question"}}, nil, nil); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if len(brain.turns) != 1 {
		t.Fatalf("want 1 turn, got %d", len(brain.turns))
	}
	first := brain.turns[0]
	if first.Resume != "" {
		t.Errorf("first turn should start cold, got resume=%q", first.Resume)
	}
	if !strings.Contains(first.Prompt, "SOUL") {
		t.Error("a cold start must carry the system prompt; the session has nothing else to learn it from")
	}
	if !strings.Contains(first.Prompt, "first question") {
		t.Error("cold start dropped the boss's actual message")
	}

	// Turn two: the session id was learned mid-run, so this must resume it.
	if _, err := p.StreamCached(ctx, "", sys, []Message{
		{Role: RoleUser, Content: "first question"},
		{Role: RoleAssistant, Content: "done"},
		{Role: RoleUser, Content: "second question"},
	}, nil, nil); err != nil {
		t.Fatalf("second turn: %v", err)
	}
	second := brain.turns[1]
	if second.Resume != "claude-abc" {
		t.Fatalf("second turn must resume the same Claude session, got %q", second.Resume)
	}
	if strings.Contains(second.Prompt, "SOUL") {
		t.Error("a resumed turn re-sent the system prompt, which breaks the cached prefix it exists to preserve")
	}
	if strings.Contains(second.Prompt, "first question") {
		t.Error("a resumed turn replayed history Claude Code already holds")
	}
	if !strings.Contains(second.Prompt, "second question") {
		t.Errorf("resumed turn dropped the boss's message: %q", second.Prompt)
	}
	// This turn's volatile context HAS to travel, or the brain stops seeing
	// anything memory retrieved after the conversation started.
	if !strings.Contains(second.Prompt, "<current_time>") {
		t.Error("resumed turn dropped the per-turn context (retrieval, clock), so freshly recalled memory never reaches it")
	}
}

// The mapping has to survive a Core restart, or a redeploy mid-conversation
// silently drops the boss back to a cold, context-free session.
func TestClaudeCodeResumeSurvivesRestart(t *testing.T) {
	store := memStore{}
	first := NewClaudeCode(&fakeBrain{session: "claude-xyz"}, store, "opus")
	ctx := WithCacheKey(context.Background(), "session-2")
	if _, err := first.StreamCached(ctx, "", SystemPrompt{}, []Message{{Role: RoleUser, Content: "hi"}}, nil, nil); err != nil {
		t.Fatalf("first turn: %v", err)
	}

	// A brand new provider, as after a restart: no warm map, only the store.
	brain := &fakeBrain{}
	restarted := NewClaudeCode(brain, store, "opus")
	if _, err := restarted.StreamCached(ctx, "", SystemPrompt{}, []Message{{Role: RoleUser, Content: "still there?"}}, nil, nil); err != nil {
		t.Fatalf("after restart: %v", err)
	}
	if brain.turns[0].Resume != "claude-xyz" {
		t.Errorf("restart lost the conversation's Claude session: got %q", brain.turns[0].Resume)
	}
}

// A brain with no harness must not masquerade as a working one: Settings reads
// Implemented() to decide whether to offer the vendor at all.
func TestClaudeCodeWithoutHarnessIsNotOffered(t *testing.T) {
	p := NewClaudeCode(nil, memStore{}, "")
	if p.Implemented() {
		t.Fatal("a provider with no Mac harness reported itself usable")
	}
	if _, err := p.StreamCached(context.Background(), "", SystemPrompt{}, []Message{{Role: RoleUser, Content: "hi"}}, nil, nil); err == nil {
		t.Fatal("want an error when there is no harness, got a silent success")
	}
}

// rejectingStore is settings.Store's contract: it refuses any key that does
// not start with "setting.". This is not a hypothetical - it is the real
// store this provider is wired to in serve.go.
type rejectingStore struct{ mem map[string]string }

func (r rejectingStore) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := r.mem[key]
	return v, ok, nil
}
func (r rejectingStore) Set(_ context.Context, key, value string) error {
	if !strings.HasPrefix(key, "setting.") {
		return fmt.Errorf("settings.Set: key %q must use \"setting.\" prefix", key)
	}
	r.mem[key] = value
	return nil
}

// Why: the handle was written under a key the store rejects, and the error was
// swallowed as best-effort. So it was never persisted, and every conversation
// started COLD after a restart: the whole transcript re-read, the subscription's
// prompt cache thrown away, the boss watching a spinner. A resumed turn finished
// in 33s where a cold one took 1m36s and up. Nothing failed loudly enough for
// anyone to notice for as long as this path has existed.
func TestTheSessionHandleIsActuallyPersisted(t *testing.T) {
	store := rejectingStore{mem: map[string]string{}}
	brain := &fakeBrain{reply: "done", session: "claude-abc"}
	p := NewClaudeCode(brain, store, "opus")

	ctx := WithCacheKey(context.Background(), "session-persist")
	if _, err := p.StreamCached(ctx, "", SystemPrompt{}, []Message{{Role: RoleUser, Content: "hi"}}, nil, nil); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if len(store.mem) == 0 {
		t.Fatal("the handle was never stored, so every turn after a restart starts cold")
	}

	// A fresh provider, as after a redeploy: only the store remains.
	restarted := NewClaudeCode(&fakeBrain{}, store, "opus")
	if got := restarted.resume(ctx, "session-persist", nil); got != "claude-abc" {
		t.Fatalf("a restart lost the conversation's Claude session: got %q", got)
	}
}
