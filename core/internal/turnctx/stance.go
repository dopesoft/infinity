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
type StanceHolder struct {
	mu     sync.RWMutex
	stance Stance
	reason string
	ready  chan struct{}
	once   sync.Once
}

// NewStanceHolder returns an empty holder (StanceUnknown until Set).
func NewStanceHolder() *StanceHolder {
	return &StanceHolder{ready: make(chan struct{})}
}

// Set records the latest classification and unblocks any Wait.
func (h *StanceHolder) Set(s Stance, reason string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.stance, h.reason = s, strings.TrimSpace(reason)
	h.mu.Unlock()
	h.once.Do(func() { close(h.ready) })
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
