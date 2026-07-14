// anniversaries.go — the dates that come back.
//
// The boss: "if I say to wish someone happy birthday, will he remember it next
// year and surface that the person's birthday is coming up?"
//
// The pieces for that already exist and were never joined up. Calls now land in
// memory (phone/memory.go), the nightly extractor reads observations and writes
// people into mem_entities with their attributes, and the heartbeat runs
// checklists. What was missing is the thing that LOOKS at those dates and
// notices one is coming round again.
//
// This is that, and it is deliberately generic: it does not know what a birthday
// is. It scans every entity attribute whose VALUE parses as a date and whose KEY
// reads like a recurring occasion, and raises the ones landing in the next few
// days. Learn an anniversary, a renewal date, a kid's first day at school, and it
// comes back on its own. No new table, no bespoke birthday feature, no per-fact
// wiring: exactly what Rule #1 asks for.
package proactive

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// lookAhead is how far in advance an occasion surfaces. Long enough to act (buy
// something, arrange a call), short enough that it is not noise.
const lookAhead = 7 * 24 * time.Hour

// occasionKeys are the attribute names that mean "this date comes round again".
// Matched as substrings, lowercased, so "birthday", "birth_date" and
// "wedding anniversary" all count.
var occasionKeys = []string{"birthday", "birth date", "birth_date", "anniversary", "nameday"}

// AnniversaryChecklist raises entities whose recurring date is nearly here.
//
// Composed into the heartbeat like the other checklists, so it runs on the same
// tick and its findings reach the boss through the same surface. Nothing about
// it is phone-specific: it works for anyone Jarvis has learned about, from any
// channel.
func AnniversaryChecklist(pool *pgxpool.Pool) Checklist {
	return func(ctx context.Context, _ *Heartbeat) ([]Finding, error) {
		if pool == nil {
			return nil, nil
		}
		rows, err := pool.Query(ctx, `
			SELECT name, COALESCE(attributes, '{}'::jsonb)::text
			FROM mem_entities
			WHERE kind = 'person'
			  AND status <> 'archived'
			  AND attributes IS NOT NULL
			  AND attributes::text <> '{}'
			LIMIT 500
		`)
		if err != nil {
			// Loud: a checklist that silently returns nothing is indistinguishable
			// from a world with no birthdays in it.
			return nil, fmt.Errorf("anniversary scan: %w", err)
		}
		defer rows.Close()

		now := time.Now().UTC()
		var out []Finding
		for rows.Next() {
			var name, attrsJSON string
			if rows.Scan(&name, &attrsJSON) != nil {
				continue
			}
			var attrs map[string]any
			if json.Unmarshal([]byte(attrsJSON), &attrs) != nil {
				continue
			}
			for key, raw := range attrs {
				if !isOccasionKey(key) {
					continue
				}
				val, ok := raw.(string)
				if !ok {
					continue
				}
				when, ok := nextOccurrence(val, now)
				if !ok {
					continue
				}
				until := when.Sub(now)
				if until < 0 || until > lookAhead {
					continue
				}
				out = append(out, Finding{
					Kind:  "pattern",
					Title: name + "'s " + humanOccasion(key) + " is " + whenWords(until, when),
					Detail: "I know this from what you have told me and from your calls. " +
						"Say the word and I will ring them for you, or draft something to send.",
					Source: "anniversary",
				})
			}
		}
		return out, rows.Err()
	}
}

func isOccasionKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	for _, want := range occasionKeys {
		if strings.Contains(k, want) {
			return true
		}
	}
	return false
}

func humanOccasion(key string) string {
	k := strings.ToLower(key)
	switch {
	case strings.Contains(k, "birth"):
		return "birthday"
	case strings.Contains(k, "anniversary"):
		return "anniversary"
	}
	return strings.ReplaceAll(k, "_", " ")
}

// nextOccurrence finds the next time this date comes round.
//
// The YEAR is deliberately ignored: a birthday recorded as 1991-07-10 is about
// the 10th of July, forever. Accepts the forms an LLM extractor actually writes.
func nextOccurrence(value string, now time.Time) (time.Time, bool) {
	v := strings.TrimSpace(value)
	if v == "" {
		return time.Time{}, false
	}
	formats := []string{
		"2006-01-02", "01-02", "1/2", "01/02", "Jan 2", "January 2",
		"2 Jan", "2 January", "Jan 2, 2006", "January 2, 2006", "2006/01/02",
	}
	for _, f := range formats {
		t, err := time.Parse(f, v)
		if err != nil {
			continue
		}
		// Roll it onto this year; if that has already passed, next year.
		next := time.Date(now.Year(), t.Month(), t.Day(), 9, 0, 0, 0, time.UTC)
		if next.Before(now.Add(-24 * time.Hour)) {
			next = next.AddDate(1, 0, 0)
		}
		return next, true
	}
	return time.Time{}, false
}

// whenWords says it the way a person would.
func whenWords(until time.Duration, when time.Time) string {
	days := int(until.Hours() / 24)
	switch {
	case days <= 0:
		return "today"
	case days == 1:
		return "tomorrow"
	case days <= 6:
		return "on " + when.Format("Monday")
	}
	return "on " + when.Format("Monday, January 2")
}
