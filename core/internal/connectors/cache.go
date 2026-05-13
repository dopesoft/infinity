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
	lastRefresh time.Time
	lastErr     string

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
	accounts, err := c.loadAccounts(ctx)
	if err != nil {
		c.recordErr("composio list: " + err.Error())
		return err
	}
	// Apply alias overlay before storing.
	for _, a := range accounts {
		if v, ok := aliases[a.ID]; ok {
			a.Alias = v
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
	c.lastRefresh = time.Now()
	c.lastErr = ""
	c.mu.Unlock()
	return nil
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

	var b strings.Builder
	b.WriteString("<connected_accounts>\n")
	b.WriteString("The boss has authenticated the following SaaS accounts. When you call a tool that supports per-account routing, pass the matching `connected_account_id`. Prefer the alias the boss assigned over the raw identity hint.\n\n")
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
