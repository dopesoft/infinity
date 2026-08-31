package browser

// RoutingBackend composes a Mac (home, residential-IP) and a Cloud (Railway)
// browser backend and routes each NEW session to one of them.
//
// It honours the session's bridge selector (the same mac/cloud/auto preference
// that routes coding + files):
//
//   - "mac"   → the Mac residential browser (falls back to Cloud if Mac errors)
//   - "cloud" → the Railway browser          (falls back to Mac if Cloud errors)
//   - "auto"/"" → Mac-first by health, Cloud fallback. Mac-first because
//     anti-detect spoofs the fingerprint but NOT the IP, and a residential IP is
//     what actually defeats DataDome/Cloudflare IP reputation — a datacenter IP
//     undercuts the whole point.
//
// The health check is scoped to the browser backends themselves (their own
// /health), NOT to the coding/fs bridge — so browsing doesn't go dark just
// because Claude Code on the Mac is down, and a pinned side that's unreachable
// still falls back rather than hard-failing. Once a session is created on a
// backend, every later call for that session id sticks to the same backend (the
// tab lives there).

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/tools"
)

// PrefFunc returns the bridge preference ("mac" | "cloud" | "auto" | "") for a
// chat session. It's a plain-string mirror of bridge.Preference so the browser
// package stays decoupled from the bridge package (the same trick bridge's own
// PrefFetcher uses to dodge an import cycle). A nil PrefFunc, or "", means auto.
type PrefFunc func(ctx context.Context, chatID string) string

type RoutingBackend struct {
	mac   Backend
	cloud Backend
	pref  PrefFunc // session bridge preference; nil => always auto

	healthTTL time.Duration

	mu         sync.Mutex
	owner      map[string]Backend // session id -> the backend that owns its tab
	macUpCache bool
	macExp     time.Time
}

// NewRoutingBackend returns a router over the two backends. Either may be nil
// (e.g. only Cloud configured); a router with both nil should not be built —
// callers use a single backend directly in that case. pref supplies the
// per-session mac/cloud/auto selector; pass nil for always-auto.
func NewRoutingBackend(mac, cloud Backend, pref PrefFunc) *RoutingBackend {
	return &RoutingBackend{
		mac:       mac,
		cloud:     cloud,
		pref:      pref,
		healthTTL: 5 * time.Second,
		owner:     make(map[string]Backend),
	}
}

func (r *RoutingBackend) macHealthy(ctx context.Context) bool {
	if r.mac == nil {
		return false
	}
	r.mu.Lock()
	if time.Now().Before(r.macExp) {
		up := r.macUpCache
		r.mu.Unlock()
		return up
	}
	r.mu.Unlock()

	hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	up := r.mac.Health(hctx) == nil
	cancel()

	r.mu.Lock()
	r.macUpCache = up
	r.macExp = time.Now().Add(r.healthTTL)
	r.mu.Unlock()
	return up
}

// prefFor reads the session's bridge selector from ctx. "" / nil PrefFunc =>
// auto.
func (r *RoutingBackend) prefFor(ctx context.Context) string {
	if r.pref == nil {
		return "auto"
	}
	return r.pref(ctx, tools.SessionIDFromContext(ctx))
}

// pick chooses the backend for a NEW session, honouring the session's bridge
// selector. For an explicit mac/cloud pin we return the pinned backend without
// a health probe and let CreateSession's fallback handle it if that side is
// down; for auto we prefer Mac when its /health is reachable.
func (r *RoutingBackend) pick(ctx context.Context) Backend {
	switch r.prefFor(ctx) {
	case "cloud":
		if r.cloud != nil {
			return r.cloud
		}
		return r.mac
	case "mac":
		if r.mac != nil {
			return r.mac
		}
		return r.cloud
	default: // "auto" / "": Mac-first by health, Cloud fallback.
		if r.mac != nil && r.macHealthy(ctx) {
			return r.mac
		}
		if r.cloud != nil {
			return r.cloud
		}
		return r.mac // last resort: let the error surface from the call
	}
}

func (r *RoutingBackend) other(b Backend) Backend {
	if b == r.mac {
		return r.cloud
	}
	return r.mac
}

// backendFor returns the backend owning an existing session, or picks one if
// the session is unknown (e.g. created before a restart).
func (r *RoutingBackend) backendFor(ctx context.Context, sessionID string) Backend {
	r.mu.Lock()
	b, ok := r.owner[sessionID]
	r.mu.Unlock()
	if ok {
		return b
	}
	return r.pick(ctx)
}

func (r *RoutingBackend) remember(sessionID string, b Backend) {
	r.mu.Lock()
	r.owner[sessionID] = b
	r.mu.Unlock()
}

func (r *RoutingBackend) forget(sessionID string) {
	r.mu.Lock()
	delete(r.owner, sessionID)
	r.mu.Unlock()
}

func (r *RoutingBackend) Health(ctx context.Context) error {
	if r.mac != nil && r.mac.Health(ctx) == nil {
		return nil
	}
	if r.cloud != nil && r.cloud.Health(ctx) == nil {
		return nil
	}
	return errors.New("no browser backend healthy")
}

func (r *RoutingBackend) CreateSession(ctx context.Context, url string) (*SessionInfo, error) {
	b := r.pick(ctx)
	if b == nil {
		return nil, errors.New("no browser backend available")
	}
	info, err := b.CreateSession(ctx, url)
	if err != nil {
		// One fallback to the other side so a single backend hiccup doesn't
		// fail the open outright.
		if alt := r.other(b); alt != nil {
			if info2, err2 := alt.CreateSession(ctx, url); err2 == nil {
				r.remember(info2.SessionID, alt)
				return info2, nil
			}
		}
		return nil, err
	}
	r.remember(info.SessionID, b)
	return info, nil
}

func (r *RoutingBackend) Navigate(ctx context.Context, sessionID, url string) (*NavResult, error) {
	return r.backendFor(ctx, sessionID).Navigate(ctx, sessionID, url)
}

func (r *RoutingBackend) Observe(ctx context.Context, sessionID string) (*ObserveResult, error) {
	return r.backendFor(ctx, sessionID).Observe(ctx, sessionID)
}

func (r *RoutingBackend) Act(ctx context.Context, sessionID string, req ActRequest) (*ActResult, error) {
	return r.backendFor(ctx, sessionID).Act(ctx, sessionID, req)
}

func (r *RoutingBackend) Input(ctx context.Context, sessionID string, ev InputEvent) error {
	return r.backendFor(ctx, sessionID).Input(ctx, sessionID, ev)
}

func (r *RoutingBackend) Extract(ctx context.Context, sessionID, format string) (*ExtractResult, error) {
	return r.backendFor(ctx, sessionID).Extract(ctx, sessionID, format)
}

func (r *RoutingBackend) Screenshot(ctx context.Context, sessionID string) (*ShotResult, error) {
	return r.backendFor(ctx, sessionID).Screenshot(ctx, sessionID)
}

func (r *RoutingBackend) Close(ctx context.Context, sessionID string) error {
	b := r.backendFor(ctx, sessionID)
	err := b.Close(ctx, sessionID)
	r.forget(sessionID)
	return err
}

func (r *RoutingBackend) SubscribeScreencast(ctx context.Context, sessionID string) (*Stream, error) {
	return r.backendFor(ctx, sessionID).SubscribeScreencast(ctx, sessionID)
}
