package proactive

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CalendarPrepChecklist returns a Checklist that scans mem_calendar_events
// for anything starting in the next 60 minutes and surfaces a Finding per
// event with quick context: best-match attendee in the world model, most
// recent memory mentioning the attendee, and existing prep checklist items.
//
// Deliberately read-only and deterministic. The Composio Calendar poller
// (mem_crons with kind='connector_poll', migration 015) keeps
// mem_calendar_events fresh; this checklist just turns the next-up event
// into a "meeting with X in N min — last talked about Y" nudge.
//
// Compose with the other heartbeat checklists via ComposeChecklists.
func CalendarPrepChecklist(pool *pgxpool.Pool) Checklist {
	return func(ctx context.Context, _ *Heartbeat) ([]Finding, error) {
		if pool == nil {
			return nil, nil
		}
		rows, err := pool.Query(ctx, `
			SELECT id::text, title, attendees, starts_at, location, prep
			  FROM mem_calendar_events
			 WHERE starts_at > NOW()
			   AND starts_at <= NOW() + INTERVAL '60 minutes'
			 ORDER BY starts_at ASC
			 LIMIT 5
		`)
		if err != nil {
			return nil, nil
		}
		defer rows.Close()

		var out []Finding
		for rows.Next() {
			var (
				id, title, location string
				attendeesRaw        []byte
				startsAt            time.Time
				prepRaw             []byte
			)
			if err := rows.Scan(&id, &title, &attendeesRaw, &startsAt, &location, &prepRaw); err != nil {
				continue
			}
			minutes := int(time.Until(startsAt).Round(time.Minute).Minutes())
			if minutes < 0 {
				continue
			}
			detail := fmt.Sprintf("Starts in %d min", minutes)
			if location != "" {
				detail += " · " + location
			}
			// Best-effort attendee lookup. mem_calendar_events.attendees is a
			// JSONB array; we look up the world model for the first attendee
			// we can resolve and append the most recent memory that
			// mentions them.
			if entSummary := bestAttendeeContext(ctx, pool, attendeesRaw); entSummary != "" {
				detail += "\n" + entSummary
			}
			if openPrep := countOpenPrep(prepRaw); openPrep > 0 {
				detail += fmt.Sprintf("\n%d prep item(s) still open", openPrep)
			}
			out = append(out, Finding{
				Kind:      "outcome",
				Title:     fmt.Sprintf("Meeting %q in %d min", title, minutes),
				Detail:    detail,
				SourceTag: "calendar:" + id,
			})
		}
		return out, nil
	}
}

// bestAttendeeContext walks the attendees JSONB array, tries each one
// against mem_entities (by name or email), and returns a short string of
// the form "John Doe — last memory: <snippet>" for the first hit. Empty
// when no attendee resolves.
func bestAttendeeContext(ctx context.Context, pool *pgxpool.Pool, raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var attendees []map[string]any
	if err := json.Unmarshal(raw, &attendees); err != nil {
		// Fall back to a plain string array.
		var strs []string
		if json.Unmarshal(raw, &strs) != nil || len(strs) == 0 {
			return ""
		}
		for _, s := range strs {
			if ctxStr := lookupAttendee(ctx, pool, s); ctxStr != "" {
				return ctxStr
			}
		}
		return ""
	}
	for _, a := range attendees {
		for _, key := range []string{"email", "name", "displayName", "address"} {
			if v, ok := a[key].(string); ok && strings.TrimSpace(v) != "" {
				if ctxStr := lookupAttendee(ctx, pool, v); ctxStr != "" {
					return ctxStr
				}
			}
		}
	}
	return ""
}

func lookupAttendee(ctx context.Context, pool *pgxpool.Pool, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	var (
		name, summary string
	)
	err := pool.QueryRow(ctx, `
		SELECT name, COALESCE(summary, '')
		  FROM mem_entities
		 WHERE status <> 'archived'
		   AND (lower(name) = lower($1)
		        OR aliases @> to_jsonb(ARRAY[$1])
		        OR attributes->>'email' = lower($1))
		 ORDER BY salience DESC
		 LIMIT 1
	`, ref).Scan(&name, &summary)
	if err != nil {
		return ""
	}
	out := name
	if summary != "" {
		out += " — " + clipPrepSnippet(summary, 160)
	}
	// One-shot recent memory mentioning this entity.
	var memSnippet string
	_ = pool.QueryRow(ctx, `
		SELECT COALESCE(raw_text, '')
		  FROM mem_memories
		 WHERE status = 'active'
		   AND raw_text ILIKE '%' || $1 || '%'
		 ORDER BY created_at DESC
		 LIMIT 1
	`, name).Scan(&memSnippet)
	if memSnippet != "" {
		out += "\nrecent memory: " + clipPrepSnippet(memSnippet, 200)
	}
	return out
}

func countOpenPrep(raw []byte) int {
	if len(raw) == 0 {
		return 0
	}
	var items []map[string]any
	if json.Unmarshal(raw, &items) != nil {
		return 0
	}
	open := 0
	for _, it := range items {
		if done, _ := it["done"].(bool); !done {
			open++
		}
	}
	return open
}

func clipPrepSnippet(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
