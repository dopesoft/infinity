package proactive

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/google/uuid"
)

// RegisterTrustTools wires the agent-facing trust helpers. Today there's
// one: trust_batch_assign, which lets a skill retroactively group the
// pending contracts it just queued under a shared batch_id so the Studio
// Trust panel can bulk-approve them in one tap.
//
// The batch_id is per-skill-run; the agent picks the count to stamp (most
// recent N pending contracts) and optionally narrows by source. Inbox
// triage uses this after the GMAIL_CREATE_DRAFT calls have all queued
// through ComposioGate.
func RegisterTrustTools(reg *tools.Registry, store *TrustStore) {
	if reg == nil || store == nil {
		return
	}
	reg.Register(&trustBatchAssignTool{store: store})
}

type trustBatchAssignTool struct{ store *TrustStore }

func (t *trustBatchAssignTool) Name() string { return "trust_batch_assign" }

func (t *trustBatchAssignTool) Description() string {
	return "Group the N most recent pending Trust contracts under a fresh batch_id. " +
		"Use this AFTER a skill has queued multiple contracts (e.g. one per email draft) " +
		"so the boss can bulk-approve them with one tap in Studio's Trust panel. " +
		"`count` defaults to 50 (caps how many of the latest pending rows get stamped). " +
		"`source` optionally narrows to one gate ('composio_gate', 'claude_code_gate', etc.). " +
		"Returns {batch_id, updated}."
}

func (t *trustBatchAssignTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{
				"type":        "integer",
				"description": "How many of the latest pending contracts to stamp. Default 50, max 200.",
			},
			"source": map[string]any{
				"type":        "string",
				"description": "Optional source filter: 'composio_gate', 'claude_code_gate', 'github_gate', 'bridge_gate', 'budget_gate'. Empty = any source.",
			},
			"batch_id": map[string]any{
				"type":        "string",
				"description": "Optional UUID to reuse. Omit to mint a fresh one.",
			},
		},
	}
}

func (t *trustBatchAssignTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	if t.store == nil || t.store.pool == nil {
		return "", errors.New("trust_batch_assign: trust store not configured")
	}
	count := 50
	if v, ok := in["count"].(float64); ok && int(v) > 0 {
		count = int(v)
	}
	if count > 200 {
		count = 200
	}
	source, _ := in["source"].(string)
	batchID, _ := in["batch_id"].(string)
	if batchID == "" {
		batchID = uuid.NewString()
	}
	tag, err := t.store.pool.Exec(ctx, `
		UPDATE mem_trust_contracts
		   SET batch_id = $1::uuid
		 WHERE id IN (
		     SELECT id
		       FROM mem_trust_contracts
		      WHERE status = 'pending'
		        AND batch_id IS NULL
		        AND ($2 = '' OR source = $2)
		      ORDER BY created_at DESC
		      LIMIT $3
		 )
	`, batchID, source, count)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"batch_id": batchID,
		"updated":  tag.RowsAffected(),
	})
	return string(out), nil
}
