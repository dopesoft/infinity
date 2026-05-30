package push

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PrefKind is the per-event toggle identifier the boss can switch in
// Settings → Notifications. Adding a new push source means adding a new
// kind here, defaulting it in DefaultPrefs, and surfacing the toggle in
// studio/components/settings/NotificationsSection.tsx — single source of
// truth for everything notifiable.
type PrefKind string

const (
	PrefTrust            PrefKind = "trust"             // approval queue items
	PrefAgentInitiative  PrefKind = "agent_initiative"  // notify tool fired by the agent
	PrefFollowupNew      PrefKind = "followup_new"      // a new email landed in mem_followups
	PrefCalendarUpcoming PrefKind = "calendar_upcoming" // a calendar event starts within 15 min
	PrefRunStarted       PrefKind = "run_started"       // long server action (cron, skill, voyager…) began
	PrefRunFinished      PrefKind = "run_finished"      // long server action finished (ok or error)
)

// AllKinds lists every togglable push source in the canonical order Studio
// renders them. Keep new entries appended unless the UX argues for a
// different position.
func AllKinds() []PrefKind {
	return []PrefKind{
		PrefTrust,
		PrefAgentInitiative,
		PrefFollowupNew,
		PrefCalendarUpcoming,
		PrefRunStarted,
		PrefRunFinished,
	}
}

// Prefs holds the boss's per-kind toggles. Persisted as a JSON blob in
// infinity_meta keyed by 'push_prefs' so we don't need a dedicated table
// for what is fundamentally a small key/value bag.
type Prefs map[PrefKind]bool

// DefaultPrefs is what the boss gets before he ever opens the Settings
// page. Errs on the quiet side: high-signal kinds (trust, agent
// initiative, calendar reminders, run finish) are on; noisy ones
// (every-new-email, run start) stay off until he opts in.
func DefaultPrefs() Prefs {
	return Prefs{
		PrefTrust:            true,
		PrefAgentInitiative:  true,
		PrefFollowupNew:      false,
		PrefCalendarUpcoming: true,
		PrefRunStarted:       false,
		PrefRunFinished:      true,
	}
}

// Allowed reports whether `kind` is currently togged on. Unknown kinds
// (a notification with no Kind set, or a kind the boss hasn't seen yet)
// default to true so we never silently drop a notification because the
// taxonomy drifted.
func (p Prefs) Allowed(kind string) bool {
	if kind == "" {
		return true
	}
	if v, ok := p[PrefKind(kind)]; ok {
		return v
	}
	// Fall back to the default if this kind is registered but the row is
	// missing it (older boss-saved blob, newer code).
	if v, ok := DefaultPrefs()[PrefKind(kind)]; ok {
		return v
	}
	return true
}

// PrefsStore is the persistence + in-memory cache for Prefs. Backed by a
// single JSON row in infinity_meta — small payload, atomic upserts,
// reusing the same store the connector aliases and identities use.
//
// Reads go through a 30s memory cache so the hot paths (every push send)
// don't hammer Postgres. Writes invalidate the cache so toggles take
// effect on the next push without waiting for TTL.
type PrefsStore struct {
	pool *pgxpool.Pool
	mu   sync.RWMutex
	last time.Time
	ttl  time.Duration
	cache Prefs
}

// NewPrefsStore wires a store to the shared pool. Pass a nil pool to get
// a no-op store that always returns defaults (safe for unit tests).
func NewPrefsStore(pool *pgxpool.Pool) *PrefsStore {
	return &PrefsStore{pool: pool, ttl: 30 * time.Second}
}

// Load returns the current prefs, hitting the cache first.
func (s *PrefsStore) Load(ctx context.Context) (Prefs, error) {
	if s == nil {
		return DefaultPrefs(), nil
	}
	s.mu.RLock()
	if s.cache != nil && time.Since(s.last) < s.ttl {
		out := make(Prefs, len(s.cache))
		for k, v := range s.cache {
			out[k] = v
		}
		s.mu.RUnlock()
		return out, nil
	}
	s.mu.RUnlock()
	return s.refresh(ctx)
}

func (s *PrefsStore) refresh(ctx context.Context) (Prefs, error) {
	if s.pool == nil {
		return DefaultPrefs(), nil
	}
	var raw string
	err := s.pool.QueryRow(ctx, `SELECT value FROM infinity_meta WHERE key = 'push_prefs'`).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return DefaultPrefs(), err
	}
	prefs := DefaultPrefs()
	if raw != "" {
		stored := Prefs{}
		if uerr := json.Unmarshal([]byte(raw), &stored); uerr == nil {
			// Layer stored values over defaults so new kinds get their
			// default until the boss flips them.
			for k, v := range stored {
				prefs[k] = v
			}
		}
	}
	s.mu.Lock()
	s.cache = prefs
	s.last = time.Now()
	s.mu.Unlock()
	out := make(Prefs, len(prefs))
	for k, v := range prefs {
		out[k] = v
	}
	return out, nil
}

// Save writes prefs back to infinity_meta. Unknown kinds in `incoming` are
// rejected so a forged POST can't pollute the bag with garbage keys; we
// merge over the current value so the boss only has to send what he
// changed.
func (s *PrefsStore) Save(ctx context.Context, incoming Prefs) (Prefs, error) {
	if s == nil || s.pool == nil {
		return DefaultPrefs(), errors.New("prefs store: no pool")
	}
	current, err := s.refresh(ctx)
	if err != nil {
		return current, err
	}
	merged := make(Prefs, len(current))
	for k, v := range current {
		merged[k] = v
	}
	known := map[PrefKind]struct{}{}
	for _, k := range AllKinds() {
		known[k] = struct{}{}
	}
	for k, v := range incoming {
		if _, ok := known[k]; !ok {
			continue // ignore unknown keys
		}
		merged[k] = v
	}
	blob, err := json.Marshal(merged)
	if err != nil {
		return current, err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO infinity_meta (key, value)
		VALUES ('push_prefs', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`, string(blob))
	if err != nil {
		return current, err
	}
	s.mu.Lock()
	s.cache = merged
	s.last = time.Now()
	s.mu.Unlock()
	out := make(Prefs, len(merged))
	for k, v := range merged {
		out[k] = v
	}
	return out, nil
}

// Allowed is the hot-path check sender uses. Loads (cached) and consults
// .Allowed on the result. Errors fall open (allow the push) because
// silently dropping notifications because Postgres is sluggish is worse
// than a brief excess.
func (s *PrefsStore) Allowed(ctx context.Context, kind string) bool {
	if s == nil {
		return true
	}
	p, err := s.Load(ctx)
	if err != nil {
		return true
	}
	return p.Allowed(kind)
}
