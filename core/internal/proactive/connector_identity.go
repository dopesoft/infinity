package proactive

import (
	"context"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/connectors"
)

// ConnectorIdentityChecklist is the heartbeat hook that surfaces a
// finding whenever any active connected_account is missing its real
// upstream identity. The finding tells the agent to run the
// `resolve-connector-identities` skill — the generic, toolkit-agnostic
// recipe that calls each toolkit's profile verb (e.g. GMAIL_GET_PROFILE)
// once per account and persists the result via `connector_identity_set`.
//
// Zero toolkit-specific code lives here. The checklist only knows
// how to count and how to ask the agent to run the skill. The skill
// body holds the cognition; this checklist is purely the proactive
// trigger that closes the loop without waiting for the boss to ask.
//
// Composes via ComposeChecklists alongside DefaultChecklist +
// CuriosityChecklist; no-ops when the cache is nil so chat-only
// deployments don't break.
func ConnectorIdentityChecklist(cache *connectors.Cache) Checklist {
	return func(ctx context.Context, _ *Heartbeat) ([]Finding, error) {
		if cache == nil {
			return nil, nil
		}
		byToolkit := cache.AccountsByToolkit()
		missing := 0
		// Count missing across only ACTIVE accounts — dormant /
		// INITIATED / FAILED accounts can't be resolved anyway (their
		// profile verb would 401), so flagging them creates noise.
		for _, accs := range byToolkit {
			for _, a := range accs {
				if !strings.EqualFold(a.Status, "ACTIVE") {
					continue
				}
				if strings.TrimSpace(a.IdentityHint) == "" {
					missing++
				}
			}
		}
		if missing == 0 {
			return nil, nil
		}
		title := fmt.Sprintf("%d connected account(s) need identity resolution", missing)
		detail := strings.Join([]string{
			"action: run `resolve-connector-identities` via `skills_invoke({name:\"resolve-connector-identities\"})`",
			"why: it calls each toolkit's profile verb once per unresolved active account, extracts the canonical email/handle/login, and persists via connector_identity_set",
			"result: after it runs, later turns render the real identity in <connected_accounts> automatically",
			"safety: idempotent and safe to run unattended",
		}, "\n")
		return []Finding{{
			Kind:        "surprise",
			Title:       title,
			Detail:      detail,
			PreApproved: true,
			Source:      "connector_identity_resolution",
		}}, nil
	}
}
