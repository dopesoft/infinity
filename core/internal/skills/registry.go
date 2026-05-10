package skills

import (
	"context"
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
	mu     sync.RWMutex
	root   string
	skills map[string]*Skill
	errs   []LoadError
	loaded time.Time
	store  *Store // optional — used to record runs / sync versions
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
	skills, errs, err := LoadFromFS(r.root)
	if err != nil {
		return nil, err
	}
	idx := make(map[string]*Skill, len(skills))
	for _, s := range skills {
		idx[s.Name] = s
	}

	r.mu.Lock()
	r.skills = idx
	r.errs = errs
	r.loaded = time.Now().UTC()
	store := r.store
	r.mu.Unlock()

	// Best-effort sync into the database so the Studio Skills tab can list
	// skills even with the in-memory cache cold.
	if store != nil {
		for _, s := range skills {
			if err := store.UpsertSkill(ctx, s); err != nil {
				errs = append(errs, LoadError{Path: s.Path, Err: fmt.Sprintf("upsert: %v", err)})
			}
		}
	}
	return errs, nil
}

// Get returns a skill by name. Lookup is case-sensitive — names are kebab-case
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
