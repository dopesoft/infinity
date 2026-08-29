package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Global search. GET /api/search?q=…
//
// ONE generic contract for finding anything, rather than a bespoke search
// endpoint per surface. The palette (⌘K) renders whatever comes back
// generically, and the per-page scoped search reuses the same response for
// its per-tab match counts — which is what lets a scoped search say "nothing
// in Facts, but 3 in Lessons" instead of silently hiding a match.
//
// Deliberately NOT the RRF machinery behind /api/memory/search. That answers
// "what does the agent recall about this", which is a ranked semantic
// question over observations. This answers "where is the thing called X",
// which is a name-substring question across every table the boss can open.
// Different questions, different queries; conflating them is how a palette
// ends up slow and full of near-misses.
//
// Every sub-query is permissive: a table that does not exist yet, or a query
// that errors, contributes zero rows rather than 500-ing the palette. A
// failure to reach ONE source is not a failure to search — but it is also
// never reported as "no results", because the counts are per kind and an
// absent kind reads as absent, not as empty.

type searchHit struct {
	// What kind of thing this is. Drives the group heading and the glyph in
	// the palette. Free-form on purpose: a new searchable table adds a case
	// here and Studio renders it without a change.
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// The name a person would recognise. Never an id, never a table name.
	Title string `json:"title"`
	// At most ONE line of context. The row rule applies here too.
	Meta string `json:"meta"`
	// Where tapping it lands.
	Href string `json:"href"`
}

type searchResponse struct {
	Query        string         `json:"query"`
	Hits         []searchHit    `json:"hits"`
	CountsByKind map[string]int `json:"counts_by_kind"`
}

// perKindLimit caps each source so one populous table (memories) cannot
// crowd every other kind out of the palette.
const perKindLimit = 6

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := searchResponse{Query: q, Hits: []searchHit{}, CountsByKind: map[string]int{}}
	if q == "" || s.cfg.Pool == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}

	ctx := r.Context()
	like := "%" + strings.ReplaceAll(strings.ReplaceAll(q, "%", `\%`), "_", `\_`) + "%"

	for _, src := range searchSources() {
		hits := src.run(ctx, s.cfg.Pool, like)
		if len(hits) == 0 {
			continue
		}
		out.CountsByKind[src.kind] = len(hits)
		out.Hits = append(out.Hits, hits...)
	}
	writeJSON(w, http.StatusOK, out)
}

type searchSource struct {
	kind string
	run  func(ctx context.Context, pool *pgxpool.Pool, like string) []searchHit
}

func searchSources() []searchSource {
	return []searchSource{
		{kind: "memory", run: searchMemories},
		{kind: "skill", run: searchSkills},
		{kind: "automation", run: searchAutomations},
		{kind: "surfaced", run: searchSurfaced},
		{kind: "session", run: searchSessions},
	}
}

// scanHits runs one query and maps every row through `row`. A query error
// yields no rows; it never propagates, so one unreachable table cannot take
// the whole palette down with it.
func scanHits(
	ctx context.Context,
	pool *pgxpool.Pool,
	sql string,
	arg string,
	row func(id, a, b string) searchHit,
) []searchHit {
	rows, err := pool.Query(ctx, sql, arg, perKindLimit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []searchHit{}
	for rows.Next() {
		var id, a, b string
		if err := rows.Scan(&id, &a, &b); err != nil {
			continue
		}
		out = append(out, row(id, a, b))
	}
	return out
}

func searchMemories(ctx context.Context, pool *pgxpool.Pool, like string) []searchHit {
	return scanHits(ctx, pool, `
		SELECT id::text,
		       COALESCE(NULLIF(btrim(title), ''), left(content, 120)),
		       COALESCE(tier, '')
		  FROM mem_memories
		 WHERE status = 'active'
		   AND (title ILIKE $1 OR content ILIKE $1)
		 ORDER BY COALESCE(importance, 0) DESC, created_at DESC
		 LIMIT $2
	`, like, func(id, title, tier string) searchHit {
		return searchHit{
			Kind:  "memory",
			ID:    id,
			Title: title,
			Meta:  memoryTierLabel(tier),
			Href:  "/memory?focus=" + id,
		}
	})
}

// memoryTierLabel turns the storage tier into the word a person would use.
// The tier itself stays visible in the detail sheet, in mono, where it is
// useful for debugging.
func memoryTierLabel(tier string) string {
	switch tier {
	case "semantic":
		return "Fact"
	case "episodic":
		return "Something seen"
	case "procedural":
		return "Something he learned"
	case "working":
		return "In progress"
	default:
		return "Fact"
	}
}

func searchSkills(ctx context.Context, pool *pgxpool.Pool, like string) []searchHit {
	return scanHits(ctx, pool, `
		SELECT COALESCE(NULLIF(skill_name, ''), name),
		       COALESCE(NULLIF(name, ''), skill_name),
		       COALESCE(description, '')
		  FROM mem_skills
		 WHERE COALESCE(status, 'active') <> 'archived'
		   AND (name ILIKE $1 OR skill_name ILIKE $1 OR description ILIKE $1)
		 ORDER BY updated_at DESC NULLS LAST
		 LIMIT $2
	`, like, func(id, name, desc string) searchHit {
		return searchHit{
			Kind:  "skill",
			ID:    id,
			Title: name,
			Meta:  firstLine(desc),
			Href:  "/skills?focus=" + id,
		}
	})
}

func searchAutomations(ctx context.Context, pool *pgxpool.Pool, like string) []searchHit {
	// Schedules and watchers are one kind to the boss ("things that fire
	// without me"), so they are one UNION rather than two groups he has to
	// read as different.
	return scanHits(ctx, pool, `
		SELECT id::text, label, sub FROM (
		  SELECT id, COALESCE(NULLIF(schedule_natural, ''), name) AS label,
		         'On a schedule' AS sub, created_at
		    FROM mem_crons
		   WHERE name ILIKE $1 OR COALESCE(schedule_natural,'') ILIKE $1
		  UNION ALL
		  SELECT id, name AS label, 'Watching for something' AS sub, created_at
		    FROM mem_sentinels
		   WHERE name ILIKE $1
		) t
		 ORDER BY created_at DESC
		 LIMIT $2
	`, like, func(id, label, sub string) searchHit {
		return searchHit{
			Kind:  "automation",
			ID:    id,
			Title: label,
			Meta:  sub,
			Href:  "/automations?focus=" + id,
		}
	})
}

func searchSurfaced(ctx context.Context, pool *pgxpool.Pool, like string) []searchHit {
	return scanHits(ctx, pool, `
		SELECT id::text, title, COALESCE(NULLIF(subtitle, ''), source)
		  FROM mem_surface_items
		 WHERE COALESCE(status, 'open') = 'open'
		   AND (title ILIKE $1 OR subtitle ILIKE $1 OR body ILIKE $1)
		 ORDER BY created_at DESC
		 LIMIT $2
	`, like, func(id, title, sub string) searchHit {
		return searchHit{
			Kind:  "surfaced",
			ID:    id,
			Title: title,
			Meta:  firstLine(sub),
			Href:  "/?focus=" + id,
		}
	})
}

func searchSessions(ctx context.Context, pool *pgxpool.Pool, like string) []searchHit {
	return scanHits(ctx, pool, `
		SELECT id::text, COALESCE(NULLIF(btrim(name), ''), 'Untitled'),
		       COALESCE(to_char(COALESCE(last_run_at, started_at), 'Mon DD'), '')
		  FROM mem_sessions
		 WHERE deleted_at IS NULL
		   AND name ILIKE $1
		 ORDER BY COALESCE(last_run_at, started_at) DESC
		 LIMIT $2
	`, like, func(id, name, when string) searchHit {
		return searchHit{
			Kind:  "session",
			ID:    id,
			Title: name,
			Meta:  when,
			Href:  "/live?session=" + id,
		}
	})
}

// firstLine keeps a hit's meta to one line. A row gets a title and at most
// one piece of meta; the rest belongs in the sheet behind it.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 90 {
		s = strings.TrimSpace(s[:90]) + "…"
	}
	return s
}
