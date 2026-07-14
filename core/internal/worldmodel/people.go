// people.go — the boss's circle, and the durable facts about them.
//
// The extractor next door (extract.go) is regex-only: it can spot an email
// address, a repo, a hostname. It cannot spot a PERSON. So "Jarvis called Phumi
// to wish her a happy birthday" taught the world model precisely nothing, and
// the people in the boss's life existed only as rows in a phone book and lines in
// a transcript. Anything that wanted to reason about them (a checklist that
// notices a birthday coming round, a call that should know the history) had
// nothing to read.
//
// Two writers here, split the way Rule #1b demands:
//
//	SyncContacts       - MECHANIC. Everyone in his phone book is a person he
//	                     knows. That needs no judgment, so it is deterministic
//	                     code: contact in, entity out, every time.
//	ExtractPeopleFacts - JUDGMENT. What is a durable fact about someone (her
//	                     birthday, that she is his wife, that she is Valentino's
//	                     mother) versus a passing detail of one call, is exactly
//	                     what an LLM is for. It proposes; code upserts.
package worldmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Drafter is the one-shot LLM seam, mirroring llm.Drafter locally so this
// package stays free of the llm stack.
type Drafter interface {
	Draft(ctx context.Context, model, system, userPrompt string, maxTokens int64) (string, error)
}

// PeopleReport counts what a pass learned.
type PeopleReport struct {
	Contacts int `json:"contacts"` // phone-book people synced
	Scanned  int `json:"scanned"`  // observations read
	People   int `json:"people"`   // person entities written
	Facts    int `json:"facts"`    // durable attributes learned
}

// Changed reports whether the pass did anything worth mentioning.
func (r PeopleReport) Changed() bool { return r.Contacts+r.People+r.Facts > 0 }

// SyncContacts makes every contact in the phone book a person in the world
// model. Deterministic, idempotent, no LLM: if he has their number, they are
// someone he knows, and the rest of the system should be able to reason about
// them by name.
func (s *Store) SyncContacts(ctx context.Context) (int, error) {
	if s == nil || s.pool == nil {
		return 0, nil
	}
	// The boss is not a person in his own circle: his own cell is in the phone
	// book so Jarvis recognizes him when he rings, not so the world model can
	// model him as an acquaintance. Who HE is lives in the boss profile.
	rows, err := s.pool.Query(ctx, `
		SELECT c.name, c.number, COALESCE(c.kind,''), COALESCE(c.location,''), COALESCE(c.note,''), COALESCE(c.aliases, '{}')
		FROM mem_contacts c
		WHERE right(regexp_replace(c.number, '[^0-9]', '', 'g'), 10) IS DISTINCT FROM (
			SELECT right(regexp_replace(value, '[^0-9]', '', 'g'), 10)
			FROM infinity_meta WHERE key = 'vault.boss_cell'
		)
	`)
	if err != nil {
		return 0, fmt.Errorf("worldmodel: read phone book: %w", err)
	}
	defer rows.Close()

	n := 0
	for rows.Next() {
		var name, number, kind, location, note string
		var aliases []string
		if err := rows.Scan(&name, &number, &kind, &location, &note, &aliases); err != nil {
			return n, err
		}
		if strings.TrimSpace(name) == "" || strings.EqualFold(name, "unknown") {
			continue
		}
		entityKind := "person"
		if kind == "org" {
			entityKind = "org"
		}
		attrs := map[string]any{"phone": number, "source": "phone_book"}
		if location != "" {
			attrs["location"] = location
		}
		summary := note
		if summary == "" {
			summary = "In the boss's phone book."
		}
		if _, err := s.UpsertEntity(ctx, &Entity{
			Kind:       entityKind,
			Name:       name,
			Aliases:    aliases,
			Attributes: attrs,
			Summary:    summary,
			// Someone he actually phones matters more than a hostname we saw once.
			Salience: 70,
		}); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// peopleSystem is the extraction instruction. Same precedent as llm/critic.go
// and the call summarizer: an auxiliary LLM task's instruction lives beside its
// seam. It asks only for what is DURABLE, because a world model full of
// one-off details is noise, and noise is what makes an assistant tiresome.
const peopleSystem = `You read an assistant's raw memory of what happened (calls, conversations, messages) and pull out what is durably TRUE about the PEOPLE in the boss's life.

Return ONLY a JSON array, no prose. Each element:
{"name":"Ariana","aliases":["Ari"],"relationship":"his wife","summary":"one sentence about who they are to him","facts":{"birthday":"1991-07-10","children":"Valentino"}}

Rules:
- Only real, named people. Never the boss himself, never Jarvis, never a business.
- Only DURABLE facts: a birthday, a relationship, a job, a child's name, where they live, something that will still be true next year. Never what happened on one call, never a mood, never a passing plan.
- Dates as YYYY-MM-DD when you know the year, otherwise MM-DD. Never invent one. If a birthday was mentioned only as an occasion ("call her, it's her birthday today"), use the date of that call.
- Omit a person entirely if you learned nothing durable about them.
- Never guess. An empty array is a perfectly good answer.`

// ExtractPeopleFacts reads recent memory and writes what it learned about the
// people in it onto their entities. The LLM proposes; this code upserts, so a
// fact only ever lands through one path and can be audited.
func (s *Store) ExtractPeopleFacts(ctx context.Context, d Drafter, limit int) (PeopleReport, error) {
	rep := PeopleReport{}
	if s == nil || s.pool == nil || d == nil {
		return rep, nil
	}
	if limit <= 0 || limit > 300 {
		limit = 60
	}

	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(raw_text, '')
		  FROM mem_observations
		 WHERE raw_text IS NOT NULL AND raw_text <> ''
		 ORDER BY created_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return rep, fmt.Errorf("worldmodel: read observations: %w", err)
	}
	var texts []string
	for rows.Next() {
		var t string
		if rows.Scan(&t) != nil {
			continue
		}
		if len(t) > 4000 {
			t = t[:4000]
		}
		texts = append(texts, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return rep, err
	}
	rep.Scanned = len(texts)
	if rep.Scanned == 0 {
		return rep, nil
	}

	// Hand the model the names it ALREADY knows these people by. Without this it
	// writes "Ariana Malaby" where the phone book says "Ariana", and the boss
	// ends up with the same wife twice.
	known := s.knownNames(ctx)
	prompt := "Memory:\n\n" + strings.Join(texts, "\n\n---\n\n")
	if known != "" {
		prompt = "People already known, USE THESE EXACT NAMES when it is one of them:\n" + known + "\n\n" + prompt
	}
	out, err := d.Draft(ctx, "", peopleSystem, prompt, 2000)
	if err != nil {
		// Loud: a silent failure here looks exactly like "the boss knows nobody".
		return rep, fmt.Errorf("worldmodel: people extraction failed: %w", err)
	}

	var people []struct {
		Name         string            `json:"name"`
		Aliases      []string          `json:"aliases"`
		Relationship string            `json:"relationship"`
		Summary      string            `json:"summary"`
		Facts        map[string]string `json:"facts"`
	}
	if err := json.Unmarshal([]byte(extractJSONArray(out)), &people); err != nil {
		return rep, fmt.Errorf("worldmodel: people extraction returned unusable JSON: %w", err)
	}

	for _, p := range people {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			continue
		}
		attrs := map[string]any{"source": "people_extract"}
		if r := strings.TrimSpace(p.Relationship); r != "" {
			attrs["relationship"] = r
		}
		for k, v := range p.Facts {
			k = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(k, " ", "_")))
			v = strings.TrimSpace(v)
			if k == "" || v == "" {
				continue
			}
			attrs[k] = v
			rep.Facts++
		}
		if _, err := s.UpsertEntity(ctx, &Entity{
			Kind:       "person",
			Name:       name,
			Aliases:    p.Aliases,
			Attributes: attrs,
			Summary:    strings.TrimSpace(p.Summary),
			Salience:   75,
		}); err != nil {
			return rep, err
		}
		rep.People++
	}
	return rep, nil
}

// knownNames lists the people already in the phone book and the world model, so
// the extractor names them the same way twice.
func (s *Store) knownNames(ctx context.Context) string {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT name FROM (
			SELECT name FROM mem_contacts
			UNION
			SELECT name FROM mem_entities WHERE kind = 'person' AND status <> 'archived'
		) x WHERE name <> '' AND lower(name) <> 'unknown'
		LIMIT 200
	`)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			names = append(names, "- "+n)
		}
	}
	return strings.Join(names, "\n")
}

// extractJSONArray pulls the JSON array out of a model reply that may have
// wrapped it in prose or a code fence, however firmly it was told not to.
func extractJSONArray(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "["); i >= 0 {
		if j := strings.LastIndex(s, "]"); j > i {
			return s[i : j+1]
		}
	}
	return "[]"
}
