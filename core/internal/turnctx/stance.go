package turnctx

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Stance is what the boss's message asked for: a conversation, or work.
//
// It is the fact the autonomy machinery was missing. Self-heal, plan
// continuation and the "act, don't ask" soul rule all push toward doing; none
// of them knew whether the boss had actually asked for anything to be done.
// The IntentFlow classifier reads each message (judgment stays in the model);
// the loop enforces the stance at the tool chokepoint (mechanic stays in code).
type Stance string

const (
	// StanceUnknown: not classified (yet), or the classifier was unavailable.
	// Treated as "no restriction" so a classifier outage never freezes work.
	StanceUnknown Stance = ""
	// StanceDiscuss: the boss is thinking out loud, asking how / what / why,
	// brainstorming, or explicitly said to talk, hold, or wait before building.
	StanceDiscuss Stance = "discuss"
	// StanceWork: a work order ("do X", "build", "fix", "send") or an approval
	// ("go ahead", "yes, do it").
	StanceWork Stance = "work"
	// StanceUnclear: the classifier could not tell. Work tools stay open;
	// plans are still created as proposals.
	StanceUnclear Stance = "unclear"
)

// ParseStance normalises classifier output.
func ParseStance(s string) Stance {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "discuss", "talk", "brainstorm":
		return StanceDiscuss
	case "work", "execute", "build", "approve", "approved":
		return StanceWork
	case "unclear", "unknown", "mixed":
		return StanceUnclear
	}
	return StanceUnknown
}

// StanceHolder is the per-turn, mutable stance shared between the WS layer
// (which classifies each message and every mid-turn steer) and the agent
// loop (which consults it before running a work tool). Wait blocks briefly
// for the FIRST classification so the loop doesn't race the async classifier
// on the first tool call; later reads are instant.
//
// ESCALATION IS ONE-WAY FOR THE LIFE OF A TURN (the latch). discuss -> work
// always applies: "ok go ahead" mid-conversation opens the gate immediately.
// work -> discuss is REFUSED once the turn has latched — once a work tool has
// actually run with the boss's consent, or he approved / resumed a plan. A
// chatty mid-build steer ("how's it going?", "nice") classifies as discuss and
// used to overwrite the running turn's stance, which retroactively shut the
// consent gate on work he had already approved AND switched off self-heal,
// plan-continuation and the verify pass (agent/loop.go turnIsDiscuss). Talking
// to Jarvis while he works must never demote the work.
//
// The latch lives HERE, not in the callers (Rule #1b): every writer — the
// IntentFlow classifier, a steer re-read, a tool — goes through Set, so the
// mechanic cannot be forgotten by a new call site. A fresh turn gets a fresh
// holder (unlatched), so a turn that genuinely starts as a conversation is
// unaffected, and an explicit stop is a turn CANCEL (the Stop button /
// `interrupt` frame), not a stance demotion.
type StanceHolder struct {
	mu     sync.RWMutex
	stance Stance
	reason string
	ready  chan struct{}
	once   sync.Once

	// latched: this turn has crossed into sanctioned work.
	latched bool
	// latchReason records why, for the refusal record below.
	latchReason string
	// refused counts work->discuss re-readings this turn rejected, with the
	// last one's reason. Recorded rather than silently dropped so the
	// behaviour is observable in tests and telemetry.
	refused       int
	refusedReason string
}

// NewStanceHolder returns an empty holder (StanceUnknown until Set).
func NewStanceHolder() *StanceHolder {
	return &StanceHolder{ready: make(chan struct{})}
}

// Set records the latest classification and unblocks any Wait. It reports
// whether the reading was applied: a work -> discuss demotion on a latched
// turn is refused (and recorded) rather than applied. Every other transition
// applies, including discuss -> work.
func (h *StanceHolder) Set(s Stance, reason string) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	if h.latched && h.stance == StanceWork && s == StanceDiscuss {
		h.refused++
		h.refusedReason = strings.TrimSpace(reason)
		h.mu.Unlock()
		h.once.Do(func() { close(h.ready) })
		return false
	}
	h.stance, h.reason = s, strings.TrimSpace(reason)
	h.mu.Unlock()
	h.once.Do(func() { close(h.ready) })
	return true
}

// Escalate moves the turn to work and latches it. This is the explicit-consent
// path: the boss approved a plan (plan_approve) or asked to carry on with one
// he already approved (plan_resume). It always wins, and from here the turn
// cannot be demoted back to discuss.
func (h *StanceHolder) Escalate(reason string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.stance, h.reason = StanceWork, strings.TrimSpace(reason)
	h.latched, h.latchReason = true, strings.TrimSpace(reason)
	h.mu.Unlock()
	h.once.Do(func() { close(h.ready) })
}

// MarkWorked latches the turn once a consent-gated work tool has actually run
// under a work stance. Deliberately a no-op on any other stance: when the
// classifier has not answered yet (StanceUnknown) the first tool call is let
// through optimistically, and that must NOT pre-empt the first real reading —
// which may legitimately be discuss.
func (h *StanceHolder) MarkWorked(reason string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.stance == StanceWork {
		h.latched = true
		if h.latchReason == "" {
			h.latchReason = strings.TrimSpace(reason)
		}
	}
	h.mu.Unlock()
}

// Latched reports whether this turn has crossed into sanctioned work, with the
// reason it did.
func (h *StanceHolder) Latched() (bool, string) {
	if h == nil {
		return false, ""
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.latched, h.latchReason
}

// RefusedDemotions reports how many work -> discuss re-readings this turn
// rejected, and the last one's reason.
func (h *StanceHolder) RefusedDemotions() (int, string) {
	if h == nil {
		return 0, ""
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.refused, h.refusedReason
}

// Get returns the current stance without waiting.
func (h *StanceHolder) Get() (Stance, string) {
	if h == nil {
		return StanceUnknown, ""
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.stance, h.reason
}

// Wait returns the stance, blocking up to timeout for the first Set.
func (h *StanceHolder) Wait(ctx context.Context, timeout time.Duration) (Stance, string) {
	if h == nil {
		return StanceUnknown, ""
	}
	select {
	case <-h.ready:
	case <-time.After(timeout):
	case <-ctx.Done():
	}
	return h.Get()
}

type stanceContextKey struct{}

// WithStance attaches the turn's holder to ctx.
func WithStance(ctx context.Context, h *StanceHolder) context.Context {
	if h == nil {
		return ctx
	}
	return context.WithValue(ctx, stanceContextKey{}, h)
}

// StanceFromContext returns the turn's holder, or nil (autonomous turns).
func StanceFromContext(ctx context.Context) *StanceHolder {
	if ctx == nil {
		return nil
	}
	h, _ := ctx.Value(stanceContextKey{}).(*StanceHolder)
	return h
}
