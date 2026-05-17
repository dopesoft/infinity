package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/embed"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// ProceduralStore is the substrate for the procedural memory tier. CoALA
// (Sumers et al.) names this tier explicitly; AutoSkill maps onto it as the
// "habits" layer. Every promoted skill writes a procedural row so the agent
// can retrieve it via the same RRF/search machinery as semantic memories.
//
// We use the existing mem_memories table with tier='procedural' rather than a
// parallel skills_index table. That keeps strength/decay/forget/embeddings
// uniform across tiers and means the same provenance + auditor paths work.
type ProceduralStore struct {
	pool     *pgxpool.Pool
	embedder embed.Embedder
}

func NewProceduralStore(pool *pgxpool.Pool, embedder embed.Embedder) *ProceduralStore {
	if embedder == nil {
		embedder = embed.NewStub()
	}
	return &ProceduralStore{pool: pool, embedder: embedder}
}

// ProceduralEntry is the wire shape returned from TopK.
type ProceduralEntry struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Content    string  `json:"content"`
	SkillName  string  `json:"skill_name"`
	Strength   float32 `json:"strength"`
	Importance int     `json:"importance"`
}

// UpsertFromSkill records a promoted skill as a procedural memory. Idempotent
// on the skill name - re-promoting the same skill updates the existing row
// rather than appending. The content is the skill's "When to use" + first
// few lines of "Steps" so retrieval at injection time gets a usable summary
// without exploding the system-prompt budget.
func (p *ProceduralStore) UpsertFromSkill(ctx context.Context, skillName, description, skillMD string, importance int) error {
	if p == nil || p.pool == nil {
		return errors.New("procedural store: no pool")
	}
	if strings.TrimSpace(skillName) == "" {
		return errors.New("procedural store: empty skill name")
	}
	title := "skill:" + strings.TrimSpace(skillName)
	content := buildProceduralContent(description, skillMD)
	emb, err := p.embedder.Embed(ctx, title+"\n"+content)
	if err != nil {
		emb = nil
	}
	var embArg any
	if emb != nil {
		embArg = pgvector.NewVector(emb)
	}
	if importance < 1 || importance > 10 {
		importance = 6 // procedural skills are above average by default
	}

	// Look up an existing procedural row for this skill. If found, update
	// content + strength reset + last_accessed. Otherwise insert.
	var existingID string
	err = p.pool.QueryRow(ctx, `
		SELECT id::text FROM mem_memories
		 WHERE tier = 'procedural' AND status = 'active' AND title = $1
		 LIMIT 1
	`, title).Scan(&existingID)
	if err == nil && existingID != "" {
		_, err = p.pool.Exec(ctx, `
			UPDATE mem_memories
			   SET content = $2,
			       importance = $3,
			       embedding = $4,
			       fts_doc = to_tsvector('english', COALESCE($1, '') || ' ' || COALESCE($2, '')),
			       strength = 1.0,
			       updated_at = NOW(),
			       last_accessed_at = NOW()
			 WHERE id = $5::uuid
		`, title, content, importance, embArg, existingID)
		return err
	}

	id := uuid.NewString()
	_, err = p.pool.Exec(ctx, `
		INSERT INTO mem_memories
		  (id, title, content, tier, version, status, strength, importance, embedding, fts_doc,
		   created_at, updated_at, last_accessed_at)
		VALUES
		  ($1::uuid, $2, $3, 'procedural', 1, 'active', 1.0, $4, $5,
		   to_tsvector('english', COALESCE($2, '') || ' ' || COALESCE($3, '')),
		   NOW(), NOW(), NOW())
	`, id, title, content, importance, embArg)
	return err
}

// MarkSkillArchived flips an active procedural memory for a skill to
// archived. Called by the skills.Decide path when a skill is rejected or
// retired so the agent stops pulling its procedural row.
func (p *ProceduralStore) MarkSkillArchived(ctx context.Context, skillName string) error {
	if p == nil || p.pool == nil {
		return nil
	}
	title := "skill:" + strings.TrimSpace(skillName)
	_, err := p.pool.Exec(ctx, `
		UPDATE mem_memories
		   SET status = 'archived', updated_at = NOW()
		 WHERE tier = 'procedural' AND title = $1
	`, title)
	return err
}

// TopK returns the K most relevant procedural memories. With query="" it
// returns the top-K by strength × importance (used as the always-injected
// slice at agent boot). With a query it embeds and ranks by cosine - used
// by the system-prompt builder when a user message arrives.
func (p *ProceduralStore) TopK(ctx context.Context, query string, k int) ([]ProceduralEntry, error) {
	if p == nil || p.pool == nil {
		return nil, nil
	}
	if k <= 0 || k > 20 {
		k = 5
	}
	q := strings.TrimSpace(query)
	if q == "" {
		rows, err := p.pool.Query(ctx, `
			SELECT id::text, COALESCE(title,''), COALESCE(content,''),
			       strength, importance
			  FROM mem_memories
			 WHERE tier = 'procedural' AND status = 'active'
			 ORDER BY strength * importance DESC, last_accessed_at DESC
			 LIMIT $1
		`, k)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanProcedural(rows)
	}
	emb, err := p.embedder.Embed(ctx, q)
	if err != nil || emb == nil {
		// Fallback to default top-K when embedding fails.
		return p.TopK(ctx, "", k)
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id::text, COALESCE(title,''), COALESCE(content,''),
		       strength, importance
		  FROM mem_memories
		 WHERE tier = 'procedural' AND status = 'active'
		   AND embedding IS NOT NULL
		 ORDER BY embedding <=> $1 ASC
		 LIMIT $2
	`, pgvector.NewVector(emb), k)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProcedural(rows)
}

func scanProcedural(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]ProceduralEntry, error) {
	var out []ProceduralEntry
	for rows.Next() {
		var e ProceduralEntry
		if err := rows.Scan(&e.ID, &e.Title, &e.Content, &e.Strength, &e.Importance); err != nil {
			return nil, err
		}
		e.SkillName = strings.TrimPrefix(e.Title, "skill:")
		out = append(out, e)
	}
	return out, rows.Err()
}

// buildProceduralContent extracts the load-bearing sections of a SKILL.md
// (description + "When to use" + first few step lines) into a compact body
// that fits in a system-prompt injection slot. The full SKILL.md still lives
// in mem_skill_versions; this is the retrieval-friendly summary.
func buildProceduralContent(description, skillMD string) string {
	desc := strings.TrimSpace(description)
	if skillMD == "" {
		return desc
	}
	var b strings.Builder
	if desc != "" {
		b.WriteString(desc)
		b.WriteString("\n\n")
	}
	// Pull the "## When to use" section verbatim if present, otherwise the
	// first 6 non-frontmatter lines.
	body := skillMD
	if idx := strings.Index(body, "\n---\n"); idx >= 0 {
		// strip yaml frontmatter
		body = body[idx+5:]
	}
	if section := extractSection(body, "When to use", 8); section != "" {
		b.WriteString("## When to use\n")
		b.WriteString(section)
	} else {
		lines := strings.Split(strings.TrimSpace(body), "\n")
		if len(lines) > 8 {
			lines = lines[:8]
		}
		b.WriteString(strings.Join(lines, "\n"))
	}
	out := strings.TrimSpace(b.String())
	if len(out) > 1200 {
		out = out[:1200] + "…"
	}
	return out
}

func extractSection(body, heading string, maxLines int) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "##") {
			continue
		}
		if !strings.Contains(strings.ToLower(l), strings.ToLower(heading)) {
			continue
		}
		end := i + 1
		for end < len(lines) && end-i-1 < maxLines {
			t := strings.TrimSpace(lines[end])
			if strings.HasPrefix(t, "## ") {
				break
			}
			end++
		}
		return strings.TrimSpace(strings.Join(lines[i+1:end], "\n"))
	}
	return ""
}

// FormatForPrompt renders a TopK slice as a short system-prompt block. The
// agent loop calls this when building the context - keeps the formatting
// contract in one place.
func FormatForPrompt(entries []ProceduralEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Your procedural skills (top matches)\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "- **%s** - %s\n", e.SkillName, firstLine(e.Content))
	}
	return b.String()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
