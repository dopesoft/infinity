package maintenance

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Highlights is the SUBSTANCE of a nightly-cognition run: the actual lesson
// texts, memory titles, and entity names the run produced, pulled back out of
// the tables it just wrote. The boss reads these, not counters — "115 decayed"
// says nothing; the lesson "when the user names a skill, invoke it before any
// planning" says everything. Collected deterministically by created/updated
// timestamps inside the run window, so it also works retroactively for past
// runs (the backfill passes the stored started_at/ended_at).
type Highlights struct {
	Lessons  []string `json:"lessons,omitempty"`  // reflection lessons + chain lessons, best first
	Memories []string `json:"memories,omitempty"` // titles of new durable memories, most important first
	Entities []string `json:"entities,omitempty"` // names of newly tracked world-model entities
}

func (h Highlights) Empty() bool {
	return len(h.Lessons) == 0 && len(h.Memories) == 0 && len(h.Entities) == 0
}

const (
	maxLessonHighlights = 5
	maxMemoryHighlights = 3
	maxEntityHighlights = 5
	maxHighlightChars   = 200
)

// CollectHighlights gathers the run window's real artifacts. Best-effort and
// nil-safe: any query failure just leaves that section empty — the digest
// degrades to counts, never blocks the run.
func CollectHighlights(ctx context.Context, pool *pgxpool.Pool, since, until time.Time) Highlights {
	var h Highlights
	if pool == nil || since.IsZero() {
		return h
	}
	if until.IsZero() {
		until = time.Now().UTC()
	}
	h.Lessons = collectLessons(ctx, pool, since, until)
	h.Memories = collectMemories(ctx, pool, since, until)
	h.Entities = collectEntities(ctx, pool, since, until)
	return h
}

// collectLessons pulls the individual lesson texts out of the reflections the
// run just wrote (mem_reflections.lessons is a JSONB array of {text,
// confidence}), highest-confidence first, then tops up with any cross-session
// chain lessons the run updated.
func collectLessons(ctx context.Context, pool *pgxpool.Pool, since, until time.Time) []string {
	type scored struct {
		text string
		conf float64
	}
	var all []scored
	rows, err := pool.Query(ctx, `
		SELECT l->>'text', COALESCE((l->>'confidence')::float, 0)
		  FROM mem_reflections r,
		       jsonb_array_elements(COALESCE(r.lessons, '[]'::jsonb)) AS l
		 WHERE r.created_at >= $1 AND r.created_at < $2
	`, since, until)
	if err == nil {
		for rows.Next() {
			var s scored
			if rows.Scan(&s.text, &s.conf) == nil && strings.TrimSpace(s.text) != "" {
				all = append(all, s)
			}
		}
		rows.Close()
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].conf > all[j].conf })

	seen := map[string]bool{}
	var out []string
	add := func(text string) {
		t := clampHighlight(text)
		if t == "" || seen[strings.ToLower(t)] {
			return
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	for _, s := range all {
		if len(out) >= maxLessonHighlights {
			return out
		}
		add(s.text)
	}
	// Chain lessons the run updated (cross-session patterns) fill the rest.
	crows, err := pool.Query(ctx, `
		SELECT lesson FROM mem_reflection_chains
		 WHERE updated_at >= $1 AND updated_at < $2
		 ORDER BY occurrences DESC, updated_at DESC
		 LIMIT 3
	`, since, until)
	if err == nil {
		for crows.Next() {
			var lesson string
			if crows.Scan(&lesson) == nil && len(out) < maxLessonHighlights {
				add(lesson)
			}
		}
		crows.Close()
	}
	return out
}

// collectMemories returns the titles of the durable memories the run promoted
// (the compressor's output), most important first.
func collectMemories(ctx context.Context, pool *pgxpool.Pool, since, until time.Time) []string {
	rows, err := pool.Query(ctx, `
		SELECT COALESCE(NULLIF(title, ''), left(content, 160))
		  FROM mem_memories
		 WHERE created_at >= $1 AND created_at < $2
		   AND superseded_by IS NULL
		 ORDER BY importance DESC NULLS LAST, created_at DESC
		 LIMIT $3
	`, since, until, maxMemoryHighlights)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			if c := clampHighlight(t); c != "" {
				out = append(out, c)
			}
		}
	}
	return out
}

// collectEntities returns the names of world-model entities the run STARTED
// tracking (created in the window) — refreshed ones are routine, new ones are
// news.
func collectEntities(ctx context.Context, pool *pgxpool.Pool, since, until time.Time) []string {
	rows, err := pool.Query(ctx, `
		SELECT name FROM mem_entities
		 WHERE created_at >= $1 AND created_at < $2
		 ORDER BY salience DESC NULLS LAST, created_at DESC
		 LIMIT $3
	`, since, until, maxEntityHighlights)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && strings.TrimSpace(n) != "" {
			out = append(out, strings.TrimSpace(n))
		}
	}
	return out
}

func clampHighlight(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= maxHighlightChars {
		return s
	}
	cut := string(r[:maxHighlightChars])
	if i := strings.LastIndexByte(cut, ' '); i > maxHighlightChars/2 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut) + "…"
}
