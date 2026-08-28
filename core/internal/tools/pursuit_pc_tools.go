// Psycho-Cybernetics cockpit tools - let Jarvis read and write the coached
// pursuit from chat.
//
//	pursuit_pc_state → read the cockpit (identity, objective, day, proofs, …)
//	pursuit_pc_write → every mutation, behind one `action` discriminator
//
// Two tools rather than eight. The write side is deliberately ONE tool with an
// action enum because it maps onto the single pc.Store.Apply chokepoint the
// HTTP cockpit also uses (Rule #1c) - a decision made in chat and the same
// decision made in the cockpit produce byte-identical state, including the
// derived side effects (a morning pledge becomes a tracked proof, an evening
// correction lands in the pattern history).

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/pursuits/pc"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterPursuitPCTools wires the coached-pursuit read/write pair. No-op if
// the pool is nil so chat-only deployments don't break.
func RegisterPursuitPCTools(r *Registry, pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	r.Register(&pursuitPCState{pool: pool})
	r.Register(&pursuitPCWrite{pool: pool})
}

// ── pursuit_pc_state ───────────────────────────────────────────────────────

type pursuitPCState struct{ pool *pgxpool.Pool }

func (t *pursuitPCState) Name() string { return "pursuit_pc_state" }

// ReadOnly is declared explicitly because the `_state` suffix is not one the
// name heuristic recognises as a read, and system_map would otherwise bucket
// this pure read under the mutating tools.
func (t *pursuitPCState) ReadOnly() bool { return true }
func (t *pursuitPCState) Description() string {
	return "Read the current state of a Psycho-Cybernetics pursuit: the boss's operating identity, abundance objective, limiting pattern, day/cycle position, today's proof actions, recent evidence and resistance, banked success memories, patterns, corrections, and the coach's current phase. Call this before coaching him so you work from what he actually wrote rather than asking him to repeat it. Use pursuit_list to find the pursuit id."
}
func (t *pursuitPCState) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pursuit_id": map[string]any{"type": "string", "description": "The pursuit's UUID."},
		},
		"required": []string{"pursuit_id"},
	}
}
func (t *pursuitPCState) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "pursuit_id")
	if id == "" {
		return "", errors.New("pursuit_id required")
	}
	cockpit, err := pc.NewStore(t.pool).Cockpit(ctx, id, time.Now())
	if err != nil {
		return "", err
	}
	return pc.FormatChatContext(cockpit), nil
}

// ── pursuit_pc_write ───────────────────────────────────────────────────────

type pursuitPCWrite struct{ pool *pgxpool.Pool }

func (t *pursuitPCWrite) Name() string { return "pursuit_pc_write" }
func (t *pursuitPCWrite) Description() string {
	return "Write a decision from this conversation back into a Psycho-Cybernetics pursuit so the cockpit and the chat never disagree. " +
		"action='session' logs a coaching session (kind=morning/midday/evening/recovery/onboarding/review/adjustment) with the boss's answers; a morning session whose answers include proof_pledge automatically creates the tracked proof, and an evening session whose answers include correction automatically files it in the pattern history. " +
		"action='identity' edits the operating identity, abundance objective, limiting pattern, or pressure test. " +
		"action='proof' pledges a proof action; action='proof/taken' marks one taken. " +
		"action='evidence' captures evidence the identity worked (kind='evidence') or resistance where the old pattern showed up (kind='resistance'). " +
		"action='memory' banks a success memory. action='pattern' logs a pattern or correction. action='review' closes the current 21-day cycle and opens the next. " +
		"Only write what the boss actually decided or said. Never invent evidence, memories, or proof actions on his behalf."
}
func (t *pursuitPCWrite) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pursuit_id": map[string]any{"type": "string", "description": "The pursuit's UUID."},
			"action": map[string]any{
				"type": "string",
				"enum": pc.WriteActions(),
			},
			"kind": map[string]any{
				"type":        "string",
				"description": "For action='session': morning|midday|evening|recovery|onboarding|review|adjustment. For action='evidence': evidence|resistance. For action='pattern': limiting|operating|correction.",
			},
			"answers": map[string]any{
				"type":        "object",
				"description": "For action='session': the boss's own answers, e.g. {rehearsal, proof_pledge} for morning or {fact, interpretation, lesson, correction} for evening.",
			},
			"coach_note":     map[string]any{"type": "string", "description": "Your own short note about what you observed and adapted. Never a judgement."},
			"identity":       map[string]any{"type": "string", "description": "For action='identity': the operating identity, in the boss's words."},
			"objective":      map[string]any{"type": "string", "description": "For action='identity': the abundance objective."},
			"pattern":        map[string]any{"type": "string", "description": "For action='identity': the limiting pattern."},
			"label":          map[string]any{"type": "string", "description": "For action='proof': the proof action."},
			"proof_id":       map[string]any{"type": "string", "description": "For action='proof/taken': the proof's UUID (from pursuit_pc_state)."},
			"taken":          map[string]any{"type": "boolean", "description": "For action='proof/taken'. Defaults true."},
			"note":           map[string]any{"type": "string", "description": "For action='proof/taken': how it went."},
			"body":           map[string]any{"type": "string", "description": "For action='evidence'/'memory'/'pattern': the text."},
			"title":          map[string]any{"type": "string", "description": "For action='memory': the memory's title."},
			"wins":           map[string]any{"type": "string", "description": "For action='review'."},
			"misses":         map[string]any{"type": "string", "description": "For action='review'."},
			"next_identity":  map[string]any{"type": "string", "description": "For action='review'. Blank keeps the current identity."},
			"next_objective": map[string]any{"type": "string", "description": "For action='review'. Blank keeps the current objective."},
			"next_pattern":   map[string]any{"type": "string", "description": "For action='review'. Blank keeps the current pattern."},
		},
		"required": []string{"pursuit_id", "action"},
	}
}

func (t *pursuitPCWrite) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "pursuit_id")
	if id == "" {
		return "", errors.New("pursuit_id required")
	}
	action := strings.TrimSpace(strString(in, "action"))
	if action == "" {
		return "", errors.New("action required")
	}
	// Everything this tool writes is the boss's own first-person reflective
	// writing: his identity, his evidence, his proof actions, his review. An
	// AUTONOMOUS turn (cron, heartbeat, sub-agent) has nobody to have said any
	// of it, so anything it wrote here would be invented on his behalf. The
	// skill body says "only write what he actually decided", but prose is
	// droppable (Rule #1b), so the refusal is enforced here instead. Live chat
	// and the cockpit are unaffected.
	if IsAutonomous(ctx) {
		return "", errors.New("refusing to write a Psycho-Cybernetics entry on an unattended turn: every field here is the boss's own writing about his own experiment, and nothing was said on this turn to write down. Raise it with him in live chat instead")
	}

	req := pc.WriteRequest{
		Identity:      strString(in, "identity"),
		Objective:     strString(in, "objective"),
		Pattern:       strString(in, "pattern"),
		Kind:          strString(in, "kind"),
		CoachNote:     strString(in, "coach_note"),
		ProofID:       strString(in, "proof_id"),
		Label:         strString(in, "label"),
		Note:          strString(in, "note"),
		Body:          strString(in, "body"),
		Title:         strString(in, "title"),
		Wins:          strString(in, "wins"),
		Misses:        strString(in, "misses"),
		NextIdentity:  strString(in, "next_identity"),
		NextObjective: strString(in, "next_objective"),
		NextPattern:   strString(in, "next_pattern"),
	}
	if v, ok := in["taken"].(bool); ok {
		req.Taken = &v
	}
	if m, ok := in["answers"].(map[string]any); ok {
		req.Answers = m
	}

	store := pc.NewStore(t.pool)
	if err := store.Apply(ctx, action, id, req); err != nil {
		return "", err
	}

	// Return the refreshed coach position so the model immediately knows what
	// the write changed and what is due next, without a second tool call.
	cockpit, err := store.Cockpit(ctx, id, time.Now())
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok":          true,
		"action":      action,
		"day":         cockpit.State.CurrentDay,
		"cycle":       cockpit.State.CycleNumber,
		"next_phase":  cockpit.Guidance.Phase,
		"next_step":   cockpit.Guidance.Headline,
		"open_prompt": cockpit.Guidance.Prompt,
	})
	return string(out), nil
}
