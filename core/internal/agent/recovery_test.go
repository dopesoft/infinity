package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/tools"
)

// pickyProvider refuses any request that still carries the thing it dislikes,
// the way a real vendor does: a flat 400 that says nothing about the boss's
// question. It answers normally once the request is clean.
type pickyProvider struct {
	mu     sync.Mutex
	calls  int
	seen   [][]llm.Message
	reject func([]llm.Message) error
}

func (p *pickyProvider) Name() string  { return "picky" }
func (p *pickyProvider) Model() string { return "picky-1" }
func (p *pickyProvider) Stream(_ context.Context, _, _ string, msgs []llm.Message, _ []llm.ToolDef, _ chan<- llm.StreamEvent) (llm.Response, error) {
	p.mu.Lock()
	p.calls++
	p.seen = append(p.seen, append([]llm.Message(nil), msgs...))
	p.mu.Unlock()
	if err := p.reject(msgs); err != nil {
		return llm.Response{}, err
	}
	return llm.Response{Text: "Here is your tailored resume."}, nil
}

func runTurn(t *testing.T, l *Loop, sessionID, msg string) (string, string) {
	t.Helper()
	out := make(chan RunEvent, 256)
	var wg sync.WaitGroup
	var lastErr string
	wg.Add(1)
	go drain(out, &wg, &lastErr)
	var text strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
	}()
	err := l.Run(context.Background(), sessionID, msg, "", nil, out)
	close(out)
	wg.Wait()
	<-done
	if err != nil {
		return "", lastErr + " | run err: " + err.Error()
	}
	return text.String(), lastErr
}

// Why: this is the boss's actual thread. He attached his resume and asked for
// a job-hunting workflow, and six turns in a row died - a file block one
// vendor rejects, a tool call an earlier crash left unanswered which then
// poisoned every turn after it, a session handle that had gone stale. Not one
// of them was about what he asked for, and not one of them recovered.
//
// The rule this asserts is the whole answer to that: a request a brain refuses
// is rebuilt into the form every brain accepts and asked AGAIN, once, before
// he is ever shown a failure. No vendor's wording appears anywhere in it.
func TestTurnRecoversFromARefusedRequest(t *testing.T) {
	reg := tools.NewRegistry()
	prov := &pickyProvider{
		reject: func(msgs []llm.Message) error {
			for _, m := range msgs {
				if len(m.Attachments) > 0 {
					return errors.New(`400 Bad Request {"message":".messages[1]: file must have a file_id or file_data"}`)
				}
			}
			return nil
		},
	}
	l := New(Config{LLM: prov, Tools: reg})

	// His resume, attached, exactly as the composer hands it over.
	s := l.GetOrCreateSession("resume-session")
	s.Append(llm.Message{
		Role:    llm.RoleUser,
		Content: "This is my resume",
		Attachments: []llm.Attachment{{
			Name: "resume.pdf",
			MIME: "application/pdf",
			Kind: llm.AttachmentDocument,
			Text: "KAI — Head of Product",
		}},
	})

	if _, errText := runTurn(t, l, "resume-session", "tailor this for VP of Product roles"); errText != "" {
		t.Fatalf("the boss was shown a failure instead of an answer: %s", errText)
	}
	if prov.calls != 2 {
		t.Fatalf("want one refusal then one clean retry, got %d calls", prov.calls)
	}
	// The retry must still carry his resume - recovering by quietly dropping
	// what he attached would be worse than the error.
	last := prov.seen[len(prov.seen)-1]
	var body strings.Builder
	for _, m := range last {
		body.WriteString(m.Content)
	}
	if !strings.Contains(body.String(), "KAI — Head of Product") {
		t.Fatalf("the retry dropped the file's content instead of carrying it as text: %q", body.String())
	}
	if !strings.Contains(body.String(), "resume.pdf") {
		t.Fatalf("the retry lost the file's name, so the brain cannot open it: %q", body.String())
	}
}

// Why: an interrupted turn leaves a tool call with no result under it, and
// every vendor then refuses the whole conversation - not just that turn, every
// turn after it. That is a thread that never works again until someone
// notices, which is exactly what happened on 2026-08-31 ("No tool output found
// for function call call_ceHuo…", twice in a row).
func TestTurnRecoversFromAPoisonedHistory(t *testing.T) {
	reg := tools.NewRegistry()
	prov := &pickyProvider{
		reject: func(msgs []llm.Message) error {
			answered := map[string]bool{}
			for _, m := range msgs {
				if m.Role == llm.RoleTool {
					answered[m.ToolCallID] = true
				}
			}
			for _, m := range msgs {
				for _, c := range m.ToolCalls {
					if !answered[c.ID] {
						return errors.New(`400 {"message":"No tool output found for function call ` + c.ID + `"}`)
					}
				}
			}
			return nil
		},
	}
	l := New(Config{LLM: prov, Tools: reg})

	s := l.GetOrCreateSession("poisoned")
	s.Append(llm.Message{Role: llm.RoleUser, Content: "earlier question"})
	s.Append(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_dead", Name: "noop"}}})

	if _, errText := runTurn(t, l, "poisoned", "where are we then?"); errText != "" {
		t.Fatalf("a thread poisoned by an old crash stayed dead: %s", errText)
	}
	if prov.calls != 2 {
		t.Fatalf("want one refusal then one clean retry, got %d calls", prov.calls)
	}
}

// Why: retrying a failure that a second attempt cannot fix just makes him wait
// twice to read the same sentence.
func TestTurnDoesNotRetryWhatCannotBeFixed(t *testing.T) {
	reg := tools.NewRegistry()
	prov := &pickyProvider{
		reject: func([]llm.Message) error {
			return llm.Unrecoverable(errors.New("the ChatGPT plus plan's usage allowance is spent"))
		},
	}
	l := New(Config{LLM: prov, Tools: reg})
	s := l.GetOrCreateSession("spent")
	s.Append(llm.Message{Role: llm.RoleUser, Content: "hi", Attachments: []llm.Attachment{{Name: "x.pdf", Kind: llm.AttachmentDocument, Text: "t"}}})

	_, errText := runTurn(t, l, "spent", "go")
	if errText == "" {
		t.Fatal("a spent plan must be reported, not swallowed")
	}
	if prov.calls != 1 {
		t.Fatalf("a hopeless failure was retried anyway (%d calls)", prov.calls)
	}
}

// namedProvider reports a model id and nothing else. Enough to ask what the
// compaction point should be for the brain that is answering.
type namedProvider struct{ model string }

func (n *namedProvider) Name() string  { return "named" }
func (n *namedProvider) Model() string { return n.model }
func (n *namedProvider) Stream(context.Context, string, string, []llm.Message, []llm.ToolDef, chan<- llm.StreamEvent) (llm.Response, error) {
	return llm.Response{}, nil
}

// Why: the boss switches brains inside a live conversation, and the point at
// which his thread starts getting summarised away has to move with the brain.
// A fixed 120K is 60% of a 200K window and 12% of the 1M one his plan runs, so
// on the big brain his conversation was being compacted with 88% of the room
// still free, and switching DOWN left it compacting far too late.
func TestCompactionPointFollowsTheBrain(t *testing.T) {
	l := New(Config{LLM: &namedProvider{model: "claude-haiku-4-5"}, Tools: tools.NewRegistry()})
	if got := l.compactAt(); got != 120_000 {
		t.Errorf("a 200K brain should compact at 60%% (120K), got %d", got)
	}

	// Same conversation, he switches to the plan brain. Note the model id is
	// the tier alias the runtime actually reports.
	l.SetProvider(&namedProvider{model: "opus[1m]"})
	if got := l.compactAt(); got != 600_000 {
		t.Errorf("a 1M brain should compact at 60%% (600K), got %d", got)
	}

	// A number he set himself outranks anything derived.
	t.Setenv("INFINITY_AUTO_COMPACT_AT", "42000")
	pinned := New(Config{LLM: &namedProvider{model: "opus[1m]"}, Tools: tools.NewRegistry()})
	if got := pinned.compactAt(); got != 42_000 {
		t.Errorf("his own setting must win, got %d", got)
	}
}

// harnessProvider is a brain that runs its OWN tools: it streams what it did
// and returns a finished answer with no tool calls for the loop to execute.
// Claude Code is one; the shape, not the vendor, is what matters here.
type harnessProvider struct{ did []string }

func (h *harnessProvider) Name() string       { return "harness" }
func (h *harnessProvider) Model() string      { return "harness-1" }
func (h *harnessProvider) RunsOwnTools() bool { return true }
func (h *harnessProvider) Stream(_ context.Context, _, _ string, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.StreamEvent) (llm.Response, error) {
	for _, name := range h.did {
		out <- llm.StreamEvent{Kind: llm.StreamToolCall, ToolCall: &llm.ToolCall{
			ID: "brain-" + name, Name: name,
		}}
	}
	// The reply comes after the work, which is the order it happens in.
	out <- llm.StreamEvent{Kind: llm.StreamText, TextDelta: "read your resume and drafted the letter"}
	return llm.Response{Text: "read your resume and drafted the letter"}, nil
}

// Why: when the brain is a harness, everything Jarvis actually DID happened
// inside the vendor's session. The events were already streamed "for the boss's
// activity ledger" and nothing consumed them, so reading his resume, running a
// command and writing a file reached neither his screen nor Infinity's memory.
//
// And they have to land IN ORDER. Recording them after the stream closed put
// the tool rows after the reply, and Studio, seeing a tool row last, told him
// "Tools ran above but Jarvis didn't follow up with a reply" while the answer
// sat right above it. A real turn on 2026-09-01 05:23 that took 90 seconds and
// produced 3,848 tokens of answer read as dead.
func TestWorkTheBrainDidItselfIsSurfacedInOrder(t *testing.T) {
	l := New(Config{
		LLM:   &harnessProvider{did: []string{"Read", "Bash", "Write"}},
		Tools: tools.NewRegistry(),
	})

	out := make(chan RunEvent, 256)
	var kinds []string
	var seen []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range out {
			switch ev.Kind {
			case EventToolCall:
				if ev.ToolCall != nil {
					seen = append(seen, ev.ToolCall.Name)
					kinds = append(kinds, "tool")
				}
			case EventDelta:
				kinds = append(kinds, "text")
			}
		}
	}()
	if err := l.Run(context.Background(), "harness-session", "tailor my resume", "", nil, out); err != nil {
		t.Fatalf("run: %v", err)
	}
	close(out)
	<-done

	if strings.Join(seen, ",") != "Read,Bash,Write" {
		t.Fatalf("the brain's own work never reached the ledger: %v", seen)
	}
	if got := strings.Join(kinds, ","); got != "tool,tool,tool,text" {
		t.Fatalf("the work must land where it happened, before the reply, got %q", got)
	}
}

// twoSegmentProvider answers, then (because the loop asks it to keep going)
// answers again with something much shorter. That is what a self-heal or
// plan-continuation pass looks like from the transcript's point of view.
type twoSegmentProvider struct {
	calls  int
	toolID string
}

func (p *twoSegmentProvider) Name() string  { return "two-segment" }
func (p *twoSegmentProvider) Model() string { return "two-segment-1" }
func (p *twoSegmentProvider) Stream(_ context.Context, _, _ string, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.StreamEvent) (llm.Response, error) {
	p.calls++
	if p.calls == 1 {
		// A real answer, delivered alongside a tool call so the loop keeps
		// going rather than ending the turn here.
		return llm.Response{
			Text:      "the long strategy answer he actually read",
			ToolCalls: []llm.ToolCall{{ID: p.toolID, Name: "noop", Input: map[string]any{}}},
		}, nil
	}
	return llm.Response{Text: "Nothing broke, boss. Waiting on your call."}, nil
}

// Why: he read a full answer, went to Settings to connect LinkedIn, came back,
// and it had been replaced by a one-liner about waiting on his decision. The
// transcript is rebuilt from what was written down, and only the turn's FINAL
// text was ever written down, so every earlier message he had actually been
// shown existed in his browser and nowhere else. 2026-09-01, and reported once
// before on 2026-08-26.
func TestEveryAssistantMessageHeSawIsWrittenDown(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&countingTool{})
	rec := &recordingHooks{}
	l := New(Config{LLM: &twoSegmentProvider{toolID: "c1"}, Tools: reg, Hooks: rec})

	out := make(chan RunEvent, 256)
	var wg sync.WaitGroup
	var lastErr string
	wg.Add(1)
	go drain(out, &wg, &lastErr)
	if err := l.Run(context.Background(), "two-segment-session", "what about built-in?", "", nil, out); err != nil {
		t.Fatalf("run: %v", err)
	}
	close(out)
	wg.Wait()

	joined := strings.Join(rec.assistantText(), " | ")
	if !strings.Contains(joined, "the long strategy answer he actually read") {
		t.Fatalf("the answer he read was never written down, so a reload loses it: %q", joined)
	}
	if !strings.Contains(joined, "Nothing broke, boss") {
		t.Fatalf("the final message was lost: %q", joined)
	}
}

// recordingHooks captures what the loop wrote down, which is exactly what a
// reload rebuilds the conversation from.
type recordingHooks struct {
	mu   sync.Mutex
	rows []struct{ name, text string }
}

func (r *recordingHooks) Emit(name, _, _, text string, _ map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, struct{ name, text string }{name, text})
}

func (r *recordingHooks) assistantText() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, row := range r.rows {
		if row.name == "AssistantMessage" || row.name == "TaskCompleted" {
			out = append(out, row.text)
		}
	}
	return out
}
