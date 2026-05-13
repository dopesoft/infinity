package server

import (
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
	if _, ok := in["toolkit_slug"]; !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "toolkit_slug required"})
		return
	}
	// Pull off the optional alias and stash it once we know the new
	// connected_account_id (we re-apply after a refresh below). The
	// Composio API doesn't accept an alias param, so we don't pass it.
	aliasPending, _ := in["alias"].(string)
	delete(in, "alias")

	payload, err := json.Marshal(in)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Proxy upstream first, but capture the response so we can refresh
	// the cache and apply the pending alias once the connected_account_id
	// is known.
	key, isAdmin := composioRESTKey()
	if key == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "no Composio key on core (set COMPOSIO_ADMIN_API_KEY for catalog browse)",
		})
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, composioAPIBase+"/connected_accounts/initiate", strings.NewReader(string(payload)))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if isAdmin {
		req.Header.Set("x-api-key", key)
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		req.Header.Set("x-consumer-api-key", key)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := composioHTTP.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "composio upstream unreachable: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// Refresh the cache so the new pending account shows up immediately
	// in Studio's active list (status will be INITIATED until the boss
	// finishes the OAuth dance — the next refresh tick flips to ACTIVE).
	if s.connectors != nil {
		go func() {
			_ = s.connectors.Refresh(r.Context())
		}()
	}

	// If the caller pre-supplied an alias and we can pluck the new
	// connected_account_id out of the response, persist it. Best-effort
	// — Composio's initiate response shape varies; we look for the
	// canonical fields.
	if aliasPending != "" && s.connectors != nil {
		var probe map[string]any
		if jerr := json.Unmarshal(body, &probe); jerr == nil {
			if id := extractConnectedAccountID(probe); id != "" {
				_ = s.connectors.SetAlias(r.Context(), id, aliasPending)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
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
