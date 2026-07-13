// Package contacts is the phone book: the boss's people and places, resolvable
// by NAME so he can say "call Ariana" instead of reciting ten digits.
//
// It backs the contact book he already has on the dashboard (PhoneCard ->
// ContactBookModal -> GET /api/phone/contacts). That surface used to be a
// read-only projection of call history, which meant a contact could only exist
// after a call and could never be looked up by name. Same surface, real spine.
//
// Both directions run through this one store, deliberately:
//
//	outbound  "call Ariana"        -> Resolve("Ariana")  -> +19293100906
//	inbound   +19293100906 rings   -> ByNumber(...)      -> "Ariana, his wife"
package contacts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Contact is one entry in the book.
type Contact struct {
	Name         string     `json:"name"`
	Aliases      []string   `json:"aliases,omitempty"`
	Number       string     `json:"number"`
	Kind         string     `json:"kind,omitempty"`     // person | org
	Location     string     `json:"location,omitempty"` // "the one on Preston Road"
	Note         string     `json:"note,omitempty"`
	Source       string     `json:"source,omitempty"`
	TimesCalled  int        `json:"times_called"`
	LastCalledAt *time.Time `json:"last_called_at,omitempty"`
}

// Store is the phone book, backed by mem_contacts.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Normalize reduces a name to its lookup form: lowercase, letters and digits
// only. "Goodfellas Pizza!" and "goodfellas pizza" are the same place.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NormalizeNumber returns the dialable E.164 form of a number in any of the
// shapes a human says or types it ("929-310-0906", "(929) 310 0906",
// "+1 929 310 0906"), or "" when it cannot be one.
//
// This is a gate, not a convenience. A contact saved as "929-310-0906" looks
// perfectly fine in the book and then fails the E.164 check at dial time, which
// would mean a contact that exists, reads correctly, and cannot be called: the
// worst kind of broken, because it looks fine until the moment it matters.
func NormalizeNumber(s string) string {
	raw := strings.TrimSpace(s)
	d := onlyDigits(raw)
	switch {
	case strings.HasPrefix(raw, "+") && len(d) >= 8 && len(d) <= 15:
		return "+" + d
	case len(d) == 10: // bare US number, the way the boss says it
		return "+1" + d
	case len(d) == 11 && d[0] == '1':
		return "+" + d
	}
	return ""
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Upsert writes a contact, keyed on the number. Calling the same line again
// enriches what is already there rather than forking a duplicate: a later call
// that learns a location or a better name fills those in, and aliases
// accumulate. Empty fields never overwrite known ones.
func (s *Store) Upsert(ctx context.Context, c Contact) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("contacts: no database pool")
	}
	name := strings.TrimSpace(c.Name)
	number := NormalizeNumber(c.Number)
	if number == "" {
		return fmt.Errorf("contacts: %q is not a dialable number, so it cannot go in the phone book "+
			"(needs 10 US digits, or + and a country code)", strings.TrimSpace(c.Number))
	}
	if name == "" {
		return fmt.Errorf("contacts: a contact needs a name (that is the whole point of the book)")
	}
	kind := strings.TrimSpace(c.Kind)
	if kind == "" {
		kind = "person"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_contacts (name, name_norm, aliases, number, kind, location, note, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (number) DO UPDATE SET
			name     = COALESCE(NULLIF(EXCLUDED.name, ''), mem_contacts.name),
			name_norm= COALESCE(NULLIF(EXCLUDED.name_norm, ''), mem_contacts.name_norm),
			-- union of aliases, so every way he has ever named them keeps working
			aliases  = ARRAY(SELECT DISTINCT unnest(mem_contacts.aliases || EXCLUDED.aliases)),
			kind     = COALESCE(NULLIF(EXCLUDED.kind, ''), mem_contacts.kind),
			location = COALESCE(NULLIF(EXCLUDED.location, ''), mem_contacts.location),
			note     = COALESCE(NULLIF(EXCLUDED.note, ''), mem_contacts.note),
			updated_at = NOW()
	`, name, Normalize(name), c.Aliases, number, kind, strings.TrimSpace(c.Location), strings.TrimSpace(c.Note), strings.TrimSpace(c.Source))
	return err
}

// MarkCalled records that this number was just dialed.
func (s *Store) MarkCalled(ctx context.Context, number string) {
	if s == nil || s.pool == nil {
		return
	}
	_, _ = s.pool.Exec(ctx, `
		UPDATE mem_contacts
		SET times_called = times_called + 1, last_called_at = NOW(), updated_at = NOW()
		WHERE number = $1
	`, strings.TrimSpace(number))
}

// Delete removes a contact from the book. The boss's call: the agent has no
// verb for this, deliberately, so nothing autonomous can quietly forget someone.
func (s *Store) Delete(ctx context.Context, number string) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("contacts: no database pool")
	}
	n := NormalizeNumber(number)
	if n == "" {
		return fmt.Errorf("contacts: %q is not a number I can look up", number)
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM mem_contacts WHERE number = $1`, n)
	return err
}

// ByNumber is the inbound direction: who is ringing.
func (s *Store) ByNumber(ctx context.Context, number string) (*Contact, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("contacts: no database pool")
	}
	digits := lastDigits(number, 10)
	if digits == "" {
		return nil, nil
	}
	// Match on the last 10 digits so "+19293100906", "929-310-0906" and
	// "9293100906" all find the same person.
	rows, err := s.query(ctx, `
		SELECT name, aliases, number, kind, location, note, source, times_called, last_called_at
		FROM mem_contacts
		WHERE right(regexp_replace(number, '[^0-9]', '', 'g'), 10) = $1
		LIMIT 1
	`, digits)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// Resolve is the outbound direction: turn what the boss SAID into who he meant.
//
// Exact name first, then alias, then a forgiving prefix/substring match, because
// he says "Ariana" for "Ariana Mbeki" and "goodfellas" for "Goodfellas Pizza".
// It returns every candidate rather than picking one: choosing between two
// Goodfellas is judgment, and judgment belongs to the agent (and to the boss,
// who gets asked "the one on Preston Road?"), never to a silent SQL LIMIT 1.
func (s *Store) Resolve(ctx context.Context, query string) ([]Contact, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("contacts: no database pool")
	}
	q := Normalize(query)
	if q == "" {
		return nil, nil
	}
	// Fuzzy matching is for "goodfellas" finding "Goodfellas Pizza", not for
	// "an" finding Ariana. Below three characters, only an exact name or alias
	// counts: a loose match on a short word would put the wrong person on the
	// phone, which is worse than admitting we do not know who he meant.
	fuzzy := len(q) >= 3
	return s.query(ctx, `
		SELECT name, aliases, number, kind, location, note, source, times_called, last_called_at
		FROM mem_contacts
		WHERE name_norm = $1
		   OR EXISTS (
		        SELECT 1 FROM unnest(aliases) a
		        WHERE lower(regexp_replace(a, '[^a-zA-Z0-9]', '', 'g')) = $1
		      )
		   OR ($2 AND name_norm LIKE $1 || '%')
		   OR ($2 AND position($1 IN name_norm) > 0)
		ORDER BY
			(name_norm = $1) DESC,          -- an exact name wins
			times_called DESC,              -- then whoever he actually calls
			last_called_at DESC NULLS LAST
		LIMIT 10
	`, q, fuzzy)
}

// All lists the book, most recently active first. Feeds the dashboard.
func (s *Store) All(ctx context.Context, limit int) ([]Contact, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("contacts: no database pool")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.query(ctx, `
		SELECT name, aliases, number, kind, location, note, source, times_called, last_called_at
		FROM mem_contacts
		ORDER BY COALESCE(last_called_at, updated_at) DESC
		LIMIT $1
	`, limit)
}

func (s *Store) query(ctx context.Context, sql string, args ...any) ([]Contact, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.Name, &c.Aliases, &c.Number, &c.Kind, &c.Location,
			&c.Note, &c.Source, &c.TimesCalled, &c.LastCalledAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// lastDigits extracts the trailing n digits of any phone-ish string.
func lastDigits(s string, n int) string {
	var b []rune
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b = append(b, r)
		}
	}
	if len(b) == 0 {
		return ""
	}
	if len(b) < n {
		return string(b)
	}
	return string(b[len(b)-n:])
}

// Describe renders candidates for an LLM to read aloud or reason over. Plain
// language, because the call agent speaks this to the boss.
func Describe(cs []Contact) string {
	if len(cs) == 0 {
		return "No contact by that name in the phone book."
	}
	var b strings.Builder
	for i, c := range cs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(c.Name + " (" + c.Number + ")")
		if c.Location != "" {
			b.WriteString(", " + c.Location)
		}
		if c.Kind == "org" {
			b.WriteString(" [business]")
		}
		if c.Note != "" {
			b.WriteString(" - " + clip(c.Note, 160))
		}
	}
	return b.String()
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
