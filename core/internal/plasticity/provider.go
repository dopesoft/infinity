package plasticity

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Provider makes Gym learning immediately useful to the agent loop. It does
// not train weights; it injects the best extracted examples as reflex lessons
// in the system prompt. This is the first plasticity step: verified experience
// changes future behavior before adapter training exists.
type Provider struct {
	pool *pgxpool.Pool
}

func NewProvider(pool *pgxpool.Pool) *Provider {
	return &Provider{pool: pool}
}

func (p *Provider) BuildSystemPrefix(ctx context.Context, _, query string) (string, error) {
	if p == nil || p.pool == nil {
		return "", nil
	}
	if ok, err := NewStore(p.pool).tableExists(ctx, "mem_training_examples"); err != nil || !ok {
		return "", err
	}
	rows, err := p.pool.Query(ctx, `
		SELECT source_kind, source_id, task_kind, input_text, output_text,
		       label, score::float8
		  FROM mem_training_examples
		 WHERE score >= 0.5
		 ORDER BY score DESC, created_at DESC
		 LIMIT 40
	`)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var examples []promptExample
	qTokens := tokenSet(query)
	for rows.Next() {
		var ex promptExample
		if err := rows.Scan(&ex.SourceKind, &ex.SourceID, &ex.TaskKind, &ex.Input, &ex.Output, &ex.Label, &ex.Score); err != nil {
			return "", err
		}
		ex.Match = lexicalOverlap(qTokens, ex.TaskKind+" "+ex.Input+" "+ex.Output)
		examples = append(examples, ex)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(examples) == 0 {
		return "", nil
	}
	sort.SliceStable(examples, func(i, j int) bool {
		if examples[i].Match == examples[j].Match {
			return examples[i].Score > examples[j].Score
		}
		return examples[i].Match > examples[j].Match
	})
	limit := 5
	if examples[0].Match == 0 {
		limit = 3
	}
	if len(examples) < limit {
		limit = len(examples)
	}

	var b strings.Builder
	b.WriteString("Gym reflex lessons (experience-backed; apply when relevant):\n")
	for i, ex := range examples[:limit] {
		fmt.Fprintf(&b, "  [%d] %s/%s score=%.2f source=%s#%s\n",
			i+1, ex.TaskKind, ex.Label, ex.Score, ex.SourceKind, trimID(ex.SourceID))
		if s := trim(ex.Input, 160); s != "" {
			fmt.Fprintf(&b, "      observed: %s\n", s)
		}
		if s := trim(ex.Output, 220); s != "" {
			fmt.Fprintf(&b, "      lesson: %s\n", s)
		}
	}
	return strings.TrimSpace(b.String()), nil
}

type promptExample struct {
	SourceKind string
	SourceID   string
	TaskKind   string
	Input      string
	Output     string
	Label      string
	Score      float64
	Match      int
}

func tokenSet(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(f) < 4 {
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
