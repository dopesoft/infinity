// Connector identity tools — the generic write-back the agent uses to
// persist what it discovered about a connected account.
//
// Design intent (per Rule #1 in CLAUDE.md): zero toolkit-specific code
// in Go. The Composio listing surfaces account ids + slugs; the system
// prompt block tells the agent that when an identity is missing it
// should load the toolkit's identity verb (whatever it's called —
// GMAIL_GET_PROFILE, SLACK_AUTH_TEST, GITHUB_GET_AUTHENTICATED_USER),
// call it, pull the canonical handle out of the response, and persist
// here. The agent figures out which verb to call and how to parse it.
// We just provide the generic landing pad: account_id → identity string.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/connectors"
)

// RegisterConnectorTools wires connector_identity_set. No-op when the
// cache is nil so chat-only / DB-less deployments don't break.
func RegisterConnectorTools(r *Registry, cache *connectors.Cache) {
	if r == nil || cache == nil {
		return
	}
	r.Register(&connectorIdentitySet{cache: cache})
}

// ── connector_identity_set ───────────────────────────────────────────────

type connectorIdentitySet struct {
	cache *connectors.Cache
}

func (t *connectorIdentitySet) Name() string   { return "connector_identity_set" }
func (t *connectorIdentitySet) ReadOnly() bool { return false }
func (t *connectorIdentitySet) Description() string {
	return "Persist the upstream identity (email / username / handle / login) " +
		"for a connected_account. Call this once after you've resolved an " +
		"account's identity by hitting the toolkit's profile verb " +
		"(e.g. GMAIL_GET_PROFILE for Gmail, SLACK_AUTH_TEST for Slack). " +
		"Future turns will see the identity in the <connected_accounts> " +
		"block automatically — no need to re-resolve. Pass empty string to " +
		"clear a stale value (e.g. after a disconnect)."
}
func (t *connectorIdentitySet) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"account_id": map[string]any{
				"type":        "string",
				"description": "Composio connected_account id (e.g. ca_xxx) from the <connected_accounts> block.",
			},
			"identity": map[string]any{
				"type":        "string",
				"description": "The real upstream identity — Gmail emailAddress, Slack user handle, GitHub login, etc. The shortest unambiguous string a human would use to refer to this account.",
			},
		},
		"required": []string{"account_id", "identity"},
	}
}
func (t *connectorIdentitySet) Execute(ctx context.Context, in map[string]any) (string, error) {
	accountID, _ := in["account_id"].(string)
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return "", fmt.Errorf("account_id required")
	}
	identity, _ := in["identity"].(string)
	identity = strings.TrimSpace(identity)
	if err := t.cache.SetIdentity(ctx, accountID, identity); err != nil {
		return "", err
	}
	resp := map[string]any{
		"ok":         true,
		"account_id": accountID,
		"identity":   identity,
	}
	b, _ := json.Marshal(resp)
	return string(b), nil
}
