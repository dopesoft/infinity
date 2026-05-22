package browser

// RoutingBackend composes a Mac (home, residential-IP) and a Cloud (Railway)
// browser backend and routes each session to one of them. It prefers the Mac
// whenever that instance is reachable: anti-detect spoofs the fingerprint but
// NOT the IP, and a residential IP is what actually defeats DataDome/Cloudflare
// IP reputation — a datacenter IP undercuts the whole point. Cloud is the
// fallback when the home box is offline.
//
// Health is scoped to the browser backends themselves (their own /health), not
// to the coding/fs bridge — so browsing doesn't go dark just because Claude Code
// on the Mac is down, and vice versa. Once a session is created on a backend,
// every later call for that session id sticks to the same backend (the tab
// lives there).

import (
	"context"
	"errors"
	"sync"
	"time"
)

type RoutingBackend struct {
	mac   Backend
	cloud Backend

	healthTTL time.Duration

	mu         sync.Mutex
	owner      map[string]Backend // session id -> the backend that owns its tab
	macUpCache bool
	macExp     time.Time
}

// NewRoutingBackend returns a router over the two backends. Either may be nil
// (e.g. only Cloud configured); a router with both nil should not be built —
// callers use a single backend directly in that case.
func NewRoutingBackend(mac, cloud Backend) *RoutingBackend {
	return &RoutingBackend{
		mac:       mac,
		cloud:     cloud,
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

// pick chooses the backend for a NEW session: Mac when reachable, else Cloud.
func (r *RoutingBackend) pick(ctx context.Context) Backend {
	if r.mac != nil && r.macHealthy(ctx) {
		return r.mac
	}
	if r.cloud != nil {
		return r.cloud
	}
	return r.mac // last resort: let the error surface from the call
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

func (r *RoutingBackend) SubscribeScreencast(ctx context.Context, sessionID string) (<-chan Frame, error) {
	return r.backendFor(ctx, sessionID).SubscribeScreencast(ctx, sessionID)
}
