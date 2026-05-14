package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/connectors"
)

// /api/connectors/composio — thin proxy over Composio's REST API for the
// Studio /connectors page. Keeps the COMPOSIO_API_KEY server-side (never
// shipped to the browser) and gives us a single chokepoint to add caching,
// rate-limiting, or per-user scoping later when Infinity goes multi-tenant.
//
// Endpoints (all gated on COMPOSIO_API_KEY being set):
//
//	GET    /api/connectors/composio/toolkits?q=&limit=&cursor=
//	GET    /api/connectors/composio/connected
//	POST   /api/connectors/composio/connect           body: { toolkit_slug }
//	DELETE /api/connectors/composio/accounts/{id}
//
// All four pass through Composio's JSON response untouched so Studio can
// evolve with their schema without server-side re-shaping.

const composioAPIBase = "https://backend.composio.dev/api/v3"

// composioHTTP is the shared upstream client. Longer-than-default response
// timeout because Composio's catalog endpoint occasionally takes ~2s.
var composioHTTP = &http.Client{Timeout: 20 * time.Second}

// composioRESTKey prefers the workspace admin API key over the consumer
// key for REST calls. Composio's /api/v3/* endpoints (toolkit catalog,
// workspace-level connected_accounts) reject `x-consumer-api-key`
// outright — the consumer key is for the MCP gateway and per-user calls
// only. Both are read fresh each call so a Railway env swap takes effect
// without a restart.
//
// Returns (key, isAdmin). When isAdmin=false the only credential we have
// is the consumer key; the REST catalog browser will still 401, but the
// proxy stays consistent so a future workspace key drops in cleanly.
func composioRESTKey() (string, bool) {
	if v := strings.TrimSpace(os.Getenv("COMPOSIO_ADMIN_API_KEY")); v != "" {
		return v, true
	}
	return strings.TrimSpace(os.Getenv("COMPOSIO_API_KEY")), false
}

// proxyComposio forwards an authenticated request to Composio and copies
// the response body verbatim. Centralised so each handler stays a 3-liner.
func proxyComposio(w http.ResponseWriter, r *http.Request, method, path string, body io.Reader) {
	key, isAdmin := composioRESTKey()
	if key == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "no Composio key on core (set COMPOSIO_ADMIN_API_KEY for catalog browse, COMPOSIO_API_KEY is the MCP consumer key only)",
		})
		return
	}
	full := composioAPIBase + path
	req, err := http.NewRequestWithContext(r.Context(), method, full, body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Send the appropriate header for the key tier. Admin → x-api-key +
	// Bearer; consumer-only fallback → x-consumer-api-key (will 401 on
	// /api/v3/* but the page surfaces that clearly).
	if isAdmin {
		req.Header.Set("x-api-key", key)
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		req.Header.Set("x-consumer-api-key", key)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := composioHTTP.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "composio upstream unreachable: " + err.Error(),
		})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) handleComposioToolkits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	// Forward q / limit / cursor / category so the page can paginate and
	// search without us hand-rolling a query whitelist.
	q := url.Values{}
	for _, k := range []string{"q", "search", "limit", "cursor", "category", "sort_by", "sort_order", "is_local_toolkit", "managed_by"} {
		if v := strings.TrimSpace(r.URL.Query().Get(k)); v != "" {
			q.Set(k, v)
		}
	}
	path := "/toolkits"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	proxyComposio(w, r, http.MethodGet, path, nil)
}

func (s *Server) handleComposioConnected(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	q := url.Values{}
	for _, k := range []string{"limit", "cursor", "toolkit_slug", "status", "user_ids"} {
		if v := strings.TrimSpace(r.URL.Query().Get(k)); v != "" {
			q.Set(k, v)
		}
	}
	path := "/connected_accounts"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	proxyComposio(w, r, http.MethodGet, path, nil)
}

// handleComposioConnect kicks off a new OAuth/connect flow. Body shape:
//
//	{ "toolkit_slug": "github", "user_id": "personal", "auth_config_id": "<optional>" }
//
// `user_id` is the Composio entity identifier — pass distinct values to
// authorise the *same* toolkit under multiple identities (e.g.
// "personal" + "work" Gmail mailboxes). Each Connect creates a fresh
// connected_account row that surfaces in the activated list as its own
// entry, alias-editable from Studio.
//
// Composio returns a redirect URL the browser opens; once the boss
// finishes the vendor's OAuth screen Composio redirects back and the
// account appears under /connected_accounts on the next poll.
func (s *Server) handleComposioConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	var in map[string]any
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	slug, _ := in["toolkit_slug"].(string)
	if slug == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "toolkit_slug required"})
		return
	}
	userID, _ := in["user_id"].(string)
	if userID == "" {
		userID = "default"
	}
	aliasPending, _ := in["alias"].(string)

	key, isAdmin := composioRESTKey()
	if key == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "set COMPOSIO_ADMIN_API_KEY on core to initiate connections",
		})
		return
	}
	if !isAdmin {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "connection initiation requires the workspace admin key (COMPOSIO_ADMIN_API_KEY), not the consumer MCP key",
		})
		return
	}

	// Composio v3 split the old single-call /connected_accounts/initiate
	// flow into two steps. Step 1: locate (or create) an auth_config for
	// this toolkit. Step 2: POST to /connected_accounts/link with the
	// auth_config_id + user_id to receive the OAuth redirect URL.
	authConfigID, err := findOrCreateAuthConfig(r.Context(), key, isAdmin, slug)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "auth_config: " + err.Error()})
		return
	}

	linkBody, _ := json.Marshal(map[string]string{
		"auth_config_id": authConfigID,
		"user_id":        userID,
	})
	req, err := http.NewRequestWithContext(
		r.Context(), http.MethodPost,
		composioAPIBase+"/connected_accounts/link",
		strings.NewReader(string(linkBody)),
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	applyComposioAuth(req, key, isAdmin)

	resp, err := composioHTTP.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "composio upstream unreachable: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// Background cache refresh so the pending account shows up in the
	// activated list (status=INITIATED until the boss finishes OAuth).
	if s.connectors != nil {
		go func() {
			_ = s.connectors.Refresh(r.Context())
		}()
	}

	// Persist the alias against the new connected_account_id on success.
	if resp.StatusCode < 400 && aliasPending != "" && s.connectors != nil {
		var probe map[string]any
		if jerr := json.Unmarshal(body, &probe); jerr == nil {
			if id := extractConnectedAccountID(probe); id != "" {
				_ = s.connectors.SetAlias(r.Context(), id, aliasPending)
			}
		}
	}

	// Normalize the success shape to {redirect_url, id} so the FE keeps
	// its existing contract regardless of Composio's response field names.
	if resp.StatusCode < 400 {
		var src map[string]any
		if jerr := json.Unmarshal(body, &src); jerr == nil {
			out := map[string]any{}
			if v, ok := src["redirect_url"].(string); ok && v != "" {
				out["redirect_url"] = v
			}
			if id := extractConnectedAccountID(src); id != "" {
				out["id"] = id
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
	}

	// Error path — pass through Composio's body so the FE can surface
	// the vendor's actual message.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// applyComposioAuth sets the right credential headers on `req` given
// the key tier. Centralises the admin vs consumer branching so every
// upstream call uses the same shape.
func applyComposioAuth(req *http.Request, key string, isAdmin bool) {
	if isAdmin {
		req.Header.Set("x-api-key", key)
		req.Header.Set("Authorization", "Bearer "+key)
		return
	}
	req.Header.Set("x-consumer-api-key", key)
}

// findOrCreateAuthConfig returns an auth_config_id for the given
// toolkit slug. Prefers an existing config in the workspace; falls
// back to creating a Composio-managed OAuth config if none exists.
//
// Caching is not worth the lock contention here — Composio's list
// endpoint replies in well under a second and connect is a manual
// user action, not a hot path. If two concurrent connects race on
// the create step we end up with two equivalent configs, and the
// next list returns whichever; both are valid.
func findOrCreateAuthConfig(ctx context.Context, key string, isAdmin bool, slug string) (string, error) {
	// 1) List existing configs filtered by toolkit slug. Composio's
	//    response wraps each row in `{ "auth_config": {...} }` or
	//    flat depending on the endpoint — handle both.
	listURL := composioAPIBase + "/auth_configs?toolkit_slug=" + url.QueryEscape(slug) + "&limit=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return "", err
	}
	applyComposioAuth(req, key, isAdmin)
	resp, err := composioHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	listBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("auth_configs list %d: %s", resp.StatusCode, truncate(string(listBody), 300))
	}
	var list struct {
		Items []struct {
			AuthConfig struct {
				ID string `json:"id"`
			} `json:"auth_config"`
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listBody, &list); err == nil && len(list.Items) > 0 {
		if id := list.Items[0].AuthConfig.ID; id != "" {
			return id, nil
		}
		if id := list.Items[0].ID; id != "" {
			return id, nil
		}
	}

	// 2) None exist — create one using Composio-managed auth so the boss
	//    doesn't have to register an OAuth app per toolkit. For toolkits
	//    that don't support Composio-managed auth (rare), this will fail
	//    and the error message tells the boss to wire a custom config in
	//    the Composio dashboard.
	createBody, _ := json.Marshal(map[string]any{
		"toolkit":     map[string]string{"slug": slug},
		"auth_config": map[string]string{"type": "use_composio_managed_auth"},
	})
	creq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, composioAPIBase+"/auth_configs",
		strings.NewReader(string(createBody)),
	)
	if err != nil {
		return "", err
	}
	creq.Header.Set("Content-Type", "application/json")
	applyComposioAuth(creq, key, isAdmin)
	cresp, err := composioHTTP.Do(creq)
	if err != nil {
		return "", err
	}
	defer cresp.Body.Close()
	cbody, _ := io.ReadAll(cresp.Body)
	if cresp.StatusCode >= 400 {
		return "", fmt.Errorf("auth_config create %d: %s", cresp.StatusCode, truncate(string(cbody), 300))
	}
	var created struct {
		AuthConfig struct {
			ID string `json:"id"`
		} `json:"auth_config"`
		ID string `json:"id"`
	}
	if err := json.Unmarshal(cbody, &created); err != nil {
		return "", fmt.Errorf("auth_config create decode: %w", err)
	}
	if id := created.AuthConfig.ID; id != "" {
		return id, nil
	}
	if id := created.ID; id != "" {
		return id, nil
	}
	return "", fmt.Errorf("auth_config create: missing id in response: %s", truncate(string(cbody), 300))
}


// extractConnectedAccountID pulls the new connected_account id out of
// Composio's initiate response. The field can live under different
// names depending on toolkit + flow; we try the common candidates.
func extractConnectedAccountID(body map[string]any) string {
	for _, key := range []string{"id", "connected_account_id", "connection_id"} {
		if v, ok := body[key].(string); ok && v != "" {
			return v
		}
	}
	if nested, ok := body["connection_data"].(map[string]any); ok {
		for _, key := range []string{"id", "connected_account_id"} {
			if v, ok := nested[key].(string); ok && v != "" {
				return v
			}
		}
	}
	return ""
}

func (s *Server) handleComposioAccount(w http.ResponseWriter, r *http.Request) {
	// Path: /api/connectors/composio/accounts/{id}
	id := strings.TrimPrefix(r.URL.Path, "/api/connectors/composio/accounts/")
	id = strings.Trim(id, "/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account id required"})
		return
	}
	switch r.Method {
	case http.MethodDelete:
		proxyComposio(w, r, http.MethodDelete, "/connected_accounts/"+url.PathEscape(id), nil)
		if s.connectors != nil {
			go func() { _ = s.connectors.Refresh(r.Context()) }()
		}
	case http.MethodGet:
		proxyComposio(w, r, http.MethodGet, "/connected_accounts/"+url.PathEscape(id), nil)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": fmt.Sprintf("%s not allowed", r.Method)})
	}
}

// handleComposioAliases is the alias CRUD surface. Used by Studio's
// inline editor on each connected-account row. The alias map persists
// in infinity_meta as a single JSON blob keyed by Composio
// connected_account id — see connectors.MetaKey for the layout.
//
//	GET    /api/connectors/composio/aliases
//	  → { "aliases": { "ca_abc": "personal", "ca_def": "work" } }
//
//	PUT    /api/connectors/composio/aliases
//	  body: { "account_id": "ca_abc", "alias": "personal" }
//	  empty alias deletes the entry.
func (s *Server) handleComposioAliases(w http.ResponseWriter, r *http.Request) {
	if s.connectors == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "connectors cache not configured"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"aliases": s.connectors.Aliases()})
	case http.MethodPut, http.MethodPost:
		var in struct {
			AccountID string `json:"account_id"`
			Alias     string `json:"alias"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		if strings.TrimSpace(in.AccountID) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_id required"})
			return
		}
		if err := s.connectors.SetAlias(r.Context(), in.AccountID, in.Alias); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "account_id": in.AccountID, "alias": in.Alias})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": fmt.Sprintf("%s not allowed", r.Method)})
	}
}

// handleComposioCacheStatus exposes the cache health for diagnostics.
// Studio can show "last refreshed Xs ago" + error state without
// re-implementing the math.
func (s *Server) handleComposioCacheStatus(w http.ResponseWriter, r *http.Request) {
	if s.connectors == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "connectors cache not configured"})
		return
	}
	writeJSON(w, http.StatusOK, s.connectors.Status())
}

// composioCache is the package-level placeholder while we wait for the
// Server struct field. Real wiring lives on s.connectors set in server.go.
var _ = (*connectors.Cache)(nil)
