package memory

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"
)

var reflectionWordRe = regexp.MustCompile(`[a-z0-9]+`)

type ReflectionChain struct {
	ID                  string    `json:"id"`
	Topic               string    `json:"topic"`
	Lesson              string    `json:"lesson"`
	SourceReflectionIDs []string  `json:"source_reflection_ids"`
	Occurrences         int       `json:"occurrences"`
	Confidence          float64   `json:"confidence"`
	FirstSeenAt         time.Time `json:"first_seen_at"`
	LastSeenAt          time.Time `json:"last_seen_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// BuildReflectionChains clusters repeated reflection lessons across sessions.
// It is deterministic and cheap: if a lesson shows up 2+ times with similar
// keywords, it becomes a meta-lesson the Memory tab can show as a chain.
func (r *Reflector) BuildReflectionChains(ctx context.Context, limit int) (int, error) {
	if r == nil || r.pool == nil {
		return 0, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, COALESCE(lessons::text, '[]'), created_at
		  FROM mem_reflections
		 WHERE jsonb_array_length(COALESCE(lessons, '[]'::jsonb)) > 0
		 ORDER BY created_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type item struct {
		id         string
		text       string
		confidence float64
		at         time.Time
	}
	clusters := map[string][]item{}
	for rows.Next() {
		var id, raw string
		var at time.Time
		if err := rows.Scan(&id, &raw, &at); err != nil {
			return 0, err
		}
		var lessons []Lesson
		_ = json.Unmarshal([]byte(raw), &lessons)
		for _, lesson := range lessons {
			text := strings.TrimSpace(lesson.Text)
			if text == "" {
				continue
			}
			key := reflectionTopic(text)
			if key == "" {
				continue
			}
			clusters[key] = append(clusters[key], item{id: id, text: text, confidence: lesson.Confidence, at: at})
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	upserted := 0
	for topic, items := range clusters {
		seen := map[string]bool{}
		var ids []string
		var first, last time.Time
		conf := 0.0
		best := items[0].text
		for _, it := range items {
			if !seen[it.id] {
				ids = append(ids, it.id)
				seen[it.id] = true
			}
			if first.IsZero() || it.at.Before(first) {
				first = it.at
			}
			if last.IsZero() || it.at.After(last) {
				last = it.at
			}
			if it.confidence > conf {
				conf = it.confidence
				best = it.text
			}
		}
		if len(ids) < 2 {
			continue
		}
		_, err := r.pool.Exec(ctx, `
			INSERT INTO mem_reflection_chains
			  (topic, lesson, source_reflection_ids, occurrences, confidence, first_seen_at, last_seen_at, updated_at)
			VALUES ($1, $2, $3::uuid[], $4, $5, $6, $7, NOW())
			ON CONFLICT (topic) DO UPDATE SET
			  lesson = EXCLUDED.lesson,
			  source_reflection_ids = EXCLUDED.source_reflection_ids,
			  occurrences = EXCLUDED.occurrences,
			  confidence = EXCLUDED.confidence,
			  first_seen_at = LEAST(mem_reflection_chains.first_seen_at, EXCLUDED.first_seen_at),
			  last_seen_at = GREATEST(mem_reflection_chains.last_seen_at, EXCLUDED.last_seen_at),
			  updated_at = NOW()
		`, topic, best, ids, len(ids), conf, first, last)
		if err != nil {
			return upserted, err
		}
		upserted++
	}
	return upserted, nil
}

func (r *Reflector) ReflectionChains(ctx context.Context, limit int) ([]ReflectionChain, error) {
	if r == nil || r.pool == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, topic, lesson, COALESCE(array_to_json(source_reflection_ids)::text, '[]'), occurrences,
		       confidence, first_seen_at, last_seen_at, updated_at
		  FROM mem_reflection_chains
		 ORDER BY updated_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReflectionChain
	for rows.Next() {
		var c ReflectionChain
		var idsJSON string
		if err := rows.Scan(&c.ID, &c.Topic, &c.Lesson, &idsJSON, &c.Occurrences,
			&c.Confidence, &c.FirstSeenAt, &c.LastSeenAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(idsJSON), &c.SourceReflectionIDs)
		out = append(out, c)
	}
	return out, rows.Err()
}

func reflectionTopic(text string) string {
	words := reflectionWordRe.FindAllString(strings.ToLower(text), -1)
	stop := map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "as": true, "before": true,
		"be": true, "for": true, "from": true, "in": true, "is": true, "it": true,
		"of": true, "on": true, "or": true, "the": true, "to": true, "with": true,
		"when": true, "you": true, "your": true, "should": true, "must": true,
	}
	var keep []string
	for _, w := range words {
		if len(w) < 4 || stop[w] {
			continue
		}
		keep = append(keep, w)
	}
	sort.Strings(keep)
	if len(keep) > 5 {
		keep = keep[:5]
	}
	return strings.Join(keep, ":")
}
