package llm

import (
	"context"
	"log"
	"os"
	"strings"
	"time"
)

// Brain failover on plan exhaustion (see quota.go for the incident).
//
// Every provider handed out by the Registry is wrapped in a failoverProvider.
// The wrapper is the boss's chosen brain (Name/Model/Unwrap all report the
// PRIMARY, so Settings, cost attribution and type assertions are unchanged)
// with one extra behaviour: when the primary returns a QuotaError before any
// content has streamed, the call is re-run on the first healthy STANDBY
// provider in the registry, the boss is told in one line, and the primary is
// held out until its reset time so later turns go straight to the standby
// without paying a doomed request. When the hold expires the primary is
// tried again and, on success, a one-line "back on X" lands in the chat.
//
// Standbys are OPT-IN ONLY: LLM_STANDBY_PROVIDERS (comma list of registry
// ids). With it unset there is NO standby and a spent plan surfaces as a
// plain "out of usage until <reset>" error. The boss's standing order
// (2026-08-26, said more than once): the Anthropic API key is never to
// carry his conversation; Claude is his Max plan through Claude Code on his
// Mac and nothing else. Never default this to a pay-per-token brain.
//
// Rule #1b: this is a mechanic (no judgment), so it lives here, not in a
// prompt. Rule #1c: wired at the single chokepoint every consumer resolves
// through (Registry.Get, plus the boot provider in serve.go).

// defaultStandbyOrder is empty on purpose (see above).
const defaultStandbyOrder = ""

var failoverLog = log.New(os.Stdout, "", log.LstdFlags)

type failoverProvider struct {
	primary Provider
	reg     *Registry
}

// WrapFailover returns p with standby routing over reg. Idempotent.
func WrapFailover(p Provider, reg *Registry) Provider {
	if p == nil || reg == nil {
		return p
	}
	if _, already := p.(*failoverProvider); already {
		return p
	}
	return &failoverProvider{primary: p, reg: reg}
}

func (f *failoverProvider) Name() string     { return f.primary.Name() }
func (f *failoverProvider) Model() string    { return f.primary.Model() }
func (f *failoverProvider) Unwrap() Provider { return f.primary }

// standbyOrder is the configured preference list of standby provider ids.
func standbyOrder() []string {
	raw := strings.TrimSpace(os.Getenv("LLM_STANDBY_PROVIDERS"))
	if raw == "" {
		raw = defaultStandbyOrder
	}
	var out []string
	for _, s := range strings.Split(raw, ",") {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// standby picks the first registered, not-spent provider that is not the
// primary. Nil when nothing can take over.
func (f *failoverProvider) standby() Provider {
	primary := strings.ToLower(f.primary.Name())
	for _, name := range standbyOrder() {
		if name == primary {
			continue
		}
		if _, _, spent := Exhausted(name); spent {
			continue
		}
		if p, ok := f.reg.lookup(name); ok && p != nil {
			return p
		}
	}
	return nil
}

// standbyModel keeps the requested model only when it belongs to the
// standby's family; otherwise the standby runs its own default. A model id
// is vendor-specific, so carrying "gpt-5.6-sol" to Anthropic would 400.
func standbyModel(sb Provider, requested string) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return ""
	}
	if ModelFamilyMatches(sb.Name(), requested) {
		return requested
	}
	return ""
}

// BrainStatus is what is actually answering right now, for the Settings API
// and the Studio chip. OnStandby is false when the primary is healthy.
type BrainStatus struct {
	Primary   string
	Provider  string
	Model     string
	OnStandby bool
	Until     time.Time
	Reason    string
}

// EffectiveBrain reports the brain a call through p would run on this
// instant, given the requested model (the Settings override, may be "").
func EffectiveBrain(p Provider, requestedModel string) BrainStatus {
	if p == nil {
		return BrainStatus{}
	}
	f, ok := p.(*failoverProvider)
	if !ok {
		return BrainStatus{Primary: p.Name(), Provider: p.Name(), Model: firstNonEmpty(requestedModel, p.Model())}
	}
	st := BrainStatus{Primary: f.primary.Name(), Provider: f.primary.Name(), Model: firstNonEmpty(requestedModel, f.primary.Model())}
	until, detail, spent := Exhausted(f.primary.Name())
	if !spent {
		return st
	}
	sb := f.standby()
	if sb == nil {
		st.Until, st.Reason = until, detail
		return st
	}
	st.Provider = sb.Name()
	st.Model = firstNonEmpty(standbyModel(sb, requestedModel), sb.Model())
	st.OnStandby = true
	st.Until = until
	st.Reason = detail
	return st
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// ProviderLabel is how a provider id reads in the boss's chat.
func ProviderLabel(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "openai_oauth":
		return "ChatGPT (your plan)"
	case "openai":
		return "OpenAI (API)"
	case "anthropic":
		return "Claude (API)"
	case "google":
		return "Gemini (API)"
	case "deepseek":
		return "DeepSeek (API)"
	case "claude_code":
		return "Claude Code (your Claude plan)"
	}
	return name
}

func switchNotice(primary, standby string, until time.Time) string {
	when := ""
	if !until.IsZero() {
		when = " until " + FormatLocalClock(until)
	}
	return "\n\n_" + ProviderLabel(primary) + " is out of usage" + when + ". I'm on " + ProviderLabel(standby) + " meanwhile._\n"
}

func recoveredNotice(primary string) string {
	return "\n\n_Back on " + ProviderLabel(primary) + "._\n"
}

type streamCall func(p Provider, model string, out chan<- StreamEvent) (Response, error)

func (f *failoverProvider) Stream(ctx context.Context, model, system string, messages []Message, tools []ToolDef, out chan<- StreamEvent) (Response, error) {
	return f.run(model, out, func(p Provider, m string, ch chan<- StreamEvent) (Response, error) {
		return p.Stream(ctx, m, system, messages, tools, ch)
	})
}

// StreamCached keeps the wrapper a CachingProvider so the loop's assertion
// succeeds through it; the standby gets StreamCached when it caches, else
// the rendered prompt.
func (f *failoverProvider) StreamCached(ctx context.Context, model string, sys SystemPrompt, messages []Message, tools []ToolDef, out chan<- StreamEvent) (Response, error) {
	return f.run(model, out, func(p Provider, m string, ch chan<- StreamEvent) (Response, error) {
		if cp, ok := p.(CachingProvider); ok {
			return cp.StreamCached(ctx, m, sys, messages, tools, ch)
		}
		return p.Stream(ctx, m, sys.Render(), messages, tools, ch)
	})
}

// CompactContext passes through to a healthy primary only; a spent primary
// cannot compact, and the caller's own fallback handles ErrNotImplemented.
func (f *failoverProvider) CompactContext(ctx context.Context, model string, messages []Message) ([]Message, TokenUsage, error) {
	if _, _, spent := Exhausted(f.primary.Name()); spent {
		return nil, TokenUsage{}, ErrNotImplemented
	}
	cp, ok := f.primary.(CompactingProvider)
	if !ok {
		return nil, TokenUsage{}, ErrNotImplemented
	}
	return cp.CompactContext(ctx, model, messages)
}

func (f *failoverProvider) run(model string, out chan<- StreamEvent, call streamCall) (Response, error) {
	primary := f.primary.Name()

	// Already known spent: go straight to the standby, no doomed request.
	if until, detail, spent := Exhausted(primary); spent {
		sb := f.standby()
		if sb == nil {
			err := &QuotaError{Provider: primary, ResetsAt: until, Detail: detail}
			emit(out, StreamEvent{Kind: StreamError, Err: err.Error()})
			return Response{}, err
		}
		return f.callStandby(sb, model, out, call)
	}
	if takeRecovered(primary) {
		emit(out, StreamEvent{Kind: StreamNotice, TextDelta: recoveredNotice(primary)})
		failoverLog.Printf("llm: %s is back; leaving standby", primary)
	}

	resp, err, contentSeen, held := f.callIntercepting(f.primary, model, out, call)
	if err == nil {
		flush(out, held)
		return resp, nil
	}
	q, isQuota := AsQuota(err)
	if !isQuota || contentSeen {
		// A real error, or a quota hit after content already streamed (a
		// re-run would duplicate output): surface exactly as before.
		flush(out, held)
		return resp, err
	}

	until := q.ResetsAt
	if until.IsZero() {
		until = time.Now().Add(defaultQuotaHold)
	}
	MarkExhausted(primary, until, q.Detail)
	sb := f.standby()
	if sb == nil {
		log.Printf("llm: %s out of usage until %s; no standby configured (LLM_STANDBY_PROVIDERS is opt-in), holding",
			primary, FormatLocalClock(until))
		flush(out, held)
		return resp, err
	}
	log.Printf("llm: %s out of usage until %s (%s); failing over to %s", primary, FormatLocalClock(until), q.Detail, sb.Name())
	emit(out, StreamEvent{Kind: StreamNotice, TextDelta: switchNotice(primary, sb.Name(), until)})
	return f.callStandby(sb, model, out, call)
}

func (f *failoverProvider) callStandby(sb Provider, model string, out chan<- StreamEvent, call streamCall) (Response, error) {
	resp, err := call(sb, standbyModel(sb, model), out)
	if q, ok := AsQuota(err); ok {
		MarkExhausted(sb.Name(), q.ResetsAt, q.Detail)
		log.Printf("llm: standby %s is out of usage too: %v", sb.Name(), err)
	}
	return resp, err
}

// callIntercepting runs call on p, forwarding content events to out as they
// arrive but HOLDING terminal events (error/complete) until the call returns,
// so a quota failure with no content can be swallowed and re-run on the
// standby without the consumer ever seeing a spurious error. contentSeen is
// true once anything the consumer could have rendered was forwarded.
func (f *failoverProvider) callIntercepting(p Provider, model string, out chan<- StreamEvent, call streamCall) (resp Response, err error, contentSeen bool, held []StreamEvent) {
	if out == nil {
		resp, err = call(p, model, nil)
		return resp, err, false, nil
	}
	mid := make(chan StreamEvent, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range mid {
			switch ev.Kind {
			case StreamError, StreamComplete:
				held = append(held, ev)
			default:
				contentSeen = true
				out <- ev
			}
		}
	}()
	resp, err = call(p, model, mid)
	close(mid)
	<-done
	return resp, err, contentSeen, held
}

func flush(out chan<- StreamEvent, held []StreamEvent) {
	for _, ev := range held {
		emit(out, ev)
	}
}
