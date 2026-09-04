package plasticity

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/dopesoft/infinity/core/internal/embed"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// Provider injects gym lessons into the agent's system prompt every turn.
// This is the reflex layer - no model training; the database is the
// learned policy. The provider's job is to surface the lessons most
// likely to change behavior on THIS turn.
//
// Retrieval is hybrid (cosine + lexical + recency) and the rendering
// splits lessons into two buckets so the model treats them differently:
//
//  1. AVOID  (label='rejected' / 'corrected'): things that went wrong
//     before. These get higher weight because negative lessons change
//     behavior more reliably than positive ones.
//  2. APPLY  (label='accepted', high score): things that worked. Useful
//     pattern matches when the agent recognises the situation.
//
// Without an embedder, retrieval degrades to the original lexical+score
// path. Rows without an embedding column populated yet (pre-migration-057)
// are still scored via lexical overlap so the layer never goes dark.
type Provider struct {
	pool     *pgxpool.Pool
	embedder embed.Embedder
}

// NewProvider builds a reflex provider. Pass nil for the embedder to fall
// back to lexical-only retrieval (works fine; cosine just adds semantic
// reach).
func NewProvider(pool *pgxpool.Pool, embedder embed.Embedder) *Provider {
	return &Provider{pool: pool, embedder: embedder}
}

// Tunables. These are the "feels right" values for a single-user agent.
const (
	candidateLimit = 80   // pulled from the table before ranking
	avoidLimit     = 6    // rendered "AVOID" lessons per turn
	applyLimit     = 6    // rendered "APPLY" lessons per turn
	cosineMin      = 0.45 // cosine sim threshold to count as a vector hit
	// blockMaxChars caps the whole <gym_reflex> block. Measured at 6.2K chars
	// on real turns (2026-09-04), re-sent on every LLM call of every turn on
	// every brain. Lessons are already ranked, so the cap drops the
	// lowest-ranked ones from each bucket until the block fits.
	blockMaxChars = 3000
)

func (p *Provider) BuildSystemPrefix(ctx context.Context, _, query string) (string, error) {
	if p == nil || p.pool == nil {
		return "", nil
	}
	if ok, err := NewStore(p.pool).tableExists(ctx, "mem_training_examples"); err != nil || !ok {
		return "", err
	}

	// Two retrieval paths run in parallel-of-thought:
	//   1. Vector cosine when the embedder is available and the query has
	//      enough signal. This pulls semantically related lessons even when
	//      none of the words overlap.
	//   2. Recency + label-based fallback that always runs and seeds the
	//      candidate pool with the freshest high-signal rows.
	candidates, err := p.fetchCandidates(ctx, query)
	if err != nil {
		return "", err
	}
	if len(candidates) == 0 {
		return "", nil
	}

	// Score each candidate. Lexical overlap is a cheap tiebreaker; cosine
	// (already part of the SELECT) is the primary signal when present.
	qTokens := tokenSet(query)
	for i := range candidates {
		candidates[i].LexMatch = lexicalOverlap(qTokens, candidates[i].TaskKind+" "+candidates[i].Input+" "+candidates[i].Output+" "+candidates[i].Label)
	}

	// Split into AVOID and APPLY buckets. The label decides; score within
	// the bucket sorts (high-confidence first), then cosine, then lex,
	// then recency-implicit (the SELECT already returned newest-first).
	var avoid, apply []promptExample
	for _, ex := range candidates {
		if isNegative(ex.Label) {
			avoid = append(avoid, ex)
		} else {
			apply = append(apply, ex)
		}
	}
	sort.SliceStable(avoid, func(i, j int) bool { return rankKey(avoid[i]) > rankKey(avoid[j]) })
	sort.SliceStable(apply, func(i, j int) bool { return rankKey(apply[i]) > rankKey(apply[j]) })

	avoid = trimSlice(avoid, avoidLimit)
	apply = trimSlice(apply, applyLimit)

	if len(avoid) == 0 && len(apply) == 0 {
		return "", nil
	}

	return renderReflexBlock(avoid, apply, blockMaxChars), nil
}

// renderReflexBlock renders the two buckets under a character cap. Both
// buckets are ranked best-first, so when the block is over budget the last
// lesson of the longer bucket is dropped, repeatedly, until it fits. Pure;
// tested directly.
func renderReflexBlock(avoid, apply []promptExample, maxChars int) string {
	render := func() string {
		var b strings.Builder
		b.WriteString("<gym_reflex>\n")
		b.WriteString("Experience-backed lessons mined from your own past sessions. AVOID lessons describe what failed before - skip those moves. APPLY lessons describe what worked - reach for them when the situation matches.\n")
		if len(avoid) > 0 {
			b.WriteString("\nAVOID:\n")
			for i, ex := range avoid {
				renderLesson(&b, i+1, ex)
			}
		}
		if len(apply) > 0 {
			b.WriteString("\nAPPLY:\n")
			for i, ex := range apply {
				renderLesson(&b, i+1, ex)
			}
		}
		b.WriteString("</gym_reflex>")
		return b.String()
	}
	out := render()
	for maxChars > 0 && len(out) > maxChars && len(avoid)+len(apply) > 1 {
		if len(avoid) >= len(apply) {
			avoid = avoid[:len(avoid)-1]
		} else {
			apply = apply[:len(apply)-1]
		}
		out = render()
	}
	return out
}

func (p *Provider) fetchCandidates(ctx context.Context, query string) ([]promptExample, error) {
	// Path 1: vector cosine, when we can embed the query.
	var vec []float32
	if p.embedder != nil && strings.TrimSpace(query) != "" {
		if v, err := p.embedder.Embed(ctx, query); err == nil {
			vec = v
		}
	}
	if len(vec) > 0 {
		rows, err := p.pool.Query(ctx, `
			SELECT source_kind, source_id, task_kind, input_text, output_text,
			       label, score::float8,
			       1 - (embedding <=> $1) AS cosine_sim
			  FROM mem_training_examples
			 WHERE embedding IS NOT NULL
			 ORDER BY embedding <=> $1 ASC
			 LIMIT $2
		`, pgvector.NewVector(vec), candidateLimit)
		if err == nil {
			out, scanErr := scanExamples(rows, true)
			rows.Close()
			if scanErr == nil && len(out) > 0 {
				return out, nil
			}
		}
	}
	// Path 2 (always available): recency + score.
	rows, err := p.pool.Query(ctx, `
		SELECT source_kind, source_id, task_kind, input_text, output_text,
		       label, score::float8, 0::float8 AS cosine_sim
		  FROM mem_training_examples
		 ORDER BY created_at DESC
		 LIMIT $1
	`, candidateLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExamples(rows, false)
}

func scanExamples(rows interface {
	Next() bool
	Scan(...any) error
}, withCosine bool) ([]promptExample, error) {
	var out []promptExample
	for rows.Next() {
		var ex promptExample
		if err := rows.Scan(&ex.SourceKind, &ex.SourceID, &ex.TaskKind, &ex.Input, &ex.Output, &ex.Label, &ex.Score, &ex.Cosine); err != nil {
			return nil, err
		}
		if !withCosine {
			ex.Cosine = 0
		}
		out = append(out, ex)
	}
	return out, nil
}

// rankKey scores an example for sort. Negative lessons get an
// effective-score bump because "don't do X" is more behavior-changing per
// token than "consider doing Y". Cosine dominates lexical when present.
func rankKey(ex promptExample) float64 {
	weight := 1.0
	if isNegative(ex.Label) {
		// Negative lessons - we WANT to surface them even at slightly
		// lower confidence than positive ones. 1.25x boost matches the
		// "surprise lesson worth more than satisfied prediction"
		// intuition without drowning out genuine high-quality positives.
		weight = 1.25
	}
	score := ex.Score * weight
	if ex.Cosine >= cosineMin {
		score += ex.Cosine * 1.5 // cosine is the primary semantic signal
	} else if ex.Cosine > 0 {
		score += 0.1 * ex.Cosine // weak hits still count, just less
	}
	score += float64(ex.LexMatch) * 0.15 // lexical breaks ties
	return score
}

func renderLesson(b *strings.Builder, idx int, ex promptExample) {
	verb := "do"
	switch {
	case isNegative(ex.Label):
		verb = "avoid"
	case ex.Label == "accepted":
		verb = "apply"
	}
	task := ex.TaskKind
	if task == "" {
		task = "general"
	}
	fmt.Fprintf(b, "  [%d] %s/%s score=%.2f%s source=%s#%s\n",
		idx, task, verb, ex.Score, cosineTag(ex.Cosine), ex.SourceKind, trimID(ex.SourceID))
	if s := trim(ex.Input, 180); s != "" {
		fmt.Fprintf(b, "      observed: %s\n", s)
	}
	if s := trim(ex.Output, 240); s != "" {
		key := "lesson"
		if isNegative(ex.Label) {
			key = "what went wrong"
		}
		fmt.Fprintf(b, "      %s: %s\n", key, s)
	}
}

func cosineTag(c float64) string {
	if c <= 0 {
		return ""
	}
	return fmt.Sprintf(" sim=%.2f", c)
}

func isNegative(label string) bool {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "rejected", "corrected", "failed":
		return true
	}
	return false
}

func trimSlice(xs []promptExample, n int) []promptExample {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

type promptExample struct {
	SourceKind string
	SourceID   string
	TaskKind   string
	Input      string
	Output     string
	Label      string
	Score      float64
	Cosine     float64
	LexMatch   int
}

func tokenSet(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		// 3+ chars catches "git", "api", "css" without ballooning into
		// connectives like "is"/"to".
		if len(f) < 3 {
			continue
		}
		out[f] = struct{}{}
	}
	return out
}

func lexicalOverlap(tokens map[string]struct{}, text string) int {
	if len(tokens) == 0 {
		return 0
	}
	hay := strings.ToLower(text)
	n := 0
	for t := range tokens {
		if strings.Contains(hay, t) {
			n++
		}
	}
	return n
}

func trim(s string, n int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func trimID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}
