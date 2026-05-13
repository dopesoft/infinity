package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// OAuth API for the ChatGPT-subscription provider (openai_oauth).
//
// The end-to-end flow is intentionally paste-based so it works from any
// device (phone first), without needing a localhost listener or a browser
// extension:
//
//   1. Studio POSTs /api/auth/openai/start. Core generates a PKCE verifier
//      + state, persists them in mem_oauth_sessions (15-min TTL), and
//      returns the authorize URL.
//   2. The user opens the URL, logs in to ChatGPT in their browser.
//      ChatGPT redirects to http://localhost:1455/auth/callback?code=...
//      &state=...; the browser shows "can't reach the page" but the URL
//      is visible in the address bar.
//   3. The user copies that URL (or just the code) and pastes it back into
//      Studio's Connect ChatGPT dialog, which calls /api/auth/openai/exchange
//      with the code + state. Core looks up the verifier, exchanges the code
//      for tokens, and upserts mem_provider_tokens.
//   4. From this point on the openai_oauth provider reads the token row on
//      every inference call and refreshes in place before expiry.
//
// We don't need to know the redirect_uri on the wire — it's tied to the
// public Codex CLI client_id and constant across sessions.

// oauthProvider returns the live OAuth-backed provider when LLM_PROVIDER
// is openai_oauth and the loop has it wired. When the env points at a
// different provider but the boss wants to pre-connect ChatGPT (so they
// can flip LLM_PROVIDER later without re-logging in), we still need a
// provider instance for the PKCE constants — we build a transient one
// against the same pool/store. The transient never streams.
func (s *Server) oauthProvider() (*llm.OpenAIOAuth, error) {
	if s == nil {
		return nil, http.ErrAbortHandler
	}
	if s.loop != nil {
		if p, ok := s.loop.Provider().(*llm.OpenAIOAuth); ok {
			return p, nil
		}
	}
	if s.pool == nil {
		return nil, errNoPool
	}
	return llm.NewOpenAIOAuth(llm.NewOAuthStore(s.pool), ""), nil
}

var errNoPool = &serverError{Status: http.StatusServiceUnavailable, Msg: "database pool not configured"}

type serverError struct {
	Status int
	Msg    string
}

func (e *serverError) Error() string { return e.Msg }

// POST /api/auth/openai/start
//
// Request: empty body.
// Response: { state, authorize_url, redirect_uri, expires_at }
func (s *Server) handleOpenAIOAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p, err := s.oauthProvider()
	if err != nil {
		writeOAuthErr(w, err)
		return
	}

	verifier, challenge, err := llm.GeneratePKCE()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	state, err := llm.RandomState()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	store := p.Store()
	expiresAt := time.Now().Add(15 * time.Minute)
	if err := store.CreateSession(ctx, llm.OAuthSession{
		State:        state,
		Provider:     p.Name(),
		CodeVerifier: verifier,
		RedirectURI:  p.RedirectURI(),
		ExpiresAt:    expiresAt,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"state":         state,
		"authorize_url": p.BuildAuthorizeURL(state, challenge),
		"redirect_uri":  p.RedirectURI(),
		"expires_at":    expiresAt.UTC().Format(time.RFC3339),
	})
}

// POST /api/auth/openai/exchange
//
// Request: one of:
//   { "code": "...", "state": "..." }
//   { "callback_url": "http://localhost:1455/auth/callback?code=...&state=..." }
// Response: { connected: true, account_email, account_id, expires_at }
func (s *Server) handleOpenAIOAuthExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p, err := s.oauthProvider()
	if err != nil {
		writeOAuthErr(w, err)
		return
	}

	var body struct {
		Code        string `json:"code"`
		State       string `json:"state"`
		CallbackURL string `json:"callback_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	code, state := body.Code, body.State

	// Accept a pasted-URL form too — common when the user just copies the
	// "this site can't be reached" address bar wholesale instead of fishing
	// out the query params.
	if (code == "" || state == "") && body.CallbackURL != "" {
		if u, err := url.Parse(strings.TrimSpace(body.CallbackURL)); err == nil {
			if code == "" {
				code = u.Query().Get("code")
			}
			if state == "" {
				state = u.Query().Get("state")
			}
		}
	}
	if code == "" || state == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "code and state required (paste either {code,state} or the full callback URL)",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	sess, err := p.Store().ConsumeSession(ctx, state)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	tok, err := p.ExchangeCode(ctx, code, sess.CodeVerifier, sess.RedirectURI)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, oauthStatusBody(tok, true))
}

// GET /api/auth/openai/status — connected? when does it expire? when was it
// last refreshed? Studio polls this to render the Connect / Reconnect badge.
func (s *Server) handleOpenAIOAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p, err := s.oauthProvider()
	if err != nil {
		writeOAuthErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	tok, err := p.Store().GetToken(ctx, p.Name())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
		return
	}
	writeJSON(w, http.StatusOK, oauthStatusBody(tok, true))
}

// POST /api/auth/openai/disconnect — drops the stored credential row. The
// next inference call will fail with "no oauth token stored" until the
// user reconnects.
func (s *Server) handleOpenAIOAuthDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p, err := s.oauthProvider()
	if err != nil {
		writeOAuthErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := p.Store().DeleteToken(ctx, p.Name()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": false})
}

func oauthStatusBody(tok llm.OAuthToken, connected bool) map[string]any {
	out := map[string]any{
		"connected":     connected,
		"provider":      tok.Provider,
		"account_id":    tok.AccountID,
		"account_email": tok.AccountEmail,
		"scope":         tok.Scope,
	}
	if tok.ExpiresAt != nil {
		out["expires_at"] = tok.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if !tok.LastRefreshed.IsZero() {
		out["last_refreshed"] = tok.LastRefreshed.UTC().Format(time.RFC3339)
	}
	return out
}

func writeOAuthErr(w http.ResponseWriter, err error) {
	if se, ok := err.(*serverError); ok {
		writeJSON(w, se.Status, map[string]string{"error": se.Msg})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}
