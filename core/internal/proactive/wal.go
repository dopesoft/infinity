// Package proactive implements the proactive engine — IntentFlow + Heartbeat
// + WAL + Working Buffer.
//
// This file holds the WAL Protocol: every user message is scanned for
// "load-bearing" patterns (corrections, preferences, decisions, dates, URLs,
// proper nouns) before the agent composes its response. Matches are written
// to mem_session_state so they survive context compaction.
package proactive

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// walTriggers is the regex set lifted from Hal's Proactive Agent skill
// (MIT-0). They are intentionally permissive — false positives are cheaper
// than missing a correction.
var walTriggers = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:it'?s|that'?s|actually)\s+\w+,?\s+not\s+\w+`),                                  // corrections
	regexp.MustCompile(`(?i)\bi\s+(?:like|prefer|don'?t\s+like|hate|love)\b[^.]{0,80}`),                          // preferences
	regexp.MustCompile(`(?i)\b(?:let'?s|let\s+us)\s+(?:go\s+with|use|pick|switch\s+to)\b[^.]{0,60}`),             // decisions
	regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`),                                                                  // ISO dates
	regexp.MustCompile(`(?i)\b(?:on|by|until|before|after)\s+(?:mon|tue|wed|thu|fri|sat|sun)[a-z]*\b[^.]{0,40}`), // weekday deadlines
	regexp.MustCompile(`\bhttps?://\S+\b`),                                                                       // URLs
	regexp.MustCompile(`(?i)\bcall(?:ed|s)?\s+(?:me|it|him|her|them)\s+\w+\b`),                                   // naming preferences
	regexp.MustCompile(`(?i)\bremember\s+(?:that|this|to)\b[^.]{0,80}`),                                          // explicit memory hints
}

// WAL extracts load-bearing fragments from a user message.
type WAL struct {
	pool *pgxpool.Pool

	mu sync.Mutex
}

func NewWAL(p *pgxpool.Pool) *WAL { return &WAL{pool: p} }

// Extract scans message and returns every fragment whose regex matched.
// De-duplicates exact matches.
func Extract(message string) []string {
	if message == "" {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, re := range walTriggers {
		for _, m := range re.FindAllString(message, -1) {
			m = strings.TrimSpace(m)
			if m == "" || seen[m] {
				continue
			}
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// Append writes the extracted fragments to mem_session_state for `sessionID`.
// Each entry is timestamped on its own line.
func (w *WAL) Append(ctx context.Context, sessionID string, extracted []string) error {
	if w == nil || w.pool == nil || sessionID == "" || len(extracted) == 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var addition strings.Builder
	for _, e := range extracted {
		fmt.Fprintf(&addition, "- [%s] %s\n", now, e)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	_, err := w.pool.Exec(ctx, `
		INSERT INTO mem_session_state (session_id, body, updated_at)
		VALUES ($1::uuid, $2, NOW())
		ON CONFLICT (session_id) DO UPDATE SET
		  body = mem_session_state.body || EXCLUDED.body,
		  updated_at = NOW()
	`, sessionID, addition.String())
	return err
}

// Read returns the current SESSION-STATE body for a session. Used by the
// Compaction Recovery flow.
func (w *WAL) Read(ctx context.Context, sessionID string) (string, error) {
	if w == nil || w.pool == nil || sessionID == "" {
		return "", nil
	}
	var body string
	err := w.pool.QueryRow(ctx,
		`SELECT body FROM mem_session_state WHERE session_id = $1::uuid`, sessionID).Scan(&body)
	if err != nil {
		return "", nil // no row → empty
	}
	return body, nil
}
