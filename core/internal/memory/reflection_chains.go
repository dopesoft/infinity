package memory

import (
	"context"
	"encoding/json"
	"math"
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

// chainCosineThreshold is the minimum cosine similarity for two lesson
// embeddings to land in the same chain. Reflection lessons are paraphrases of
// the same insight ("skill returned docs not execution data" vs "skill
// response was recipe metadata, not results") that share almost no exact
// keywords, so the previous alphabetical-first-5-keyword key never grouped
// them and the table stayed empty. Embedding similarity catches the paraphrase.
const chainCosineThreshold = 0.72

type chainItem struct {
	id         string
	text       string
	confidence float64
	at         time.Time
	vec        []float32
}

// BuildReflectionChains clusters repeated reflection lessons across sessions
// into meta-lessons the Memory tab shows as chains and the
// ReflectionChainsProvider injects into the system prompt. Clustering is by
// embedding cosine similarity (paraphrase-robust); if the embedder is
// unavailable it falls back to the lexical keyword key so the loop still runs
// in a degraded deployment. A cluster needs ≥2 distinct source reflections to
// become a chain.
func (r *Reflector) BuildReflectionChains(ctx context.Context, limit int) (int, error) {
	if r == nil || r.pool == nil {
		return 0, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	// A MATERIALIZED CTE coerces `lessons` to a guaranteed-array column FIRST,
	// so jsonb_array_length() in the outer WHERE can never touch a scalar.
	// The earlier inline `CASE ... THEN jsonb_array_length(...) END > 0` guard
	// was insufficient: Postgres may still evaluate the length call on a scalar
	// row when the planner reorders the CASE branches, which is exactly how
	// nightly-cognition kept dying with `cannot get array length of a scalar`
	// (SQLSTATE 22023) even after the typeof guard shipped. Materializing the
	// normalization is the only form the planner can't flatten around.
	rows, err := r.pool.Query(ctx, `
		WITH normalized AS MATERIALIZED (
			SELECT id::text AS id,
			       CASE WHEN jsonb_typeof(COALESCE(lessons, '[]'::jsonb)) = 'array'
			            THEN COALESCE(lessons, '[]'::jsonb)
			            ELSE '[]'::jsonb
			       END AS arr,
			       created_at
			  FROM mem_reflections
		)
		SELECT id, arr::text, created_at
		  FROM normalized
		 WHERE jsonb_array_length(arr) > 0
		 ORDER BY created_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var items []chainItem
	for rows.Next() {
		var id, raw string
		var at time.Time
		if err := rows.Scan(&id, &raw, &at); err != nil {
			return 0, err
		}
		lessons := normalizeLessonsJSON(raw)
		for _, lesson := range lessons {
			text := strings.TrimSpace(lesson.Text)
			if text == "" {
				continue
			}
			items = append(items, chainItem{id: id, text: text, confidence: lesson.Confidence, at: at})
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	clusters := r.clusterLessons(ctx, items)

	upserted := 0
	for _, members := range clusters {
		seen := map[string]bool{}
		var ids []string
		var first, last time.Time
		conf := 0.0
		best := members[0].text
		var allText []string
		for _, it := range members {
			allText = append(allText, it.text)
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
		topic := clusterTopic(allText)
		if topic == "" {
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

// clusterLessons groups lesson items semantically. It embeds each lesson and
// greedily clusters by cosine similarity against running centroids. If
// embedding yields too few vectors (embedder down / stubbed), it falls back to
// grouping by the lexical keyword key so the loop degrades instead of failing.
func (r *Reflector) clusterLessons(ctx context.Context, items []chainItem) [][]chainItem {
	// Embed each distinct lesson text once.
	vecByText := map[string][]float32{}
	embedded := 0
	for i := range items {
		t := items[i].text
		if v, ok := vecByText[t]; ok {
			items[i].vec = v
			if v != nil {
				embedded++
			}
			continue
		}
		v, err := r.embedder.Embed(ctx, t)
		if err != nil || len(v) == 0 {
			vecByText[t] = nil
			continue
		}
		vecByText[t] = v
		items[i].vec = v
		embedded++
	}

	// Lexical fallback when embeddings are unavailable.
	if embedded < 2 {
		byKey := map[string][]chainItem{}
		for _, it := range items {
			key := reflectionTopic(it.text)
			if key == "" {
				continue
			}
			byKey[key] = append(byKey[key], it)
		}
		out := make([][]chainItem, 0, len(byKey))
		for _, v := range byKey {
			out = append(out, v)
		}
		return out
	}

	// Greedy cosine clustering against incremental centroids.
	type cluster struct {
		centroid []float32
		members  []chainItem
	}
	var clusters []*cluster
	for _, it := range items {
		if it.vec == nil {
			continue // skip un-embeddable items in the semantic pass
		}
		bestIdx, bestSim := -1, chainCosineThreshold
		for ci, c := range clusters {
			if sim := cosine(it.vec, c.centroid); sim >= bestSim {
				bestSim = sim
				bestIdx = ci
			}
		}
		if bestIdx < 0 {
			clusters = append(clusters, &cluster{centroid: append([]float32(nil), it.vec...), members: []chainItem{it}})
			continue
		}
		c := clusters[bestIdx]
		// Incremental centroid mean.
		n := float32(len(c.members))
		for d := range c.centroid {
			c.centroid[d] = (c.centroid[d]*n + it.vec[d]) / (n + 1)
		}
		c.members = append(c.members, it)
	}
	out := make([][]chainItem, 0, len(clusters))
	for _, c := range clusters {
		out = append(out, c.members)
	}
	return out
}

// cosine returns the cosine similarity of two equal-length vectors. Returns 0
// for mismatched or zero-norm inputs.
func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// clusterTopic derives a stable dedup key for a cluster from the most frequent
// salient keywords across all member lessons. Aggregating over the whole
// cluster (rather than one member's first-5 keywords) keeps the key stable
// across nightly runs as long as the cluster's dominant vocabulary persists,
// so ON CONFLICT (topic) updates the same chain instead of spawning duplicates.
func clusterTopic(texts []string) string {
	freq := map[string]int{}
	for _, t := range texts {
		seen := map[string]bool{}
		for _, w := range reflectionKeywords(t) {
			if seen[w] {
				continue
			}
			seen[w] = true
			freq[w]++
		}
	}
	type kv struct {
		w string
		n int
	}
	var kvs []kv
	for w, n := range freq {
		kvs = append(kvs, kv{w, n})
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].n != kvs[j].n {
			return kvs[i].n > kvs[j].n
		}
		return kvs[i].w < kvs[j].w
	})
	var keep []string
	for _, k := range kvs {
		keep = append(keep, k.w)
		if len(keep) >= 4 {
			break
		}
	}
	sort.Strings(keep)
	return strings.Join(keep, ":")
}

func normalizeLessonsJSON(raw string) []Lesson {
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil
	}
	arr, ok := decoded.([]any)
	if !ok {
		return nil
	}
	buf, err := json.Marshal(arr)
	if err != nil {
		return nil
	}
	var lessons []Lesson
	if err := json.Unmarshal(buf, &lessons); err != nil {
		return nil
	}
	return lessons
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

var reflectionStopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "before": true,
	"be": true, "for": true, "from": true, "in": true, "is": true, "it": true,
	"of": true, "on": true, "or": true, "the": true, "to": true, "with": true,
	"when": true, "you": true, "your": true, "should": true, "must": true,
}

// reflectionKeywords returns the salient (non-stopword, ≥4 char) lowercased
// words of a lesson, in document order. Shared by clusterTopic (aggregate
// keying) and reflectionTopic (lexical fallback).
func reflectionKeywords(text string) []string {
	words := reflectionWordRe.FindAllString(strings.ToLower(text), -1)
	var keep []string
	for _, w := range words {
		if len(w) < 4 || reflectionStopWords[w] {
			continue
		}
		keep = append(keep, w)
	}
	return keep
}

func reflectionTopic(text string) string {
	keep := reflectionKeywords(text)
	sort.Strings(keep)
	if len(keep) > 5 {
		keep = keep[:5]
	}
	return strings.Join(keep, ":")
}
