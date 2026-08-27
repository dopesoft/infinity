package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Why: 2026-08-26. The boss's ChatGPT Plus plan hit usage_limit_reached with
// a 3.8h reset; the OAuth provider retried the dead request three times per
// call, three concurrent builds hammered it, and the chat said "brief server
// hiccup". ANTHROPIC_API_KEY was wired the whole time. These tests pin the
// contract: a spent plan is typed, is not retried, hands the call to a
// standby, tells the boss once, and comes back when the reset passes.

type fakeProvider struct {
	name   string
	model  string
	err    error
	text   string
	calls  int
	models []string
}

func (f *fakeProvider) Name() string  { return f.name }
func (f *fakeProvider) Model() string { return f.model }
func (f *fakeProvider) Stream(_ context.Context, model, _ string, _ []Message, _ []ToolDef, out chan<- StreamEvent) (Response, error) {
	f.calls++
	f.models = append(f.models, model)
	if f.err != nil {
		emit(out, StreamEvent{Kind: StreamError, Err: f.err.Error()})
		return Response{}, f.err
	}
	emit(out, StreamEvent{Kind: StreamText, TextDelta: f.text})
	emit(out, StreamEvent{Kind: StreamComplete, StopReason: "end_turn"})
	return Response{Text: f.text, StopReason: "end_turn"}, nil
}

func newFailoverFixture(t *testing.T, primaryErr error) (*failoverProvider, *fakeProvider, *fakeProvider) {
	t.Helper()
	ResetQuotaLedgerForTest()
	t.Cleanup(ResetQuotaLedgerForTest)
	// Standby is opt-in; these tests opt in explicitly. The default is none.
	t.Setenv("LLM_STANDBY_PROVIDERS", "anthropic")
	primary := &fakeProvider{name: "openai_oauth", model: "gpt-5.6-sol", err: primaryErr, text: "from gpt"}
	standby := &fakeProvider{name: "anthropic", model: "claude-sonnet-4-5-20250929", text: "from claude"}
	reg := NewRegistry()
	reg.providers[primary.name] = primary
	reg.providers[standby.name] = standby
	got, ok := reg.Get("openai_oauth")
	if !ok {
		t.Fatal("primary must resolve from the registry")
	}
	f, ok := got.(*failoverProvider)
	if !ok {
		t.Fatalf("Registry.Get must hand out the failover wrapper, got %T", got)
	}
	return f, primary, standby
}

func drain(out chan StreamEvent) []StreamEvent {
	close(out)
	var evs []StreamEvent
	for ev := range out {
		evs = append(evs, ev)
	}
	return evs
}

func kinds(evs []StreamEvent) string {
	var b strings.Builder
	for _, e := range evs {
		b.WriteString(string(e.Kind) + " ")
	}
	return b.String()
}

func TestFailover_SpentPlanHandsTheCallToTheStandby(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour)
	f, primary, standby := newFailoverFixture(t, &QuotaError{Provider: "openai_oauth", ResetsAt: reset, Detail: "plus plan spent"})

	out := make(chan StreamEvent, 64)
	resp, err := f.Stream(context.Background(), "gpt-5.6-sol", "sys", nil, nil, out)
	if err != nil {
		t.Fatalf("standby must carry the call, got %v", err)
	}
	if resp.Text != "from claude" || standby.calls != 1 || primary.calls != 1 {
		t.Fatalf("expected one primary attempt then the standby answer; primary=%d standby=%d text=%q", primary.calls, standby.calls, resp.Text)
	}
	// The standby must NOT be handed a foreign model id.
	if standby.models[0] != "" {
		t.Fatalf("standby got the primary's model %q; must fall back to its own default", standby.models[0])
	}
	evs := drain(out)
	// No spurious error reached the consumer; the boss got exactly one
	// notice naming the reset time, then the standby's content.
	if strings.Contains(kinds(evs), "error") {
		t.Fatalf("the primary's quota error leaked to the consumer: %s", kinds(evs))
	}
	var notices int
	for _, e := range evs {
		if e.Kind == StreamNotice {
			notices++
			if !strings.Contains(e.TextDelta, "out of usage") || !strings.Contains(e.TextDelta, "until") {
				t.Fatalf("notice must say the plan is out of usage and until when: %q", e.TextDelta)
			}
		}
	}
	if notices != 1 {
		t.Fatalf("expected exactly one switch notice, got %d: %s", notices, kinds(evs))
	}
	if _, _, spent := Exhausted("openai_oauth"); !spent {
		t.Fatal("primary must be recorded spent until its reset")
	}

	// Second call: straight to the standby, no doomed primary request, no
	// second notice (the chip carries the state between turns).
	out2 := make(chan StreamEvent, 64)
	if _, err := f.Stream(context.Background(), "gpt-5.6-sol", "sys", nil, nil, out2); err != nil {
		t.Fatal(err)
	}
	if primary.calls != 1 || standby.calls != 2 {
		t.Fatalf("known-spent primary must not be re-probed before its reset; primary=%d standby=%d", primary.calls, standby.calls)
	}
	if strings.Contains(kinds(drain(out2)), "notice") {
		t.Fatal("the switch notice must not repeat on every turn")
	}
}

func TestFailover_KeepsTheModelWhenItBelongsToTheStandbyFamily(t *testing.T) {
	f, _, standby := newFailoverFixture(t, &QuotaError{Provider: "openai_oauth", Detail: "spent"})
	out := make(chan StreamEvent, 64)
	if _, err := f.Stream(context.Background(), "claude-opus-4-7", "sys", nil, nil, out); err != nil {
		t.Fatal(err)
	}
	if standby.models[0] != "claude-opus-4-7" {
		t.Fatalf("a model id in the standby's own family must be kept, got %q", standby.models[0])
	}
	drain(out)
}

func TestFailover_OrdinaryErrorsAndHealthyCallsAreUntouched(t *testing.T) {
	boom := errors.New("openai_oauth: status=500 body=oops")
	f, primary, standby := newFailoverFixture(t, boom)
	out := make(chan StreamEvent, 64)
	_, err := f.Stream(context.Background(), "", "sys", nil, nil, out)
	if !errors.Is(err, boom) || standby.calls != 0 {
		t.Fatalf("a non-quota error must surface as-is with no failover; err=%v standby=%d", err, standby.calls)
	}
	if !strings.Contains(kinds(drain(out)), "error") {
		t.Fatal("the held error event must be flushed to the consumer when there is no failover")
	}

	primary.err = nil
	out = make(chan StreamEvent, 64)
	resp, err := f.Stream(context.Background(), "", "sys", nil, nil, out)
	if err != nil || resp.Text != "from gpt" || standby.calls != 0 {
		t.Fatalf("healthy primary must answer directly; err=%v text=%q standby=%d", err, resp.Text, standby.calls)
	}
	if k := kinds(drain(out)); !strings.Contains(k, "text") || !strings.Contains(k, "complete") {
		t.Fatalf("content and completion must reach the consumer in order: %s", k)
	}
}

func TestFailover_NoStandbySurfacesTheQuotaError(t *testing.T) {
	q := &QuotaError{Provider: "openai_oauth", Detail: "spent"}
	f, _, _ := newFailoverFixture(t, q)
	delete(f.reg.providers, "anthropic")
	out := make(chan StreamEvent, 64)
	_, err := f.Stream(context.Background(), "", "sys", nil, nil, out)
	if got, ok := AsQuota(err); !ok || got != q {
		t.Fatalf("with no standby the typed quota error must surface: %v", err)
	}
	if !strings.Contains(kinds(drain(out)), "error") {
		t.Fatal("the consumer must still see the error event")
	}
}

func TestFailover_ComesBackAfterTheReset(t *testing.T) {
	f, primary, standby := newFailoverFixture(t, nil)
	// Simulate a hold that has already expired.
	MarkExhausted("openai_oauth", time.Now().Add(time.Second), "spent")
	quotaMu.Lock()
	quotaExhausted["openai_oauth"] = quotaEntry{until: time.Now().Add(-time.Second), detail: "spent"}
	quotaMu.Unlock()

	out := make(chan StreamEvent, 64)
	resp, err := f.Stream(context.Background(), "", "sys", nil, nil, out)
	if err != nil || resp.Text != "from gpt" || primary.calls != 1 || standby.calls != 0 {
		t.Fatalf("after the reset the primary must answer again; err=%v text=%q primary=%d standby=%d", err, resp.Text, primary.calls, standby.calls)
	}
	evs := drain(out)
	var back bool
	for _, e := range evs {
		if e.Kind == StreamNotice && strings.Contains(e.TextDelta, "Back on") {
			back = true
		}
	}
	if !back {
		t.Fatalf("the boss must be told once that the primary is back: %s", kinds(evs))
	}
}

func TestEffectiveBrain_ReportsTheStandbyWhileSpent(t *testing.T) {
	f, _, _ := newFailoverFixture(t, nil)
	st := EffectiveBrain(f, "gpt-5.6-sol")
	if st.OnStandby || st.Provider != "openai_oauth" || st.Model != "gpt-5.6-sol" {
		t.Fatalf("healthy primary must report itself: %+v", st)
	}
	until := time.Now().Add(time.Hour)
	MarkExhausted("openai_oauth", until, "plus plan spent")
	st = EffectiveBrain(f, "gpt-5.6-sol")
	if !st.OnStandby || st.Provider != "anthropic" || st.Model != "claude-sonnet-4-5-20250929" || !st.Until.Equal(until) {
		t.Fatalf("spent primary must report the standby and the reset: %+v", st)
	}
}

func TestQuotaFromOpenAIBody(t *testing.T) {
	body := `{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached","plan_type":"plus","resets_at":1787800427,"eligible_promo":null,"resets_in_seconds":13709}}`
	q, ok := quotaFromOpenAIBody("openai_oauth", 429, body)
	if !ok || q.ResetsAt.Unix() != 1787800427 || !strings.Contains(q.Detail, "plus") {
		t.Fatalf("the Codex usage_limit_reached body must parse into a typed quota error: ok=%v q=%+v", ok, q)
	}
	if !strings.Contains(q.Error(), "usage limit reached") {
		t.Fatalf("error text must carry the phrase errs.Humanize keys on: %q", q.Error())
	}
	if _, ok := quotaFromOpenAIBody("openai_oauth", 429, `{"error":{"type":"rate_limit_exceeded","message":"slow down"}}`); ok {
		t.Fatal("a per-minute 429 is transient, not a spent plan")
	}
	if _, ok := quotaFromOpenAIBody("openai_oauth", 500, body); ok {
		t.Fatal("only a 429 can be a spent plan")
	}
}

func TestParseResetClock(t *testing.T) {
	loc, _ := time.LoadLocation("America/Chicago")
	now := time.Date(2026, 8, 26, 18, 25, 0, 0, loc)
	at, ok := ParseResetClock("You're out of extra usage · resets 6:50pm (America/Chicago)", now)
	if !ok || !at.Equal(time.Date(2026, 8, 26, 18, 50, 0, 0, loc)) {
		t.Fatalf("expected 6:50pm today, got ok=%v %v", ok, at)
	}
	// A clock already behind us today means tomorrow.
	at, ok = ParseResetClock("resets 9am", now)
	if !ok || at.Day() != 27 || at.Hour() != 9 {
		t.Fatalf("a passed clock rolls to tomorrow, got ok=%v %v", ok, at)
	}
	if _, ok := ParseResetClock("no reset clause here", now); ok {
		t.Fatal("no clause must report ok=false")
	}
}

// The boss's standing order: no pay-per-token brain ever takes over on its
// own. With LLM_STANDBY_PROVIDERS unset, a spent plan surfaces as the typed
// error and the API-key provider is never called, even though it is wired.
func TestFailover_DefaultHasNoStandby(t *testing.T) {
	q := &QuotaError{Provider: "openai_oauth", Detail: "spent"}
	f, _, standby := newFailoverFixture(t, q)
	t.Setenv("LLM_STANDBY_PROVIDERS", "")
	out := make(chan StreamEvent, 64)
	_, err := f.Stream(context.Background(), "", "sys", nil, nil, out)
	if got, ok := AsQuota(err); !ok || got != q {
		t.Fatalf("with no standby configured the quota error must surface: %v", err)
	}
	if standby.calls != 0 {
		t.Fatalf("the wired API-key provider must NOT be called by default (calls=%d)", standby.calls)
	}
	drain(out)
	if st := EffectiveBrain(f, ""); st.OnStandby {
		t.Fatalf("no standby must be reported by default: %+v", st)
	}
}
