// Connector identity tools - the generic write-back the agent uses to
// persist what it discovered about a connected account.
//
// Design intent (per Rule #1 in CLAUDE.md): zero toolkit-specific code
// in Go. The Composio listing surfaces account ids + slugs; the system
// prompt block tells the agent that when an identity is missing it
// should load the toolkit's identity verb (whatever it's called -
// GMAIL_GET_PROFILE, SLACK_AUTH_TEST, GITHUB_GET_AUTHENTICATED_USER),
// call it, pull the canonical handle out of the response, and persist
// here. The agent figures out which verb to call and how to parse it.
// We just provide the generic landing pad: account_id → identity string.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/dopesoft/infinity/core/internal/connectors"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterConnectorTools wires the generic connector tools:
//   - connector_identity_set   (persist a resolved account identity)
//   - connector_accounts_list  (enumerate live accounts — kills hardcoded ids)
//   - connector_coverage_mark  (record a per-mailbox triage pass)
//
// No-op when the cache is nil so chat-only / DB-less deployments don't break.
// pool may be nil; connector_coverage_mark only registers when it's present.
func RegisterConnectorTools(r *Registry, cache *connectors.Cache, pool *pgxpool.Pool) {
	if r == nil || cache == nil {
		return
	}
	r.Register(&connectorIdentitySet{cache: cache})
	r.Register(&connectorAccountsList{cache: cache})
	if pool != nil {
		r.Register(&connectorCoverageMark{pool: pool, cache: cache})
	}
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
		"block automatically - no need to re-resolve. Pass empty string to " +
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
				"description": "The real upstream identity - Gmail emailAddress, Slack user handle, GitHub login, etc. The shortest unambiguous string a human would use to refer to this account.",
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

// ── connector_accounts_list ──────────────────────────────────────────────
//
// The generic building block that lets a recipe DISCOVER which mailboxes /
// workspaces / repos are connected RIGHT NOW, instead of hardcoding account
// ids in a cron string. A revoke+reconnect mints a fresh account id; a cron
// that listed the old id silently goes blind (this is exactly what dropped
// the boss's lawyer email on 2026-05-28). A recipe that calls this every run
// always sees the live set, so reconnects self-heal with zero edits.
//
// Reads the same in-process cache that builds the <connected_accounts>
// system-prompt overlay, so it never costs a network hop and matches what
// the agent already sees. Zero toolkit-specific Go.

type connectorAccountsList struct {
	cache *connectors.Cache
}

func (t *connectorAccountsList) Name() string   { return "connector_accounts_list" }
func (t *connectorAccountsList) ReadOnly() bool { return true }
func (t *connectorAccountsList) Description() string {
	return "List the connected third-party accounts the boss has linked, with their " +
		"live status and identity. Use this to discover accounts dynamically instead " +
		"of hardcoding account ids — e.g. at the top of an inbox-triage run, call " +
		"connector_accounts_list({toolkit:\"gmail\"}) to get every active mailbox, so " +
		"a reconnected account is covered automatically. Returns account_id, identity, " +
		"alias, toolkit, and status. Defaults to ACTIVE accounts only; pass " +
		"include_inactive:true to also see REVOKED/INITIATED ones. Omit toolkit to list all."
}
func (t *connectorAccountsList) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"toolkit": map[string]any{
				"type":        "string",
				"description": "Toolkit slug to filter by (e.g. \"gmail\", \"slack\", \"github\"). Omit to list every connected account across all toolkits.",
			},
			"include_inactive": map[string]any{
				"type":        "boolean",
				"description": "When true, include non-ACTIVE accounts (REVOKED/INITIATED/FAILED). Default false — only live accounts you can actually use.",
			},
		},
	}
}
func (t *connectorAccountsList) Execute(ctx context.Context, in map[string]any) (string, error) {
	toolkit := strings.ToLower(strings.TrimSpace(asString(in["toolkit"])))
	includeInactive, _ := in["include_inactive"].(bool)

	type acctOut struct {
		AccountID string `json:"account_id"`
		Toolkit   string `json:"toolkit"`
		Identity  string `json:"identity,omitempty"`
		Alias     string `json:"alias,omitempty"`
		Status    string `json:"status"`
	}
	out := []acctOut{}

	emit := func(slug string, a *connectors.Account) {
		out = append(out, acctOut{
			AccountID: a.ID,
			Toolkit:   slug,
			Identity:  a.IdentityHint,
			Alias:     a.Alias,
			Status:    a.Status,
		})
	}

	if toolkit != "" {
		var accs []*connectors.Account
		if includeInactive {
			accs = t.cache.AccountsByToolkit()[toolkit]
		} else {
			accs = t.cache.ActiveAccountsByToolkit(toolkit)
		}
		for _, a := range accs {
			emit(toolkit, a)
		}
	} else {
		byTk := t.cache.AccountsByToolkit()
		slugs := make([]string, 0, len(byTk))
		for s := range byTk {
			slugs = append(slugs, s)
		}
		sort.Strings(slugs)
		for _, s := range slugs {
			for _, a := range byTk[s] {
				if !includeInactive && !strings.EqualFold(a.Status, "ACTIVE") {
					continue
				}
				emit(s, a)
			}
		}
	}

	resp := map[string]any{
		"count":    len(out),
		"accounts": out,
	}
	b, _ := json.Marshal(resp)
	return string(b), nil
}

// ── connector_coverage_mark ──────────────────────────────────────────────
//
// The per-mailbox heartbeat that turns a silent 9-day blind spot into a 12h
// alarm. A triage recipe calls this once per account at the end of its pass —
// EVEN when the inbox was quiet — so we always know the last time each mailbox
// was actually scanned, not just whether the cron as a whole reported "ok".
// ConnectorCoverageChecklist reads these rows and surfaces a finding when any
// active account goes stale. Generic landing pad; the recipe owns the cognition.

type connectorCoverageMark struct {
	pool  *pgxpool.Pool
	cache *connectors.Cache
}

func (t *connectorCoverageMark) Name() string   { return "connector_coverage_mark" }
func (t *connectorCoverageMark) ReadOnly() bool { return false }
func (t *connectorCoverageMark) Description() string {
	return "Record that you just finished a coverage pass over a connected account " +
		"(e.g. one mailbox during inbox triage). Call this once per account at the END " +
		"of its pass — even when nothing needed surfacing — so the system knows the " +
		"mailbox was actually scanned. This is what powers the staleness alarm that " +
		"catches an inbox silently dropping out of coverage after a reconnect. " +
		"status: \"ok\" on a clean pass, \"error\" if the account could not be scanned " +
		"(include the trimmed error)."
}
func (t *connectorCoverageMark) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"account_id": map[string]any{
				"type":        "string",
				"description": "Composio connected_account id (ca_xxx) you just triaged.",
			},
			"toolkit": map[string]any{
				"type":        "string",
				"description": "Toolkit slug, e.g. \"gmail\". Defaults to \"gmail\" if omitted.",
			},
			"status": map[string]any{
				"type":        "string",
				"description": "\"ok\" if the pass completed, \"error\" if the account couldn't be scanned.",
				"enum":        []string{"ok", "error"},
			},
			"error": map[string]any{
				"type":        "string",
				"description": "Trimmed error text when status is \"error\". Omit on success.",
			},
		},
		"required": []string{"account_id", "status"},
	}
}
func (t *connectorCoverageMark) Execute(ctx context.Context, in map[string]any) (string, error) {
	accountID := strings.TrimSpace(asString(in["account_id"]))
	if accountID == "" {
		return "", fmt.Errorf("account_id required")
	}
	toolkit := strings.ToLower(strings.TrimSpace(asString(in["toolkit"])))
	if toolkit == "" {
		toolkit = "gmail"
	}
	status := strings.ToLower(strings.TrimSpace(asString(in["status"])))
	if status != "ok" && status != "error" {
		return "", fmt.Errorf("status must be \"ok\" or \"error\"")
	}
	errText := strings.TrimSpace(asString(in["error"]))

	// Identity is best-effort context for the staleness finding; pull from the
	// live cache so the alarm can say "khaya@malabieindustries.com" not "ca_xxx".
	identity := ""
	if t.cache != nil {
		identity = t.cache.Identities()[accountID]
	}

	_, err := t.pool.Exec(ctx, `
		INSERT INTO mem_connector_coverage
		  (toolkit, account_id, identity, last_triaged_at, last_status, last_error, updated_at)
		VALUES ($1, $2, NULLIF($3,''), NOW(), $4, NULLIF($5,''), NOW())
		ON CONFLICT (toolkit, account_id) DO UPDATE SET
		  identity        = COALESCE(NULLIF(EXCLUDED.identity,''), mem_connector_coverage.identity),
		  last_triaged_at = NOW(),
		  last_status     = EXCLUDED.last_status,
		  last_error      = EXCLUDED.last_error,
		  updated_at      = NOW()
	`, toolkit, accountID, identity, status, errText)
	if err != nil {
		return "", fmt.Errorf("coverage mark: %w", err)
	}

	resp := map[string]any{"ok": true, "account_id": accountID, "toolkit": toolkit, "status": status}
	b, _ := json.Marshal(resp)
	return string(b), nil
}

// asString coerces an arbitrary JSON-decoded value to a trimmed string.
func asString(v any) string {
	s, _ := v.(string)
	return s
}
