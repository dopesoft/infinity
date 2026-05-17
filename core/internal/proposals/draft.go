// Package proposals - draft.go: one active draft per parent_skill with
// compound merge + conflict detection.
//
// The bug this closes: today the extractor / optimizer / agent
// skill_propose tool all INSERT plain rows into mem_skill_proposals
// without any "is there already a candidate for this skill?" check.
// 7-8 candidates pile up for the same parent_skill; approving them
// one by one interleaves the body because Decide() doesn't know
// about the prior pending edits.
//
// The fix: one open candidate per parent_skill enforced by a partial
// unique index (migration 039). New proposals MERGE into the existing
// draft: a short Haiku call unifies the existing body with the new
// proposed change, appends to changes_log, and emits a <conflicts>
// block when the new change CONTRADICTS the existing draft. The
// boss sees ONE card with the full pending bundle.
//
// Brand-new skill proposals (parent_skill IS NULL) keep the old path -
// the constraint doesn't fire on NULL parent_skill.

package proposals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CandidateDraft is the input to UpsertCandidate. Fields mirror the
// columns of mem_skill_proposals the caller controls. The Sample /
// reasoning entry is what lands in changes_log per merge.
type CandidateDraft struct {
	Name        string // skill name (canonical phrasing)
	ParentSkill string // existing skill being refined; "" for brand-new
	Description string
	Reasoning   string // refreshed-then-appended to changes_log
	SkillMD     string // proposed body
	RiskLevel   string // low | medium | high | critical
	Source      string // "extractor" | "optimizer" | "agent" | etc.
}

// UpsertResult captures what UpsertCandidate did. IsNew=true means a
// fresh row was inserted; IsNew=false means an existing candidate was
// merged into. Revision is the post-call revision count (1 for fresh).
// Conflicts is the LLM-flagged contradictions parsed out of the merge
// response (empty when the merge was clean).
type UpsertResult struct {
	ID        string   `json:"id"`
	IsNew     bool     `json:"is_new"`
	Revision  int      `json:"revision"`
	Conflicts []string `json:"conflicts,omitempty"`
}

// Drafter is the minimal LLM interface UpsertCandidate needs. *llm.Anthropic
// satisfies it; the narrow surface keeps this file from pulling the full
// provider interface.
type Drafter interface {
	Draft(ctx context.Context, model, system, userPrompt string, maxTokens int64) (string, error)
}

// DraftMemoryWriter is the optional bridge into the A-MEM graph (Opt 7).
// When set via SetDraftMemoryWriter, every fresh / merged skill draft
// gets a mem_memories twin (tier='working') with embedding + associative
// edges to its nearest neighbours. The agent can RRF-retrieve "we have
// a pending refinement to gmail_triage that adds retries" mid-turn,
// instead of forgetting drafts exist between sessions.
type DraftMemoryWriter interface {
	WriteSkillDraftMemory(ctx context.Context, proposalID, parentSkill, name, body string) (memoryID string, err error)
}

// memoryWriter is the package-level draft-memory bridge. Nil-safe.
var memoryWriter DraftMemoryWriter

// SetDraftMemoryWriter wires the A-MEM bridge. Called from serve.go boot.
func SetDraftMemoryWriter(w DraftMemoryWriter) { memoryWriter = w }

// MergeEvaluator is the optional bridge into GEPA Pareto evaluation
// (Opt 3). When the merge detects len(conflicts) > 0, we hand the
// (existing, proposed, merged) trio to GEPA for an empirical pick
// instead of trusting Haiku's choice. nil-safe.
type MergeEvaluator interface {
	EvaluateSkillMerge(ctx context.Context, parentSkill string, candidates []SkillCandidate) (best string, err error)
}

// SkillCandidate is one body the merge evaluator considers.
type SkillCandidate struct {
	Label string // "existing" | "proposed" | "merged"
	Body  string
}

var mergeEvaluator MergeEvaluator

// SetMergeEvaluator wires the GEPA bridge. Called from serve.go boot.
func SetMergeEvaluator(e MergeEvaluator) { mergeEvaluator = e }

// mergeModel defaults to Haiku - cheap, deterministic, fast. Override
// with INFINITY_SKILL_MERGE_MODEL when iterating.
const mergeModel = "claude-haiku-4-5-20251001"

// UpsertCandidate writes (or merges into) a skill proposal. Behavior:
//
//   - ParentSkill == "" -> always INSERT a fresh row (brand-new skill).
//     Same legacy path the extractor used before this helper existed.
//   - ParentSkill != "" with no open candidate -> INSERT, revision=1,
//     changes_log seeded with one entry.
//   - ParentSkill != "" with an open candidate -> Haiku merges the
//     existing body with the new proposed change, appends a
//     changes_log entry, increments revision, captures any
//     <conflicts> the merge flagged.
//
// drafter can be nil; in that case the merge degrades to "keep the new
// body, append reasoning to changes_log, no conflict detection." The
// fallback still avoids dupe rows.
func UpsertCandidate(
	ctx context.Context,
	pool *pgxpool.Pool,
	drafter Drafter,
	logger *slog.Logger,
	d CandidateDraft,
) (*UpsertResult, error) {
	if pool == nil {
		return nil, errors.New("voyager: no pool")
	}
	d.Name = strings.TrimSpace(d.Name)
	d.ParentSkill = strings.TrimSpace(d.ParentSkill)
	d.SkillMD = strings.TrimSpace(d.SkillMD)
	if d.Name == "" || d.SkillMD == "" {
		return nil, errors.New("voyager: name + skill_md required")
	}
	if d.RiskLevel == "" {
		d.RiskLevel = "low"
	}
	if d.Source == "" {
		d.Source = "agent"
	}

	// Brand-new skill: keep the legacy INSERT path. No partial unique
	// index fires when parent_skill is NULL, so duplicate brand-new
	// proposals for the same name are allowed (this is by design -
	// they represent independent ideas the boss can compare).
	if d.ParentSkill == "" {
		return insertFreshProposal(ctx, pool, d, nil)
	}

	// Existing-skill refinement: try to merge into the open draft.
	existing, found, err := loadOpenCandidate(ctx, pool, d.ParentSkill)
	if err != nil {
		return nil, fmt.Errorf("load open candidate: %w", err)
	}
	if !found {
		return insertFreshProposal(ctx, pool, d, nil)
	}

	// Merge body + collect conflicts.
	mergedBody, conflicts := mergeBodies(ctx, drafter, logger, existing.SkillMD, d.SkillMD)

	// Opt 3: if the merge flagged conflicts, hand (existing, proposed,
	// merged) to GEPA for an empirical pick before we accept Haiku's
	// blended body. The evaluator returns whichever candidate wins on
	// the synthetic tests; we substitute it for mergedBody.
	if len(conflicts) > 0 && mergeEvaluator != nil {
		best, evalErr := mergeEvaluator.EvaluateSkillMerge(ctx, d.ParentSkill, []SkillCandidate{
			{Label: "existing", Body: existing.SkillMD},
			{Label: "proposed", Body: d.SkillMD},
			{Label: "merged", Body: mergedBody},
		})
		if evalErr == nil && best != "" {
			mergedBody = best
			if logger != nil {
				logger.Info("skill merge evaluator picked best body",
					"parent_skill", d.ParentSkill,
					"conflicts_before", len(conflicts))
			}
		} else if evalErr != nil && logger != nil {
			logger.Warn("skill merge evaluator failed, keeping Haiku merge",
				"parent_skill", d.ParentSkill, "err", evalErr)
		}
	}

	changeEntry := map[string]any{
		"at":          time.Now().UTC().Format(time.RFC3339),
		"reasoning":   truncate(d.Reasoning, 500),
		"source":      d.Source,
		"description": truncate(d.Description, 200),
	}
	entryJSON, _ := json.Marshal(changeEntry)

	var conflictsJSON []byte
	if len(conflicts) > 0 {
		conflictsJSON, _ = json.Marshal(conflicts)
	}

	var (
		id       string
		revision int
	)
	err = pool.QueryRow(ctx, `
		UPDATE mem_skill_proposals
		   SET skill_md       = $2,
		       description    = COALESCE(NULLIF($3, ''), description),
		       reasoning      = $4,
		       risk_level     = $5,
		       changes_log    = changes_log || $6::jsonb,
		       revision       = revision + 1,
		       conflicts      = COALESCE($7::jsonb, conflicts),
		       last_merged_at = NOW()
		 WHERE id = $1
		 RETURNING id::text, revision
	`, existing.ID, mergedBody, truncate(d.Description, 200),
		truncate(d.Reasoning, 500), strings.ToLower(d.RiskLevel),
		string(entryJSON), nullableJSON(conflictsJSON)).Scan(&id, &revision)
	if err != nil {
		return nil, fmt.Errorf("merge update: %w", err)
	}
	if logger != nil {
		logger.Info("skill proposal merged",
			"parent_skill", d.ParentSkill,
			"id", id,
			"revision", revision,
			"conflicts", len(conflicts),
			"source", d.Source)
	}
	// Opt 7: refresh the draft's mem_memories twin so RRF retrieval
	// surfaces the LATEST merged body. Auto-link writes associative
	// edges to nearest neighbours, threading the draft into the
	// memory graph mid-revision.
	writeDraftMemory(ctx, id, d.ParentSkill, d.Name, mergedBody)
	return &UpsertResult{
		ID:        id,
		IsNew:     false,
		Revision:  revision,
		Conflicts: conflicts,
	}, nil
}

// writeDraftMemory dispatches to the package-level memoryWriter (set by
// serve.go). Best-effort - nil writer means the A-MEM linkage is just
// disabled for this deploy, not an error.
func writeDraftMemory(ctx context.Context, proposalID, parentSkill, name, body string) {
	if memoryWriter == nil {
		return
	}
	_, _ = memoryWriter.WriteSkillDraftMemory(ctx, proposalID, parentSkill, name, body)
}

// GetOpenCandidate returns the current open candidate for a parent_skill,
// if any. Read-only - the skill_proposal_get tool wraps this so the
// agent can see what's pending before producing a new merge body.
func GetOpenCandidate(
	ctx context.Context,
	pool *pgxpool.Pool,
	parentSkill string,
) (*existingCandidate, bool, error) {
	return loadOpenCandidate(ctx, pool, strings.TrimSpace(parentSkill))
}

type existingCandidate struct {
	ID          string
	Name        string
	SkillMD     string
	Description string
	Reasoning   string
	RiskLevel   string
	Revision    int
	ChangesLog  []map[string]any
}

func loadOpenCandidate(ctx context.Context, pool *pgxpool.Pool, parentSkill string) (*existingCandidate, bool, error) {
	if parentSkill == "" {
		return nil, false, nil
	}
	var (
		c          existingCandidate
		changesRaw []byte
	)
	err := pool.QueryRow(ctx, `
		SELECT id::text, name, COALESCE(skill_md,''), COALESCE(description,''),
		       COALESCE(reasoning,''), COALESCE(risk_level,'low'),
		       revision, COALESCE(changes_log, '[]'::jsonb)
		  FROM mem_skill_proposals
		 WHERE parent_skill = $1 AND status = 'candidate' AND proposal_kind = 'draft'
		 LIMIT 1
	`, parentSkill).Scan(&c.ID, &c.Name, &c.SkillMD, &c.Description,
		&c.Reasoning, &c.RiskLevel, &c.Revision, &changesRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if len(changesRaw) > 0 {
		_ = json.Unmarshal(changesRaw, &c.ChangesLog)
	}
	return &c, true, nil
}

func insertFreshProposal(ctx context.Context, pool *pgxpool.Pool, d CandidateDraft, conflicts []string) (*UpsertResult, error) {
	entry := map[string]any{
		"at":          time.Now().UTC().Format(time.RFC3339),
		"reasoning":   truncate(d.Reasoning, 500),
		"source":      d.Source,
		"description": truncate(d.Description, 200),
	}
	entryJSON, _ := json.Marshal(entry)
	changesLog := "[" + string(entryJSON) + "]"

	var parentSkill any
	proposalKind := "standalone"
	if d.ParentSkill != "" {
		parentSkill = d.ParentSkill
		proposalKind = "draft"
	} else {
		parentSkill = nil
	}

	var conflictsJSON []byte
	if len(conflicts) > 0 {
		conflictsJSON, _ = json.Marshal(conflicts)
	}

	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO mem_skill_proposals
		  (name, description, reasoning, skill_md, risk_level, status,
		   parent_skill, changes_log, revision, conflicts, proposal_kind)
		VALUES ($1, $2, $3, $4, $5, 'candidate', $6, $7::jsonb, 1, COALESCE($8::jsonb, '[]'::jsonb), $9)
		RETURNING id::text
	`, d.Name, truncate(d.Description, 200), truncate(d.Reasoning, 500),
		d.SkillMD, strings.ToLower(d.RiskLevel), parentSkill,
		changesLog, nullableJSON(conflictsJSON), proposalKind).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("insert fresh proposal: %w", err)
	}
	// Opt 7: write the draft's mem_memories twin + auto-link.
	writeDraftMemory(ctx, id, d.ParentSkill, d.Name, d.SkillMD)
	return &UpsertResult{
		ID:        id,
		IsNew:     true,
		Revision:  1,
		Conflicts: conflicts,
	}, nil
}

// mergeBodies asks Haiku to unify the existing body with the new
// proposed change. Haiku is instructed to emit conflicts in a
// <conflicts> block at the END of the response; we parse those into a
// []string. Returns (mergedBody, conflicts).
//
// When drafter is nil (no Anthropic client wired), falls back to:
//   - body = newBody (the latest proposed change wins)
//   - conflicts = empty
//
// This degrades gracefully without losing the changes_log accumulation.
func mergeBodies(ctx context.Context, drafter Drafter, logger *slog.Logger, existing, proposed string) (string, []string) {
	existing = strings.TrimSpace(existing)
	proposed = strings.TrimSpace(proposed)
	if existing == "" {
		return proposed, nil
	}
	if proposed == "" || existing == proposed {
		return existing, nil
	}
	if drafter == nil {
		return proposed, nil
	}

	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(`There is an existing pending skill draft and a new proposed change to that draft.
Your job: produce a SINGLE unified body that incorporates BOTH the existing pending edits AND the new proposed change.

If the new change CONTRADICTS the existing pending edits (same setting, different values; mutually exclusive behaviors; one removes what the other adds), list each contradiction on its own line inside a <conflicts>...</conflicts> block at the END of your response. Otherwise omit the block entirely.

Return the unified body as raw markdown - no fences, no commentary.

<existing_draft>
%s
</existing_draft>

<new_proposed_change>
%s
</new_proposed_change>`, existing, proposed)

	raw, err := drafter.Draft(cctx, mergeModel, mergeSystem, prompt, 4000)
	if err != nil {
		if logger != nil {
			logger.Warn("skill merge draft failed; falling back to proposed body",
				"err", err)
		}
		return proposed, nil
	}
	body, conflicts := parseMergeResponse(raw)
	if body == "" {
		body = proposed
	}
	return body, conflicts
}

const mergeSystem = `You unify two markdown skill drafts. Produce one body that incorporates both. Flag contradictions explicitly. Never invent steps that neither draft contains.`

func parseMergeResponse(raw string) (string, []string) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```markdown")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	openTag := "<conflicts>"
	closeTag := "</conflicts>"
	openIdx := strings.LastIndex(s, openTag)
	if openIdx < 0 {
		return s, nil
	}
	closeIdx := strings.LastIndex(s, closeTag)
	if closeIdx <= openIdx {
		return s, nil
	}
	body := strings.TrimSpace(s[:openIdx])
	block := s[openIdx+len(openTag) : closeIdx]
	var conflicts []string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimPrefix(line, "*")
		line = strings.TrimSpace(line)
		if line != "" {
			conflicts = append(conflicts, line)
		}
	}
	return body, conflicts
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
