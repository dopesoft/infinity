// Package tools — Composio toolkit verb registration.
//
// Composio's MCP gateway only exposes 7 control tools to the agent
// (COMPOSIO_SEARCH_TOOLS, COMPOSIO_MULTI_EXECUTE_TOOL, etc.). That
// forces the agent into a multi-step "discover then execute" dance
// any time it wants to use a real toolkit verb like
// GMAIL_FETCH_EMAILS — and the dance is the failure mode that
// produced agent replies of "(input_tokens=…)" when the model gave
// up and ran arbitrary Python via REMOTE_WORKBENCH.
//
// RegisterComposioVerbs pre-registers every verb of every CONNECTED
// toolkit as a dormant entry in the local registry. The catalog
// block in the agent's system prompt collapses each toolkit to one
// line (`composio__GMAIL_* (23 verbs)`); the agent uses the same
// `tool_search` + `load_tools` pattern it already uses for every
// other dormant tool. Active-set schemas stay tight by design —
// only the verbs the model actually pulls in pay per-turn schema
// cost, never the whole 50+ catalog.
//
// Execution goes through connectors.ExecuteClient → Composio's
// `/api/v3/tools/execute/{slug}` REST endpoint, with the right
// `connected_account_id` resolved from the live Cache. No MCP
// indirection on the hot path, no Python sandbox fallback.
//
// This file lives in `tools/` (not `connectors/`) because
// `connectors/` is imported by `tools/cron_tools.go`; moving the
// Tool wrapper into `connectors/` would create an import cycle.
// The fetcher could in principle live next to ExecuteClient, but
// keeping the whole flow co-located reads better than splitting
// across packages for a one-line dependency win.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/connectors"
)

const composioToolsBase = "https://backend.composio.dev/api/v3"

// composioVerbDef is the relevant subset of Composio's /api/v3/tools row.
type composioVerbDef struct {
	Slug            string         `json:"slug"`             // e.g. "GMAIL_FETCH_EMAILS"
	Name            string         `json:"name"`             // human label
	Description     string         `json:"description"`
	Toolkit         composioVerbTK `json:"toolkit"`
	InputParameters map[string]any `json:"input_parameters"` // JSON Schema object
}

type composioVerbTK struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// fetchComposioVerbs paginates GET /api/v3/tools?toolkit_slug=X and
// returns every verb in the toolkit. Caller supplies the API key — we
// don't read env here so a key rotation is one explicit pass-through
// away.
func fetchComposioVerbs(ctx context.Context, hc *http.Client, key, toolkitSlug string) ([]composioVerbDef, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	var all []composioVerbDef
	cursor := ""
	for {
		q := url.Values{}
		q.Set("toolkit_slug", toolkitSlug)
		q.Set("limit", "100")
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		u := composioToolsBase + "/tools?" + q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("verbs build request: %w", err)
		}
		req.Header.Set("x-api-key", key)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := hc.Do(req)
		if err != nil {
			return nil, fmt.Errorf("verbs do: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			snippet := string(body)
			if len(snippet) > 200 {
				snippet = snippet[:200] + "…"
			}
			return nil, fmt.Errorf("verbs %s: %d %s", toolkitSlug, resp.StatusCode, snippet)
		}
		var page struct {
			Items      []composioVerbDef `json:"items"`
			NextCursor string            `json:"next_cursor"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("verbs decode: %w", err)
		}
		all = append(all, page.Items...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return all, nil
}

// composioVerb is the Tool implementation for one Composio verb.
// Execution routes through ExecuteClient (REST), not the MCP gateway —
// the agent calls this exactly the way it calls any other native tool.
type composioVerb struct {
	slug        string
	toolkitSlug string
	desc        string
	schema      map[string]any
	cache       *connectors.Cache
	exec        *connectors.ExecuteClient
}

func (v *composioVerb) Name() string           { return "composio__" + v.slug }
func (v *composioVerb) Description() string    { return v.desc }
func (v *composioVerb) Schema() map[string]any { return v.schema }

func (v *composioVerb) Execute(ctx context.Context, input map[string]any) (string, error) {
	// Separate the agent-supplied connected_account_id (our routing
	// field) from the verb's native arguments so Composio's execute
	// envelope receives them in the right slots.
	var accountID string
	args := make(map[string]any, len(input))
	for k, val := range input {
		if k == "connected_account_id" {
			if s, ok := val.(string); ok {
				accountID = strings.TrimSpace(s)
			}
			continue
		}
		args[k] = val
	}
	if accountID == "" {
		// Auto-route when there's exactly one connected account for
		// this toolkit. With multiple, the model has to choose — the
		// <connected_accounts> overlay enumerates the IDs and aliases
		// so the error message points back at that list.
		accs := v.cache.AccountsByToolkit()[v.toolkitSlug]
		switch len(accs) {
		case 0:
			return "", fmt.Errorf("no connected %s account — connect one in Settings → Connectors", v.toolkitSlug)
		case 1:
			accountID = accs[0].ID
		default:
			hints := make([]string, 0, len(accs))
			for _, a := range accs {
				label := a.Alias
				if label == "" {
					label = a.IdentityHint
				}
				if label != "" {
					hints = append(hints, fmt.Sprintf("%s (%s)", a.ID, label))
				} else {
					hints = append(hints, a.ID)
				}
			}
			return "", fmt.Errorf("connected_account_id required: %d %s accounts connected — pass one of %v (see <connected_accounts>)", len(accs), v.toolkitSlug, hints)
		}
	}

	account, err := v.accountByID(accountID)
	if err != nil {
		return "", err
	}
	entityID := strings.TrimSpace(account.UserID)
	if entityID == "" {
		return "", fmt.Errorf("connected account %s is missing Composio entity binding (user_id); reconnect it or patch the account registry", accountID)
	}

	resp, err := v.exec.Execute(ctx, connectors.ExecuteRequest{
		Slug:               v.slug,
		ConnectedAccountID: accountID,
		EntityID:           entityID,
		Arguments:          args,
	})
	if err != nil {
		return "", err
	}
	if !resp.Successful {
		if msg := strings.TrimSpace(resp.Error); msg != "" {
			return "", fmt.Errorf("composio %s: %s", v.slug, msg)
		}
		return "", fmt.Errorf("composio %s: not successful", v.slug)
	}
	if len(resp.Data) == 0 {
		return "{}", nil
	}
	return string(resp.Data), nil
}

func (v *composioVerb) accountByID(accountID string) (*connectors.Account, error) {
	if v == nil || v.cache == nil {
		return nil, fmt.Errorf("composio verb cache not configured")
	}
	for _, accs := range v.cache.AccountsByToolkit() {
		for _, a := range accs {
			if a != nil && a.ID == accountID {
				return a, nil
			}
		}
	}
	return nil, fmt.Errorf("connected account %s not found in cache", accountID)
}

// buildComposioVerbSchema augments a Composio verb's native
// input_parameters with a top-level `connected_account_id` field so the
// agent can route across multiple accounts of the same toolkit. We do
// NOT add it to required[] because the Execute path resolves a
// single-account toolkit's id automatically — only multi-account
// toolkits force the model to pick.
func buildComposioVerbSchema(toolkitSlug string, params map[string]any) map[string]any {
	out := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	// Copy the verb's native schema verbatim (type/properties/required/
	// etc.) so JSON Schema features stay intact for the LLM.
	for k, v := range params {
		out[k] = v
	}
	props, _ := out["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		out["properties"] = props
	}
	props["connected_account_id"] = map[string]any{
		"type": "string",
		"description": "Composio connected_account_id (e.g. ca_…). " +
			"Required when the boss has multiple " + toolkitSlug +
			" accounts connected — match the alias against the boss's intent. " +
			"Optional when there's only one (auto-resolved). " +
			"See the <connected_accounts> block for valid IDs + aliases.",
	}
	return out
}

// ComposioVerbSync owns the runtime adaptation loop for Composio toolkit
// verbs: a stateful diff between what's connected (cache snapshot) and
// what's registered (its own tracked set). Hot-reloading is the point —
// when the boss connects a new toolkit, its verbs light up in the agent's
// catalog within one cache refresh tick (default 60s) with no redeploy,
// and verbs disappear from the catalog when an account is disconnected.
//
// Reg, Cache, Exec, KeyFn are required. Sync is idempotent + safe under
// concurrent calls; the mutex serializes the diff/register/unregister
// path so two near-simultaneous refreshes can't double-fetch a toolkit.
type ComposioVerbSync struct {
	Reg   *Registry
	Cache *connectors.Cache
	Exec  *connectors.ExecuteClient
	KeyFn func() string

	mu         sync.Mutex
	registered map[string][]string // toolkit_slug → tool names we own
	logger     *log.Logger
}

// infoLog writes to stdout so Railway tags these lines severity=info
// instead of severity=error. The stdlib `log` package writes to stderr
// by default; reserve that for genuine failures only.
func (s *ComposioVerbSync) info() *log.Logger {
	if s.logger == nil {
		s.logger = log.New(os.Stdout, "", log.LstdFlags)
	}
	return s.logger
}

// Sync diffs the live cache snapshot against the registrar's tracked set
// and brings the registry into alignment. Toolkits newly visible get
// their verbs fetched + registered; toolkits no longer live get their
// previously-registered verbs unregistered so the catalog never lies.
//
// Returns (added, removed, toolkitsActive, error). The error is the
// first toolkit-level fetch failure; other toolkits keep processing so
// one bad upstream doesn't strand the rest.
func (s *ComposioVerbSync) Sync(ctx context.Context) (added, removed, toolkitsActive int, err error) {
	if s == nil || s.Reg == nil || s.Cache == nil || s.Exec == nil || s.KeyFn == nil {
		return 0, 0, 0, nil
	}
	key := strings.TrimSpace(s.KeyFn())
	if key == "" {
		return 0, 0, 0, fmt.Errorf("no Composio key (set COMPOSIO_ADMIN_API_KEY)")
	}
	live := s.Cache.AccountsByToolkit()

	// Determine which toolkits have ≥1 ACTIVE account right now. We
	// only register verbs for those; INITIATED / PENDING / FAILED
	// rows have no usable connected_account_id.
	wantActive := make(map[string]struct{}, len(live))
	for slug, accs := range live {
		for _, a := range accs {
			if strings.EqualFold(a.Status, "ACTIVE") {
				wantActive[slug] = struct{}{}
				break
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.registered == nil {
		s.registered = make(map[string][]string)
	}

	// Drop toolkits we own that are no longer active.
	for slug, names := range s.registered {
		if _, keep := wantActive[slug]; keep {
			continue
		}
		for _, n := range names {
			s.Reg.Unregister(n)
		}
		removed += len(names)
		delete(s.registered, slug)
		s.info().Printf("composio: hot-reload unregistered %d verbs for %s (account disconnected)", len(names), slug)
	}

	// Bring up toolkits that are active but not yet registered.
	hc := &http.Client{Timeout: 30 * time.Second}
	var firstErr error
	for slug := range wantActive {
		if _, already := s.registered[slug]; already {
			continue
		}
		defs, e := fetchComposioVerbs(ctx, hc, key, slug)
		if e != nil {
			s.info().Printf("composio: fetch verbs for %s: %v", slug, e)
			if firstErr == nil {
				firstErr = e
			}
			continue
		}
		toolNames := make([]string, 0, len(defs))
		for _, d := range defs {
			name := "composio__" + d.Slug
			s.Reg.Register(&composioVerb{
				slug:        d.Slug,
				toolkitSlug: strings.ToLower(slug),
				desc:        strings.TrimSpace(d.Description),
				schema:      buildComposioVerbSchema(strings.ToLower(slug), d.InputParameters),
				cache:       s.Cache,
				exec:        s.Exec,
			})
			toolNames = append(toolNames, name)
		}
		s.registered[slug] = toolNames
		added += len(toolNames)
		s.info().Printf("composio: hot-reload registered %d verbs for %s", len(toolNames), slug)
	}
	return added, removed, len(wantActive), firstErr
}