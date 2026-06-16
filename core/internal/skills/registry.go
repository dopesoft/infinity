package skills

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Registry is the in-process source of truth for skills. It loads skills from
// the filesystem on demand and keeps them indexed by name. Persistence (the
// run history, version history) lives in *Store; the Registry intentionally
// does *not* depend on the database so it works with `DATABASE_URL` unset.
type Registry struct {
	mu       sync.RWMutex
	root     string
	skills   map[string]*Skill
	errs     []LoadError
	loaded   time.Time
	healedAt time.Time // last GetFresh self-heal; throttles rescans
	store    *Store    // optional - used to record runs / sync versions
}

// NewRegistry creates a Registry rooted at `root` (e.g. "./skills"). Callers
// should call Reload() to populate it; it intentionally does not read the
// filesystem at construction time so wiring is order-independent.
func NewRegistry(root string) *Registry {
	return &Registry{
		root:   root,
		skills: make(map[string]*Skill),
	}
}

func (r *Registry) Root() string { return r.root }

// AttachStore wires the optional persistence layer. Safe to call after Reload.
func (r *Registry) AttachStore(s *Store) {
	r.mu.Lock()
	r.store = s
	r.mu.Unlock()
}

// Reload re-reads every SKILL.md under the root. Existing skills are replaced
// atomically. Returns the load errors so callers can surface them.
func (r *Registry) Reload(ctx context.Context) ([]LoadError, error) {
	skills, errs, err := r.ReloadInMemory()
	if err != nil {
		return nil, err
	}
	errs = append(errs, r.StoreSync(ctx, skills)...)
	return errs, nil
}

// ReloadInMemory walks the skills directory and swaps the in-memory index —
// the ONLY part of a reload the agent needs to invoke skills this turn. It does
// no network/DB I/O, so it returns in milliseconds. The returned slice is the
// loaded skill set, handed to StoreSync (typically on a background goroutine at
// boot) so the slow Postgres sync never gates the HTTP/WS listener coming up.
func (r *Registry) ReloadInMemory() ([]*Skill, []LoadError, error) {
	skills, errs, err := LoadFromFS(r.root)
	if err != nil {
		return nil, nil, err
	}
	idx := make(map[string]*Skill, len(skills))
	for _, s := range skills {
		idx[s.Name] = s
	}

	r.mu.Lock()
	r.skills = idx
	r.errs = errs
	r.loaded = time.Now().UTC()
	r.mu.Unlock()
	return skills, errs, nil
}

// StoreSync best-effort upserts each skill into Postgres so the Studio Skills
// tab can list them even with the in-memory cache cold. It touches only the
// store — never the in-memory index — so it is safe to run from a detached
// background goroutine while the rest of boot proceeds. Each upsert gets its
// OWN bounded timeout rather than sharing the caller's deadline across the
// whole loop: against the Supabase session pooler a single slow upsert would
// otherwise consume the shared budget and cascade "context deadline exceeded"
// into every remaining skill (the boot symptom where ~10 skills failed to
// load). A per-skill cap isolates the slow one and lets the rest through;
// cancellation still propagates from the parent.
func (r *Registry) StoreSync(ctx context.Context, skills []*Skill) []LoadError {
	r.mu.RLock()
	store := r.store
	r.mu.RUnlock()
	if store == nil {
		return nil
	}
	var errs []LoadError
	for _, s := range skills {
		uctx, ucancel := context.WithTimeout(ctx, 8*time.Second)
		err := store.UpsertSkill(uctx, s)
		ucancel()
		if err != nil {
			errs = append(errs, LoadError{Path: s.Path, Err: fmt.Sprintf("upsert: %v", err)})
		}
	}
	return errs
}

// Put adds (or replaces) a skill in the in-memory index and, when a Store
// is attached, persists it to Postgres. This is the runtime self-authoring
// path: a skill the agent creates via skill_create is invocable THIS
// session - no redeploy, no boot reload. The boot-time
// MaterializeActiveSkills + Reload chain re-hydrates it from the DB on the
// next restart, so it is durable.
func (r *Registry) Put(ctx context.Context, s *Skill) error {
	if s == nil || s.Name == "" {
		return errors.New("skills: nil or unnamed skill")
	}
	s.LoadedAt = time.Now().UTC()
	r.mu.Lock()
	r.skills[s.Name] = s
	store := r.store
	r.mu.Unlock()
	if store != nil {
		if err := store.UpsertSkill(ctx, s); err != nil {
			return fmt.Errorf("skills: persist %q: %w", s.Name, err)
		}
	}
	return nil
}

// Get returns a skill by name. Lookup is case-sensitive - names are kebab-case
// by convention.
func (r *Registry) Get(name string) (*Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[name]
	return s, ok
}

// All returns every loaded skill, sorted by name. Used by the agent
// system-prompt injection and the Studio list view.
func (r *Registry) All() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Skill, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Errors returns the latest set of non-fatal load errors.
func (r *Registry) Errors() []LoadError {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]LoadError, len(r.errs))
	copy(out, r.errs)
	return out
}

// Match wraps the trigger-matcher for use by the agent loop. Limit ≤ 0 means
// no cap.
func (r *Registry) Match(message string, limit int) []Match {
	all := r.All()
	return MatchTriggers(message, all, limit)
}

// Loaded reports when the last successful filesystem scan finished.
func (r *Registry) Loaded() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loaded
}
