// Package connectors owns the in-process picture of "which third-party
// SaaS accounts the boss has connected, and what they're called." Today
// the canonical source is Composio (one row per OAuth grant in their
// connected_accounts table); the cache mirrors that list periodically so
// the agent loop can render account-aware catalog hints without a network
// hop on every turn.
//
// Aliases (human-readable labels — "personal", "work", "support inbox")
// live in `infinity_meta` under a single JSON blob keyed by Composio's
// connected_account id. Editing happens via REST from Studio; reads are
// folded into the cache snapshot.
package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Account is the cached projection of one Composio connected_account
// row plus the boss's alias overlay. Field set is intentionally narrow
// — only what the agent loop needs to render its system-prompt block.
type Account struct {
	ID           string    `json:"id"`
	ToolkitSlug  string    `json:"toolkit_slug"`
	ToolkitName  string    `json:"toolkit_name"`
	Status       string    `json:"status"`
	Alias        string    `json:"alias,omitempty"`
	UserID       string    `json:"user_id,omitempty"` // the entity id passed to Composio at Connect
	IdentityHint string    `json:"identity_hint,omitempty"` // best-effort email/username extracted from Composio meta
	CreatedAt    time.Time `json:"created_at,omitempty"`
}

// MetaKey is the infinity_meta row that stores the alias map. One JSON
// blob (account_id → alias) keeps the schema migration-free and reads
// cheap; with O(dozens) of accounts the alternative table would be
// pure overhead.
const MetaKey = "connectors_aliases"

// IdentityMetaKey is the infinity_meta row that caches the real OAuth
// identity per connected_account (e.g. Gmail's emailAddress). Composio's
// /connected_accounts list response often omits the upstream identity —
// for Gmail you have to call GMAIL_GET_PROFILE to learn the email — so
// we fetch lazily on the first Refresh after a new account appears,
// cache forever, and overlay onto every system-prompt render.
//
// Why persist: re-fetching on every boot is wasted REST calls (one per
// connected account, every container start). The OAuth identity doesn't
// change without a disconnect/reconnect, so a permanent cache is correct.
const IdentityMetaKey = "connectors_identities"

// Cache is the live "what's connected" snapshot. Concurrent-safe via
// RWMutex; refresh happens in the background ticker plus on explicit
// Refresh() (called after Connect/Disconnect to avoid the cache-stale
// race). Read-only consumers (the agent loop's catalog builder) take
// the RLock and walk a sorted view.
type Cache struct {
	pool       *pgxpool.Pool
	adminKey   func() string // late-bound so a Railway env hot-swap takes effect
	apiBaseURL string
	httpClient *http.Client
	refresh    time.Duration

	mu          sync.RWMutex
	byToolkit   map[string][]*Account
	aliases     map[string]string
	// identities is the agent-discovered overlay: account_id → real
	// upstream identity (email / handle / username). Written by the
	// generic `connector_identity_set` tool — Composio's list response
	// doesn't reliably include OAuth identity, but the agent has every
	// toolkit's verbs and can call e.g. GMAIL_GET_PROFILE / slack auth.test
	// / github__get_me itself, then persist the result here. Generic
	// store; zero toolkit-specific Go.
	identities  map[string]string
	lastRefresh time.Time
	lastErr     string

	// onChange fires after a successful Refresh whenever the set of
	// (toolkit_slug → connected account count) changed since the last
	// snapshot. The toolkit-verb registrar subscribes to this so a new
	// Composio connection lights up the agent's tool catalog without
	// waiting for a redeploy — runtime adaptation is the point.
	onChangeMu sync.Mutex
	onChange   func()
	lastShape  map[string]int

	cancel context.CancelFunc
}

// New builds the cache. `adminKey` is a getter so we read Composio's
// admin key every refresh and pick up Railway env changes without a
// restart (mirrors composioRESTKey in connectors_api.go).
func New(pool *pgxpool.Pool, adminKey func() string) *Cache {
	return &Cache{
		pool:       pool,
		adminKey:   adminKey,
		apiBaseURL: "https://backend.composio.dev/api/v3",
		httpClient: &http.Client{Timeout: 20 * time.Second},
		refresh:    60 * time.Second,
		byToolkit:  make(map[string][]*Account),
		aliases:    make(map[string]string),
		identities: make(map[string]string),
	}
}

// Start kicks the background refresh loop. Idempotent — calling twice
// is harmless. Stop with cancel from the parent context.
func (c *Cache) Start(ctx context.Context) {
	if c.cancel != nil {
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	// Prime once synchronously so the first turn after boot sees data.
	_ = c.Refresh(loopCtx)
	go func() {
		t := time.NewTicker(c.refresh)
		defer t.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-t.C:
				_ = c.Refresh(loopCtx)
			}
		}
	}()
}

// Stop cancels the background refresh. Safe to call multiple times.
func (c *Cache) Stop() {
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
}

// Refresh forces an out-of-band reload of accounts + aliases. Used by
// the connectors API after Connect/Disconnect so the agent doesn't
// have to wait for the next tick.
func (c *Cache) Refresh(ctx context.Context) error {
	aliases, err := c.loadAliases(ctx)
	if err != nil {
		c.recordErr("alias load: " + err.Error())
		// Continue — we can still refresh the account list, just without alias overlay.
	}
	identities, err := c.loadIdentities(ctx)
	if err != nil {
		c.recordErr("identity load: " + err.Error())
		// Continue — identity overlay is optional.
	}
	accounts, err := c.loadAccounts(ctx)
	if err != nil {
		c.recordErr("composio list: " + err.Error())
		return err
	}
	// Apply alias + identity overlays before storing. Listing-side
	// identity (extractIdentityHint) is a default; persisted identity
	// (set by the agent via connector_identity_set) wins when present
	// because it's the canonical truth confirmed against the upstream.
	for _, a := range accounts {
		if v, ok := aliases[a.ID]; ok {
			a.Alias = v
		}
		if v, ok := identities[a.ID]; ok && v != "" {
			a.IdentityHint = v
		}
	}

	byToolkit := make(map[string][]*Account, len(accounts))
	for _, a := range accounts {
		byToolkit[strings.ToLower(a.ToolkitSlug)] = append(byToolkit[strings.ToLower(a.ToolkitSlug)], a)
	}
	for slug := range byToolkit {
		sort.Slice(byToolkit[slug], func(i, j int) bool {
			return byToolkit[slug][i].CreatedAt.Before(byToolkit[slug][j].CreatedAt)
		})
	}

	c.mu.Lock()
	c.byToolkit = byToolkit
	c.aliases = aliases
	c.identities = identities
	c.lastRefresh = time.Now()
	c.lastErr = ""
	c.mu.Unlock()

	// Snapshot the shape (toolkit → active-account count) and fire
	// onChange if it diverges from the last seen shape. Cheap diff
	// keeps a noop refresh from doing real work on the subscriber.
	shape := make(map[string]int, len(byToolkit))
	for slug, accs := range byToolkit {
		active := 0
		for _, a := range accs {
			if strings.EqualFold(a.Status, "ACTIVE") {
				active++
			}
		}
		if active > 0 {
			shape[slug] = active
		}
	}
	c.onChangeMu.Lock()
	changed := !sameShape(c.lastShape, shape)
	c.lastShape = shape
	cb := c.onChange
	c.onChangeMu.Unlock()
	if changed && cb != nil {
		// Async so a slow subscriber can't stall the refresh ticker.
		go cb()
	}
	return nil
}

// SetOnChange wires a callback fired after Refresh whenever the set of
// connected toolkits (or per-toolkit active-account counts) actually
// changes. Idempotent: passing nil clears the subscription. The
// callback runs in its own goroutine so a slow subscriber never blocks
// the refresh ticker.
func (c *Cache) SetOnChange(fn func()) {
	c.onChangeMu.Lock()
	c.onChange = fn
	c.onChangeMu.Unlock()
}

// sameShape returns true when the two toolkit→count maps are identical.
// Used by Refresh to decide whether to fire the onChange callback.
func sameShape(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func (c *Cache) recordErr(msg string) {
	c.mu.Lock()
	c.lastErr = msg
	c.mu.Unlock()
	log.Printf("connectors cache: %s", msg)
}

// AccountsByToolkit returns a snapshot of cached accounts keyed by lower-
// case toolkit slug. Callers must not mutate returned slices.
func (c *Cache) AccountsByToolkit() map[string][]*Account {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string][]*Account, len(c.byToolkit))
	for k, v := range c.byToolkit {
		out[k] = v
	}
	return out
}

// Status reports refresh health for diagnostics.
type Status struct {
	LastRefresh time.Time `json:"last_refresh"`
	Toolkits    int       `json:"toolkits"`
	Accounts    int       `json:"accounts"`
	LastError   string    `json:"last_error,omitempty"`
}

func (c *Cache) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	total := 0
	for _, v := range c.byToolkit {
		total += len(v)
	}
	return Status{
		LastRefresh: c.lastRefresh,
		Toolkits:    len(c.byToolkit),
		Accounts:    total,
		LastError:   c.lastErr,
	}
}

// SetAlias upserts the alias for a single connected_account id and
// invalidates the cache so the next read sees the new label. Persists
// to infinity_meta atomically by re-marshalling the full map (we have
// O(dozens) of entries — JSON in a single row is fine).
func (c *Cache) SetAlias(ctx context.Context, accountID, alias string) error {
	if strings.TrimSpace(accountID) == "" {
		return fmt.Errorf("account id required")
	}
	c.mu.Lock()
	if c.aliases == nil {
		c.aliases = make(map[string]string)
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		delete(c.aliases, accountID)
	} else {
		c.aliases[accountID] = alias
	}
	snapshot := make(map[string]string, len(c.aliases))
	for k, v := range c.aliases {
		snapshot[k] = v
	}
	// Update the live by-toolkit overlay so callers reading right
	// after SetAlias see the new value without waiting for Refresh.
	for _, accs := range c.byToolkit {
		for _, a := range accs {
			if a.ID == accountID {
				a.Alias = alias
			}
		}
	}
	c.mu.Unlock()
	return c.saveAliases(ctx, snapshot)
}

// Aliases returns a copy of the alias map.
func (c *Cache) Aliases() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]string, len(c.aliases))
	for k, v := range c.aliases {
		out[k] = v
	}
	return out
}

// SetIdentity upserts the agent-discovered upstream identity for a
// connected_account (e.g. Gmail's emailAddress, Slack's user handle,
// GitHub's login). Called by the generic `connector_identity_set` tool.
// Generic — no toolkit-specific code lives here; the agent decides what
// verb to call to learn the identity and writes the result back here.
func (c *Cache) SetIdentity(ctx context.Context, accountID, identity string) error {
	if strings.TrimSpace(accountID) == "" {
		return fmt.Errorf("account id required")
	}
	identity = strings.TrimSpace(identity)
	c.mu.Lock()
	if c.identities == nil {
		c.identities = make(map[string]string)
	}
	if identity == "" {
		delete(c.identities, accountID)
	} else {
		c.identities[accountID] = identity
	}
	snapshot := make(map[string]string, len(c.identities))
	for k, v := range c.identities {
		snapshot[k] = v
	}
	// Update the live by-toolkit overlay so callers reading right after
	// SetIdentity see the new value without waiting for Refresh.
	for _, accs := range c.byToolkit {
		for _, a := range accs {
			if a.ID == accountID {
				a.IdentityHint = identity
			}
		}
	}
	c.mu.Unlock()
	return c.saveIdentities(ctx, snapshot)
}

// Identities returns a copy of the identity map (account_id → identity).
func (c *Cache) Identities() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]string, len(c.identities))
	for k, v := range c.identities {
		out[k] = v
	}
	return out
}

// loadIdentities reads the persisted identity blob. Same pattern as
// loadAliases — JSON in a single infinity_meta row.
func (c *Cache) loadIdentities(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string)
	if c.pool == nil {
		return out, nil
	}
	var raw string
	err := c.pool.QueryRow(ctx, `SELECT value FROM infinity_meta WHERE key = $1`, IdentityMetaKey).Scan(&raw)
	if err != nil {
		return out, nil
	}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if jerr := json.Unmarshal([]byte(raw), &out); jerr != nil {
		return out, fmt.Errorf("identity blob malformed: %w", jerr)
	}
	return out, nil
}

func (c *Cache) saveIdentities(ctx context.Context, m map[string]string) error {
	if c.pool == nil {
		return fmt.Errorf("no db pool")
	}
	b, _ := json.Marshal(m)
	_, err := c.pool.Exec(ctx, `
		INSERT INTO infinity_meta (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
	`, IdentityMetaKey, string(b))
	if err != nil {
		return fmt.Errorf("save identities: %w", err)
	}
	return nil
}

// loadAliases reads the alias JSON blob from infinity_meta. Missing
// row = empty map, not an error — first-time boot has no aliases.
func (c *Cache) loadAliases(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string)
	if c.pool == nil {
		return out, nil
	}
	var raw string
	err := c.pool.QueryRow(ctx, `SELECT value FROM infinity_meta WHERE key = $1`, MetaKey).Scan(&raw)
	if err != nil {
		// pgx returns ErrNoRows for missing — treat as empty map.
		return out, nil
	}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if jerr := json.Unmarshal([]byte(raw), &out); jerr != nil {
		return out, fmt.Errorf("alias blob malformed: %w", jerr)
	}
	return out, nil
}

func (c *Cache) saveAliases(ctx context.Context, m map[string]string) error {
	if c.pool == nil {
		return fmt.Errorf("no db pool")
	}
	b, _ := json.Marshal(m)
	_, err := c.pool.Exec(ctx, `
		INSERT INTO infinity_meta (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
	`, MetaKey, string(b))
	if err != nil {
		return fmt.Errorf("save aliases: %w", err)
	}
	return nil
}

// loadAccounts hits Composio's /connected_accounts and projects each
// row into our narrow Account shape. We deliberately keep the projection
// minimal — extra fields can be re-fetched on demand.
func (c *Cache) loadAccounts(ctx context.Context) ([]*Account, error) {
	key := c.adminKey()
	if key == "" {
		return nil, fmt.Errorf("COMPOSIO_ADMIN_API_KEY not set")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBaseURL+"/connected_accounts?limit=200", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("composio %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		Items []struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			UserID    string `json:"user_id"`
			CreatedAt string `json:"created_at"`
			Toolkit   struct {
				Slug string `json:"slug"`
				Name string `json:"name"`
			} `json:"toolkit"`
			// Composio's connected_account shape includes auth meta that
			// often surfaces the OAuth identity (email/username). We pluck
			// a best-effort hint without over-fitting their schema.
			Meta map[string]any `json:"meta"`
			Data map[string]any `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	out := make([]*Account, 0, len(parsed.Items))
	for _, it := range parsed.Items {
		a := &Account{
			ID:          it.ID,
			ToolkitSlug: it.Toolkit.Slug,
			ToolkitName: it.Toolkit.Name,
			Status:      it.Status,
			UserID:      it.UserID,
		}
		if t, err := time.Parse(time.RFC3339, it.CreatedAt); err == nil {
			a.CreatedAt = t
		}
		a.IdentityHint = extractIdentityHint(it.Meta, it.Data)
		out = append(out, a)
	}
	return out, nil
}

// extractIdentityHint walks Composio's meta/data blobs looking for the
// OAuth-side identity the user would recognise (email > username >
// display_name > generic name). Best-effort — when nothing surfaces we
// return "" and the UI falls back to the account id tail.
func extractIdentityHint(parts ...map[string]any) string {
	keys := []string{"email", "username", "user_email", "display_name", "name", "login"}
	for _, m := range parts {
		if m == nil {
			continue
		}
		for _, k := range keys {
			if v, ok := m[k]; ok {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
		// Dig one level deeper — Composio nests OAuth identity under
		// "user", "profile", or "account" depending on the toolkit.
		for _, k := range []string{"user", "profile", "account", "authed_user"} {
			if nested, ok := m[k].(map[string]any); ok {
				for _, key := range keys {
					if v, ok := nested[key]; ok {
						if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
							return strings.TrimSpace(s)
						}
					}
				}
			}
		}
	}
	return ""
}

// SystemPromptBlock renders the connected-accounts overlay for the agent
// loop's per-turn system prompt. The model sees the toolkit-by-toolkit
// breakdown with alias → account_id mappings, so when a tool schema asks
// for `connected_account_id` the model can pick the right one based on
// the user's intent ("send from work account").
//
// Returns "" when nothing is connected so the prompt stays clean.
func (c *Cache) SystemPromptBlock() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.byToolkit) == 0 {
		return ""
	}
	slugs := make([]string, 0, len(c.byToolkit))
	for slug := range c.byToolkit {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	// Detect any account missing its real upstream identity so we can
	// tell the agent to self-resolve. The list is rendered inline below
	// each toolkit so the instruction is right next to the data.
	missingIdentity := false
	for _, accs := range c.byToolkit {
		for _, a := range accs {
			if a.IdentityHint == "" {
				missingIdentity = true
				break
			}
		}
		if missingIdentity {
			break
		}
	}

	var b strings.Builder
	b.WriteString("<connected_accounts>\n")
	b.WriteString("The boss has authenticated the following SaaS accounts via Composio. Every verb of each connected toolkit is already in your <tool_catalog> as `composio__TOOLKIT_VERB` — discover with `tool_search(\"send gmail\")`, bring online with `load_tools([\"composio__GMAIL_SEND_EMAIL\"])`, then call it directly. The gateway tools (`composio__COMPOSIO_SEARCH_TOOLS`, `composio__COMPOSIO_MULTI_EXECUTE_TOOL`) are a fallback for toolkits whose verbs failed to pre-register; do not reach for `composio__COMPOSIO_REMOTE_WORKBENCH` / `_BASH_TOOL` for anything a real toolkit verb covers.\n\n")
	b.WriteString("Pass `connected_account_id` to every Composio verb that hits an authenticated upstream. When there's exactly one account for a toolkit it's auto-resolved; with multiple accounts (e.g. personal + work Gmail) you must pick — match the boss's intent against the alias (\"send from work\" → alias=work → that id) AND the `identity` (the real email/handle) so you can answer questions like \"which email did I connect for work?\" without guessing. If the intent is ambiguous, ask before sending.\n\n")
	if missingIdentity {
		b.WriteString("Some accounts below have no `identity` yet — meaning you don't yet know the real email/handle/login behind each `ca_*` id. Run the `resolve-connector-identities` skill now (via `skills_invoke({name:\"resolve-connector-identities\"})`) — it carries the generic, toolkit-agnostic recipe (find the toolkit's profile verb, call it with the connected_account_id, extract the canonical handle, persist via `connector_identity_set`). One run resolves every account; results persist to `infinity_meta` so future turns render `identity=\"...\"` automatically and no further calls are needed. Do this proactively the first time you see a missing identity.\n\n")
	}
	for _, slug := range slugs {
		accs := c.byToolkit[slug]
		if len(accs) == 0 {
			continue
		}
		name := accs[0].ToolkitName
		if name == "" {
			name = slug
		}
		b.WriteString(name)
		b.WriteString(" (")
		b.WriteString(slug)
		b.WriteString("):\n")
		for _, a := range accs {
			b.WriteString("  - id=")
			b.WriteString(a.ID)
			if a.Alias != "" {
				b.WriteString("  alias=\"")
				b.WriteString(a.Alias)
				b.WriteString("\"")
			}
			if a.IdentityHint != "" {
				b.WriteString("  identity=\"")
				b.WriteString(a.IdentityHint)
				b.WriteString("\"")
			}
			if a.Status != "" && strings.ToUpper(a.Status) != "ACTIVE" {
				b.WriteString("  status=")
				b.WriteString(a.Status)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("</connected_accounts>")
	return b.String()
}
