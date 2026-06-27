package llm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// unknownOAIEventOnce tracks event types we've already logged so a
// long-running stream doesn't drown the logs in duplicate warnings.
// Stdout (info-level) on first sighting, silent thereafter.
var (
	unknownOAIEventMu   sync.Mutex
	unknownOAIEventSeen = map[string]struct{}{}
	unknownOAIEventLog  = log.New(os.Stdout, "", log.LstdFlags)
)

// logUnknownOAIEvent prints the SSE event type the first time we
// encounter it. Useful when OpenAI ships a new reasoning-event variant
// and the ThinkingBlock goes silent - the next deploy's logs show
// exactly which event name we missed so the handler can be extended.
func logUnknownOAIEvent(t string) {
	if t == "" {
		return
	}
	unknownOAIEventMu.Lock()
	_, seen := unknownOAIEventSeen[t]
	if !seen {
		unknownOAIEventSeen[t] = struct{}{}
	}
	unknownOAIEventMu.Unlock()
	if !seen {
		unknownOAIEventLog.Printf("openai_oauth: unhandled stream event %q", t)
	}
}

// debugLogSeenEvent logs EVERY distinct event type once per process -
// handled and unhandled alike - so we can verify the reasoning stream
// shape from the prod logs without instrumenting the call site every
// time. Gated on INFINITY_OAI_DEBUG_EVENTS=true so the steady state
// stays quiet.
func debugLogSeenEvent(t string) {
	if t == "" {
		return
	}
	if os.Getenv("INFINITY_OAI_DEBUG_EVENTS") != "true" {
		return
	}
	unknownOAIEventMu.Lock()
	key := "seen:" + t
	_, seen := unknownOAIEventSeen[key]
	if !seen {
		unknownOAIEventSeen[key] = struct{}{}
	}
	unknownOAIEventMu.Unlock()
	if !seen {
		unknownOAIEventLog.Printf("openai_oauth: stream event seen %q", t)
	}
}

// OpenAIOAuth is an LLM provider that authenticates via the standard
// "Sign in with ChatGPT" PKCE flow (the same one Codex CLI uses) and routes
// inference through `chatgpt.com/backend-api/codex/responses` so requests
// consume the user's ChatGPT Plus/Pro subscription quota instead of pay-per
// -token API credit.
//
// The token lifecycle lives in OAuthStore (pgx-backed). On every Stream call
// we read the persisted row, refresh if we're within RefreshLeadTime of
// expiry, and use the access_token as a bearer. Refresh rotates the refresh
// token, so the store always holds the most recent pair.
//
// Wire protocol notes
//   - Endpoint and auth headers follow the same shape Codex CLI uses. OpenAI
//     does not publish these as a stable public contract, so the chatgpt.com
//     backend may change - when it does, update the request shape here and
//     bump the user-agent so we can correlate failures in the audit log.
//   - The body uses OpenAI's Responses API JSON. Streamed events arrive as
//     SSE with `event: <name>` + `data: <json>` pairs; we parse the small
//     subset we care about and discard the rest.
type OpenAIOAuth struct {
	store       *OAuthStore
	model       string
	httpClient  *http.Client
	apiBase     string
	authBase    string
	clientID    string
	scopes      string
	redirectURI string
	refreshLead time.Duration

	// refreshMu serializes refresh attempts so a burst of concurrent turns
	// doesn't trigger N parallel /oauth/token calls that all rotate the
	// refresh token and invalidate each other.
	refreshMu sync.Mutex
}

const (
	// Codex CLI's public OAuth client. Override via OPENAI_OAUTH_CLIENT_ID
	// when OpenAI rotates this identifier (rare but it has happened).
	defaultOpenAIClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultOpenAIAuthBase = "https://auth.openai.com"
	defaultOpenAIAPIBase  = "https://chatgpt.com/backend-api/codex"
	// Scopes must include the connectors scopes Codex CLI requests -
	// without them the issuer routes you to the platform project picker
	// instead of the subscription-org consent screen.
	defaultOpenAIScopes      = "openid profile email offline_access api.connectors.read api.connectors.invoke"
	defaultOpenAIRedirectURI = "http://localhost:1455/auth/callback"
	defaultOpenAIRefreshLead = 2 * time.Minute
)

func NewOpenAIOAuth(store *OAuthStore, model string) *OpenAIOAuth {
	if model == "" {
		// Default to the Codex roster - ChatGPT-account OAuth (the
		// subscription path) rejects plain "gpt-5" with
		//   The 'gpt-5' model is not supported when using Codex with
		//   a ChatGPT account.
		// gpt-5-codex is the canonical Codex CLI default and is what
		// the subscription plan actually exposes. Override via
		// LLM_MODEL_OPENAI_OAUTH if you want the smaller codex-mini.
		model = "gpt-5-codex"
	}
	return &OpenAIOAuth{
		store:       store,
		model:       model,
		httpClient:  &http.Client{Timeout: 0}, // streaming - no overall timeout
		apiBase:     envOr("OPENAI_OAUTH_API_BASE", defaultOpenAIAPIBase),
		authBase:    envOr("OPENAI_OAUTH_AUTH_BASE", defaultOpenAIAuthBase),
		clientID:    envOr("OPENAI_OAUTH_CLIENT_ID", defaultOpenAIClientID),
		scopes:      envOr("OPENAI_OAUTH_SCOPES", defaultOpenAIScopes),
		redirectURI: envOr("OPENAI_OAUTH_REDIRECT_URI", defaultOpenAIRedirectURI),
		refreshLead: defaultOpenAIRefreshLead,
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func (o *OpenAIOAuth) Name() string  { return "openai_oauth" }
func (o *OpenAIOAuth) Model() string { return o.model }

// Store exposes the underlying token store so the HTTP layer can read/write
// without needing its own pgx pool reference.
func (o *OpenAIOAuth) Store() *OAuthStore { return o.store }

// ClientID / AuthBase / RedirectURI / Scopes / APIBase let the HTTP layer
// build the authorize URL with the same constants the provider uses for
// refresh - keeps the OAuth contract in one place.
func (o *OpenAIOAuth) ClientID() string    { return o.clientID }
func (o *OpenAIOAuth) AuthBase() string    { return o.authBase }
func (o *OpenAIOAuth) APIBase() string     { return o.apiBase }
func (o *OpenAIOAuth) RedirectURI() string { return o.redirectURI }
func (o *OpenAIOAuth) Scopes() string      { return o.scopes }

// --- PKCE helpers (shared with the HTTP start handler) ----------------------

// GeneratePKCE returns a (verifier, challenge) pair where the challenge is
// the URL-safe base64-encoded SHA256 of the verifier - the S256 method.
func GeneratePKCE() (verifier, challenge string, err error) {
	buf := make([]byte, 64)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// RandomState returns a URL-safe random string suitable for OAuth `state`.
func RandomState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// BuildAuthorizeURL returns the URL the user should visit in their browser.
//
// The `codex_cli_simplified_flow` + `id_token_add_organizations` flags are
// the bits that make OpenAI skip its platform project-picker step and
// instead bind the resulting token to the user's ChatGPT subscription org -
// so the issued access token routes to chatgpt.com/backend-api/codex
// (subscription quota) rather than api.openai.com (pay-per-token). Codex
// CLI sends both unconditionally; omitting them is what triggers the
// "choose a project" page some users have hit on this flow.
func (o *OpenAIOAuth) BuildAuthorizeURL(state, challenge string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", o.clientID)
	q.Set("redirect_uri", o.redirectURI)
	q.Set("scope", o.scopes)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	return fmt.Sprintf("%s/oauth/authorize?%s", strings.TrimRight(o.authBase, "/"), q.Encode())
}

// ExchangeCode swaps an authorization code for tokens using the PKCE verifier.
// On success the token row is upserted into the store.
func (o *OpenAIOAuth) ExchangeCode(ctx context.Context, code, verifier, redirectURI string) (OAuthToken, error) {
	if redirectURI == "" {
		redirectURI = o.redirectURI
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", o.clientID)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", redirectURI)
	tok, err := o.tokenRequest(ctx, form)
	if err != nil {
		return OAuthToken{}, err
	}
	if err := o.store.UpsertToken(ctx, tok); err != nil {
		return tok, fmt.Errorf("persist token: %w", err)
	}
	return tok, nil
}

// refreshIfNeeded returns a fresh access token, refreshing in place when the
// stored access_token is within o.refreshLead of expiry. Concurrent callers
// serialize on refreshMu so we never rotate the refresh token twice in
// parallel.
func (o *OpenAIOAuth) refreshIfNeeded(ctx context.Context) (OAuthToken, error) {
	tok, err := o.store.GetToken(ctx, o.Name())
	if err != nil {
		return OAuthToken{}, err
	}
	if tok.ExpiresAt == nil || time.Until(*tok.ExpiresAt) > o.refreshLead {
		return tok, nil
	}
	if tok.RefreshToken == "" {
		return tok, errors.New("openai_oauth: token expired and no refresh_token stored - reconnect ChatGPT")
	}

	o.refreshMu.Lock()
	defer o.refreshMu.Unlock()

	// Re-check under lock - another goroutine may have refreshed while we
	// were waiting for the mutex.
	tok, err = o.store.GetToken(ctx, o.Name())
	if err != nil {
		return OAuthToken{}, err
	}
	if tok.ExpiresAt != nil && time.Until(*tok.ExpiresAt) > o.refreshLead {
		return tok, nil
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", o.clientID)
	form.Set("refresh_token", tok.RefreshToken)
	form.Set("scope", o.scopes)
	refreshed, err := o.tokenRequest(ctx, form)
	if err != nil {
		return tok, fmt.Errorf("openai_oauth refresh: %w", err)
	}
	// Carry over account fields the refresh response may omit.
	if refreshed.AccountID == "" {
		refreshed.AccountID = tok.AccountID
	}
	if refreshed.AccountEmail == "" {
		refreshed.AccountEmail = tok.AccountEmail
	}
	if err := o.store.UpsertToken(ctx, refreshed); err != nil {
		return tok, fmt.Errorf("openai_oauth persist refresh: %w", err)
	}
	return refreshed, nil
}

// tokenRequest POSTs to /oauth/token and parses the response into an
// OAuthToken. Identity claims (account_id, email) are extracted from the
// id_token when present.
func (o *OpenAIOAuth) tokenRequest(ctx context.Context, form url.Values) (OAuthToken, error) {
	endpoint := strings.TrimRight(o.authBase, "/") + "/oauth/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return OAuthToken{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return OAuthToken{}, fmt.Errorf("oauth token: status=%d body=%s", resp.StatusCode, truncateOAuth(string(body), 400))
	}

	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return OAuthToken{}, fmt.Errorf("decode token response: %w", err)
	}

	tok := OAuthToken{
		Provider:     o.Name(),
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		IDToken:      raw.IDToken,
		TokenType:    raw.TokenType,
		Scope:        raw.Scope,
	}
	if raw.ExpiresIn > 0 {
		exp := time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second)
		tok.ExpiresAt = &exp
	}
	if sub, email := decodeIDTokenClaims(raw.IDToken); sub != "" || email != "" {
		tok.AccountID = sub
		tok.AccountEmail = email
	}
	if tok.TokenType == "" {
		tok.TokenType = "Bearer"
	}
	return tok, nil
}

// decodeIDTokenClaims pulls the `sub` and `email` claims out of a JWT id_token
// without verifying signatures - we only use these for identity display in
// Studio and as the chatgpt-account-id header. The token comes straight from
// the OAuth response over TLS, so signature verification adds no security
// here beyond what TLS already gave us.
func decodeIDTokenClaims(idToken string) (sub, email string) {
	if idToken == "" {
		return "", ""
	}
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some providers pad the segment - retry tolerant.
		if p, perr := base64.URLEncoding.DecodeString(parts[1] + strings.Repeat("=", (4-len(parts[1])%4)%4)); perr == nil {
			payload = p
		} else {
			return "", ""
		}
	}
	var claims struct {
		Sub              string `json:"sub"`
		Email            string `json:"email"`
		ChatGPTAccountID string `json:"https://api.openai.com/auth/chatgpt_account_id"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", ""
	}
	if claims.ChatGPTAccountID != "" {
		return claims.ChatGPTAccountID, claims.Email
	}
	return claims.Sub, claims.Email
}

// --- Streaming inference ----------------------------------------------------

func (o *OpenAIOAuth) Stream(
	ctx context.Context,
	model string,
	system string,
	messages []Message,
	tools []ToolDef,
	out chan<- StreamEvent,
) (Response, error) {
	return o.StreamCached(ctx, model, SystemPrompt{Stable: system}, messages, tools, out)
}

// StreamCached renders the system stable-first into the Responses API
// `instructions` field and sets `prompt_cache_key` so Codex's automatic
// caching hits the stable prefix across a session's turns.
func (o *OpenAIOAuth) StreamCached(
	ctx context.Context,
	model string,
	sys SystemPrompt,
	messages []Message,
	tools []ToolDef,
	out chan<- StreamEvent,
) (Response, error) {
	var resp Response
	system := sys.Render()
	cacheKey := CacheKeyFromContext(ctx)
	// steal C: per-turn reasoning effort. ctx hint wins; else the env fallback.
	// dropEffort is flipped by the 400 handler below if the backend rejects the
	// level, so we retry once with effort omitted instead of tanking the turn.
	ctxEffort := string(EffortFromContext(ctx))

	tok, err := o.refreshIfNeeded(ctx)
	if err != nil {
		emit(out, StreamEvent{Kind: StreamError, Err: err.Error()})
		return resp, err
	}

	// Respect the configured model. The boss's Settings choice (or the
	// per-call override from a sub-agent) is the truth. We only translate tier
	// *nicknames* like "haiku" / "sonnet"; real model ids pass through to
	// OpenAI untouched. The retry-on-400 below catches genuine "model not
	// supported" errors and falls back once.
	effectiveModel := o.model
	if model != "" {
		if nickname := tierNicknameToCodex(model); nickname != "" {
			effectiveModel = nickname
		} else {
			effectiveModel = model
		}
	}

	// Reliability: a single transient provider hiccup — HTTP 5xx/429/529, a
	// dropped connection, or an in-stream `server_error` event — must NOT tank
	// the whole turn (the bug that failed the boss's inbox-triage cron on a
	// `server_error` at sequence_number:1). We re-issue with exponential
	// backoff as long as NOTHING has streamed to the caller yet, so a retry can
	// never duplicate output. Deterministic code, never model-driven. Tunables:
	// LLM_OPENAI_OAUTH_RETRIES (default 3 retries → 4 attempts) and
	// LLM_OPENAI_OAUTH_RETRY_BACKOFF (default 600ms, doubling, capped 8s).
	maxAttempts := transientMaxAttempts()
	backoff := transientBackoffBase()
	triedModelFallback := false
	triedEffortDrop := false
	dropEffort := false
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Resolve the effort to send THIS attempt: ctx hint > env fallback, unless
		// a prior 400 told us the backend rejects it (dropEffort -> omit).
		effortToSend := ctxEffort
		if effortToSend == "" {
			effortToSend = strings.TrimSpace(os.Getenv("INFINITY_OPENAI_REASONING_EFFORT"))
		}
		if dropEffort {
			effortToSend = ""
		}
		httpResp, attemptErr := o.attemptStream(ctx, tok, effectiveModel, system, cacheKey, effortToSend, messages, tools)

		// Network-level failure (no usable response).
		if attemptErr != nil {
			lastErr = attemptErr
			if attempt < maxAttempts && isTransientNetErr(attemptErr) && ctx.Err() == nil {
				unknownOAIEventLog.Printf("openai_oauth: transient network error, retry %d/%d after %s: %v",
					attempt, maxAttempts-1, backoff, attemptErr)
				if !sleepBackoff(ctx, &backoff) {
					break
				}
				continue
			}
			emit(out, StreamEvent{Kind: StreamError, Err: attemptErr.Error()})
			return resp, attemptErr
		}

		// Self-heal: Codex rejected the model id (400). Retry ONCE with a
		// known-served fallback — orthogonal to transient retry (a different
		// model, not the same request again). Covers a per-call override the
		// plan doesn't expose AND the configured default itself being rejected
		// (oauthFallbackModel(), override via LLM_OPENAI_OAUTH_FALLBACK_MODEL).
		if httpResp.StatusCode == 400 {
			raw, _ := io.ReadAll(httpResp.Body)
			httpResp.Body.Close()
			bodyStr := string(raw)
			if !triedModelFallback && looksLikeModelRejection(bodyStr) {
				fallback := o.model
				if fallback == "" || fallback == effectiveModel {
					fallback = oauthFallbackModel()
				}
				if fallback != "" && fallback != effectiveModel {
					unknownOAIEventLog.Printf("openai_oauth: model %q rejected, retrying with %q (reason: %s)",
						effectiveModel, fallback, truncateOAuth(bodyStr, 200))
					effectiveModel = fallback
					triedModelFallback = true
					continue
				}
			}
			// steal C: the Codex backend rejected the reasoning.effort value (its
			// accepted enum is model-dependent and not a published contract). Retry
			// ONCE with effort omitted (model default) rather than failing the turn.
			// Gated on a body that specifically implicates the effort/reasoning
			// param so an unrelated 400 still surfaces as a real error (never-hide).
			if !triedEffortDrop && effortToSend != "" && looksLikeEffortRejection(bodyStr) {
				unknownOAIEventLog.Printf("openai_oauth: reasoning.effort %q rejected, retrying with effort omitted (reason: %s)",
					effortToSend, truncateOAuth(bodyStr, 200))
				dropEffort = true
				triedEffortDrop = true
				continue
			}
			statusErr := fmt.Errorf("openai_oauth: status=400 body=%s", truncateOAuth(bodyStr, 400))
			emit(out, StreamEvent{Kind: StreamError, Err: statusErr.Error()})
			return resp, statusErr
		}

		// Transient HTTP status — retry the same request after backoff.
		if httpResp.StatusCode/100 != 2 {
			raw, _ := io.ReadAll(httpResp.Body)
			httpResp.Body.Close()
			statusErr := fmt.Errorf("openai_oauth: status=%d body=%s", httpResp.StatusCode, truncateOAuth(string(raw), 400))
			lastErr = statusErr
			if attempt < maxAttempts && isTransientStatus(httpResp.StatusCode) && ctx.Err() == nil {
				unknownOAIEventLog.Printf("openai_oauth: transient status %d, retry %d/%d after %s",
					httpResp.StatusCode, attempt, maxAttempts-1, backoff)
				if !sleepBackoff(ctx, &backoff) {
					break
				}
				continue
			}
			emit(out, StreamEvent{Kind: StreamError, Err: statusErr.Error()})
			return resp, statusErr
		}

		// 2xx — consume the SSE stream.
		r, sErr := readResponsesSSE(httpResp.Body, out)
		httpResp.Body.Close()
		if sErr != nil {
			var rt *retryableStreamError
			if errors.As(sErr, &rt) {
				// Clean transient failure mid-stream (no content emitted yet).
				lastErr = errors.New(rt.msg)
				if attempt < maxAttempts && ctx.Err() == nil {
					unknownOAIEventLog.Printf("openai_oauth: transient stream error, retry %d/%d after %s: %s",
						attempt, maxAttempts-1, backoff, truncateOAuth(rt.msg, 160))
					if !sleepBackoff(ctx, &backoff) {
						break
					}
					continue
				}
				// Retries exhausted: emit the terminal events readResponsesSSE
				// deferred to us so the caller's stream still closes cleanly.
				emit(out, StreamEvent{Kind: StreamError, Err: rt.msg})
				emit(out, StreamEvent{Kind: StreamComplete, StopReason: "error"})
				return r, lastErr
			}
			// Non-retryable stream error: readResponsesSSE already emitted the
			// terminal events; pass it straight through.
			return r, sErr
		}
		return r, nil
	}

	// Fell out of the loop — typically ctx cancelled during a backoff sleep.
	if lastErr == nil {
		lastErr = errors.New("openai_oauth: exhausted transient retries")
	}
	emit(out, StreamEvent{Kind: StreamError, Err: lastErr.Error()})
	emit(out, StreamEvent{Kind: StreamComplete, StopReason: "error"})
	return resp, lastErr
}

// --- Transient-failure retry helpers ----------------------------------------

// retryableStreamError marks an in-stream provider error that occurred BEFORE
// any content was emitted, so Stream can re-issue the request without
// duplicating output. readResponsesSSE returns this instead of emitting
// terminal events; Stream owns the retry-or-surface decision.
type retryableStreamError struct{ msg string }

func (e *retryableStreamError) Error() string { return e.msg }

// transientMaxAttempts is total attempts (1 + retries). LLM_OPENAI_OAUTH_RETRIES
// sets the retry count; default 3 → 4 attempts.
func transientMaxAttempts() int {
	if v := strings.TrimSpace(os.Getenv("LLM_OPENAI_OAUTH_RETRIES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n + 1
		}
	}
	return 4
}

// transientBackoffBase is the first backoff delay; doubles each retry, capped
// at maxTransientBackoff. Override via LLM_OPENAI_OAUTH_RETRY_BACKOFF.
func transientBackoffBase() time.Duration {
	if v := strings.TrimSpace(os.Getenv("LLM_OPENAI_OAUTH_RETRY_BACKOFF")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 600 * time.Millisecond
}

const maxTransientBackoff = 8 * time.Second

// sleepBackoff waits *d (then doubles it, capped) unless ctx is cancelled
// first. Returns false if ctx ended during the wait so the caller stops.
func sleepBackoff(ctx context.Context, d *time.Duration) bool {
	t := time.NewTimer(*d)
	defer t.Stop()
	select {
	case <-t.C:
		if *d *= 2; *d > maxTransientBackoff {
			*d = maxTransientBackoff
		}
		return true
	case <-ctx.Done():
		return false
	}
}

// isTransientStatus reports whether an HTTP status warrants a retry: rate
// limits, overload, and 5xx server errors. 4xx (other than 408/425/429) are
// client errors we should NOT hammer.
func isTransientStatus(code int) bool {
	switch code {
	case 408, 425, 429, 500, 502, 503, 504, 529:
		return true
	}
	return false
}

// isTransientResponsesError matches the in-stream error payloads OpenAI emits
// for provider-side hiccups — the ones that self-heal on a retry.
func isTransientResponsesError(raw string) bool {
	s := strings.ToLower(raw)
	for _, needle := range []string{
		"server_error", "server had an error", "internal error", "internal_error",
		"overloaded", "rate_limit", "rate limit", "temporarily", "try again",
		"timeout", "timed out", "503", "502", "500",
	} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// isTransientNetErr matches connection-level blips worth retrying. Caller
// cancellation / deadline is deliberately NOT retryable — that's the loop's
// own budget, not a provider problem.
func isTransientNetErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, needle := range []string{
		"connection reset", "broken pipe", "unexpected eof", "connection refused",
		"i/o timeout", "tls handshake timeout", "server closed", "use of closed",
		"http2: ", "stream error", "goaway", "eof",
	} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// attemptStream issues a single /responses request for the given model
// and returns the raw HTTP response. Pulled out of Stream so the caller
// can inspect the status, decide whether to retry with a different
// model, and reissue without duplicating header / payload assembly.
func (o *OpenAIOAuth) attemptStream(
	ctx context.Context,
	tok OAuthToken,
	model, system, cacheKey, effort string,
	messages []Message,
	tools []ToolDef,
) (*http.Response, error) {
	body := buildResponsesRequest(model, system, cacheKey, effort, messages, tools)
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(o.apiBase, "/") + "/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	if tok.AccountID != "" {
		req.Header.Set("chatgpt-account-id", tok.AccountID)
	}
	req.Header.Set("User-Agent", "infinity-core/1 (openai_oauth)")
	return o.httpClient.Do(req)
}

// CompactContext uses the stateless Responses compaction endpoint supported by
// the ChatGPT Codex backend. Unlike previous_response_id, this does not require
// store=true (the backend rejects store=true for subscription OAuth) and returns
// the canonical next input window to pass back on later /responses calls.
func (o *OpenAIOAuth) CompactContext(ctx context.Context, model string, messages []Message) ([]Message, TokenUsage, error) {
	tok, err := o.refreshIfNeeded(ctx)
	if err != nil {
		return nil, TokenUsage{}, err
	}
	effectiveModel := o.model
	if model != "" {
		if nickname := tierNicknameToCodex(model); nickname != "" {
			effectiveModel = nickname
		} else {
			effectiveModel = model
		}
	}
	body := map[string]any{
		"model": effectiveModel,
		"input": buildResponsesInput(messages),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, TokenUsage{}, err
	}
	endpoint := strings.TrimRight(o.apiBase, "/") + "/responses/compact"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, TokenUsage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	if tok.AccountID != "" {
		req.Header.Set("chatgpt-account-id", tok.AccountID)
	}
	req.Header.Set("User-Agent", "infinity-core/1 (openai_oauth)")

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, TokenUsage{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, TokenUsage{}, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, TokenUsage{}, fmt.Errorf("openai_oauth compact: status=%d body=%s", resp.StatusCode, truncateOAuth(string(raw), 400))
	}
	var bodyResp struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(raw, &bodyResp); err != nil {
		return nil, TokenUsage{}, fmt.Errorf("openai_oauth compact decode: %w", err)
	}
	if len(bodyResp.Output) == 0 {
		return nil, TokenUsage{}, errors.New("openai_oauth compact returned empty output")
	}
	out := make([]Message, 0, len(bodyResp.Output))
	for _, item := range bodyResp.Output {
		out = append(out, responseItemToMessage(item))
	}
	usage := TokenUsage{}
	if u := decodeUsage(raw); u != nil {
		usage = *u
	}
	return out, usage, nil
}

// looksLikeModelRejection identifies a 400 body whose root cause is the
// model name (rather than e.g. malformed payload). Codex returns a few
// distinct phrasings - "is not supported when using Codex with a ChatGPT
// account", "model_not_found", "does not exist", "invalid model" - so we
// match on the common substrings. Conservative on purpose: a false
// positive just means we retry with the default once.
func looksLikeModelRejection(body string) bool {
	if body == "" {
		return false
	}
	b := strings.ToLower(body)
	switch {
	case strings.Contains(b, "model_not_found"),
		strings.Contains(b, "does not exist"),
		strings.Contains(b, "invalid model"),
		strings.Contains(b, "is not supported"),
		strings.Contains(b, "no such model"),
		strings.Contains(b, "unknown model"),
		strings.Contains(b, "unsupported model"):
		return true
	}
	return false
}

// buildResponsesRequest assembles the JSON payload for /responses. Messages
// are translated into the Responses API's `input` array (one item per turn).
// Tool calls and tool results round-trip via `function_call` / `function_call
// _output` items, the same shape the upstream API documents.
func buildResponsesInput(messages []Message) []any {
	input := make([]any, 0, len(messages))
	for _, m := range messages {
		if raw, ok := RawResponseItem(m); ok {
			var item map[string]any
			if err := json.Unmarshal(raw, &item); err == nil && len(item) > 0 {
				input = append(input, item)
				continue
			}
		}
		switch m.Role {
		case RoleUser:
			input = append(input, map[string]any{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": m.Content},
				},
			})
		case RoleAssistant:
			if m.Content != "" {
				input = append(input, map[string]any{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{"type": "output_text", "text": m.Content},
					},
				})
			}
			for _, tc := range m.ToolCalls {
				args, _ := json.Marshal(tc.Input)
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      tc.Name,
					"arguments": string(args),
				})
			}
		case RoleTool:
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": m.ToolCallID,
				"output":  m.Content,
			})
		}
	}
	return input
}

// buildResponsesRequest assembles the JSON payload for /responses. Messages
// are translated into the Responses API's `input` array (one item per turn),
// except provider-native raw response items returned by /responses/compact are
// passed through unchanged. Tool calls and tool results round-trip via
// `function_call` / `function_call_output` items, the same shape the upstream
// API documents.
func buildResponsesRequest(model, system, cacheKey, effort string, messages []Message, tools []ToolDef) map[string]any {
	body := map[string]any{
		"model":  model,
		"input":  buildResponsesInput(messages),
		"stream": true,
		"store":  false,
	}
	builtinWebSearch := openAIModelSupportsBuiltInWebSearch(model)
	if builtinWebSearch {
		// IMPORTANT: must be "web_search", NOT "web_search_preview".
		// The Codex backend (chatgpt.com/backend-api/codex/responses)
		// rejects "web_search_preview" with 400 "Unsupported tool type"
		// - that string is the public api.openai.com/v1/responses name.
		// Verified against openai/codex tool_spec.rs:
		//   #[serde(rename = "web_search")] WebSearch { ... }
		// DO NOT REVERT TO `web_search_preview` - it will 400 every
		// request because this `tools` array is sent on every turn,
		// not just web-search turns.
		body["tools"] = []map[string]any{{"type": "web_search"}}
	}
	if system != "" {
		body["instructions"] = system
	}
	// Pin the cache shard to the session so the stable prefix auto-caches
	// across turns. Codex's /responses honors prompt_cache_key like the
	// public Responses API.
	if cacheKey != "" {
		body["prompt_cache_key"] = cacheKey
	}
	// Reasoning-capable models compute thinking tokens internally regardless
	// of this flag, but the SUMMARY text only streams when we explicitly
	// request it. Per OpenAI's reasoning guide
	// (developers.openai.com/api/docs/guides/reasoning):
	//
	//	"To access the most detailed summarizer available for a model, set
	//	 the value of this parameter to `auto`. `auto` will be equivalent to
	//	 `detailed` for most reasoning models today."
	//
	// We previously sent "detailed" explicitly - which on gpt-5.x returns an
	// EMPTY summary the large majority of the time, so the boss saw NO
	// streamed reasoning at all. "auto" is the documented way to get the
	// fullest available summary and is what actually streams. Override with
	// INFINITY_OPENAI_REASONING_SUMMARY (auto|concise|detailed) if needed.
	//
	// reasoning.effort (steal C): the caller passes the FINAL resolved level via
	// `effort` (per-turn ctx hint > INFINITY_OPENAI_REASONING_EFFORT env, resolved
	// in StreamCached). When "" we OMIT the field so the model keeps its own
	// default - omit === default - so an un-escalated turn costs exactly what it
	// did before C existed ("never silently change reasoning depth/cost"). The
	// accepted enum (none|low|medium|high|xhigh) is MODEL-DEPENDENT on the Codex
	// backend and not a published contract; a level the backend rejects is caught
	// by the effort-drop 400 fallback in StreamCached. Skipped for non-reasoning
	// models (gpt-4o/4.1) where `reasoning` would error.
	if modelSupportsReasoning(model) {
		summary := strings.TrimSpace(os.Getenv("INFINITY_OPENAI_REASONING_SUMMARY"))
		if summary == "" {
			summary = "auto"
		}
		reasoning := map[string]any{
			"summary": summary,
		}
		if lvl := strings.TrimSpace(effort); lvl != "" {
			reasoning["effort"] = lvl
		}
		body["reasoning"] = reasoning
	}
	if len(tools) > 0 {
		apiTools := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			// Avoid a name collision with OpenAI's built-in `web_search` hosted
			// tool, added above for gpt-5.x. Two tools sharing the name
			// "web_search" (the native one + our Tavily function) is ambiguous and
			// risks a 400; the built-in supersedes ours, so drop our function when
			// it's active. The agent reaches Tavily-class search via the built-in;
			// paywall/transcript/Twitter-Reddit reads route to agent-reach instead.
			if builtinWebSearch && t.Name == "web_search" {
				continue
			}
			schema := t.Schema
			if schema == nil {
				schema = map[string]any{"type": "object"}
			}
			apiTools = append(apiTools, map[string]any{
				"type":        "function",
				"name":        t.Name,
				"description": t.Description,
				"parameters":  schema,
			})
		}
		if existing, ok := body["tools"].([]map[string]any); ok {
			body["tools"] = append(existing, apiTools...)
		} else {
			body["tools"] = apiTools
		}
	}
	return body
}

// openAIModelSupportsBuiltInWebSearch reports whether the model served via
// the ChatGPT-account OAuth path (chatgpt.com/backend-api/codex/responses)
// accepts OpenAI's built-in web search tool. The Codex backend expects the
// bare name `"web_search"` (NOT `"web_search_preview"`, which is the public
// api.openai.com Responses API spelling - Codex 400s with "Unsupported tool
// type" on that). Verified against openai/codex tool_spec.rs (#[serde(rename
// = "web_search")]). The codex roster (gpt-5*, codex-mini-latest, chatgpt-*
// aliases) all support built-in web search; older API-only families do not
// ride the OAuth path so they're excluded.
func openAIModelSupportsBuiltInWebSearch(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return false
	}
	if strings.HasPrefix(m, "gpt-5") || strings.HasPrefix(m, "codex-") || strings.HasPrefix(m, "chatgpt-") {
		return true
	}
	return false
}

// modelSupportsReasoning identifies the OpenAI model families that emit
// reasoning summaries. All GPT-5.x variants (including the minis and nanos)
// support it per OpenAI's model docs, as do the o-series reasoning models.
// gpt-4* models don't and will error if `reasoning` is sent.
func modelSupportsReasoning(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(m, "gpt-5") {
		return true
	}
	if strings.HasPrefix(m, "o4") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o1") {
		return true
	}
	return false
}

// ModelSupportsReasoning is the exported capability check used by the steal-C
// effort router (in serve.go) to clamp: a non-reasoning model gets no effort
// hint, and is NEVER swapped for a reasoning-capable one to satisfy a level.
func ModelSupportsReasoning(model string) bool { return modelSupportsReasoning(model) }

// looksLikeEffortRejection reports whether a 400 body specifically implicates
// the reasoning.effort parameter, so steal C can retry once with effort omitted
// WITHOUT masking unrelated 400s (never-hide-errors). It requires the param name
// (effort/reasoning) AND a rejection verb, so a generic 400 still surfaces.
func looksLikeEffortRejection(body string) bool {
	b := strings.ToLower(body)
	if !strings.Contains(b, "effort") && !strings.Contains(b, "reasoning") {
		return false
	}
	for _, verb := range []string{"unsupported", "invalid", "not supported", "not allowed", "must be one of", "unknown", "unexpected"} {
		if strings.Contains(b, verb) {
			return true
		}
	}
	return false
}

// readResponsesSSE consumes the SSE stream and emits StreamEvents. We accept
// both the explicit `event:` line variants and bare `data:` payloads with a
// `type` discriminator, since the Responses API uses both shapes across model
// versions.
func readResponsesSSE(r io.Reader, out chan<- StreamEvent) (Response, error) {
	var resp Response
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), 4*1024*1024)

	pending := make(map[string]*pendingToolCall)
	// function_call_arguments.delta keys on the output item id (fc_…), which is
	// NOT the call_id `pending` is keyed by — so index pending calls by item id
	// too, otherwise the per-token argument deltas can't be resolved (and the
	// live canvas stream would silently never fire on this path).
	byItem := make(map[string]*pendingToolCall)

	// Tracks whether any reasoning has streamed to `out`. Combined with
	// resp.Text / resp.ToolCalls / pending below, it tells the caller whether a
	// mid-stream error can be retried cleanly — once ANY user-visible content
	// has been emitted, a retry would duplicate it, so we surface instead.
	var sawThinking bool

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if raw == "" || raw == "[DONE]" {
			continue
		}

		var evt struct {
			Type      string          `json:"type"`
			Delta     string          `json:"delta"`
			Text      string          `json:"text"`
			Response  json.RawMessage `json:"response"`
			Item      json.RawMessage `json:"item"`
			ItemID    string          `json:"item_id"`
			Arguments string          `json:"arguments"`
			Output    json.RawMessage `json:"output"`
		}
		if err := json.Unmarshal([]byte(raw), &evt); err != nil {
			continue
		}
		debugLogSeenEvent(evt.Type)

		switch evt.Type {
		case "response.output_text.delta":
			if evt.Delta != "" {
				resp.Text += evt.Delta
				emit(out, StreamEvent{Kind: StreamText, TextDelta: evt.Delta})
			}
		case
			// Current Responses API reasoning event names (gpt-5 reasoning
			// variants, o3, o4 family). Names have shifted across model
			// generations so we handle the family of variants the upstream
			// has shipped - extra unknown ones get ignored silently below.
			"response.reasoning.delta",
			"response.reasoning_summary.delta",
			"response.reasoning_summary_text.delta",
			"response.reasoning_summary_part.delta",
			"response.reasoning_text.delta":
			if evt.Delta != "" {
				sawThinking = true
				emit(out, StreamEvent{Kind: StreamThinking, ThinkingDelta: evt.Delta})
			}
		case "response.output_item.added":
			if call := decodePendingCall(evt.Item); call != nil {
				pending[call.ID] = call
				if call.ItemID != "" {
					byItem[call.ItemID] = call
				}
			}
		case "response.output_item.done":
			// Fallback path: some Responses-API model variants (and partial
			// streams under load) finalize an output item without first
			// emitting per-token deltas. The final `output_item.done` event
			// carries the complete item, so we mine it for any text or
			// function call we haven't already surfaced - without this the
			// turn appears empty in the UI even though the model replied.
			if text := decodeMessageText(evt.Item); text != "" {
				if !strings.HasSuffix(resp.Text, text) {
					delta := strings.TrimPrefix(text, resp.Text)
					if delta != "" {
						resp.Text += delta
						emit(out, StreamEvent{Kind: StreamText, TextDelta: delta})
					}
				}
			}
			if call := decodePendingCall(evt.Item); call != nil {
				if pc, ok := pending[call.ID]; ok {
					if call.Arguments != "" {
						pc.Arguments = call.Arguments
					}
					tc := finalizeToolCall(pc)
					if !toolCallAlreadyEmitted(resp.ToolCalls, tc.ID) {
						resp.ToolCalls = append(resp.ToolCalls, tc)
						emit(out, StreamEvent{Kind: StreamToolCall, ToolCall: &tc})
					}
					delete(pending, call.ID)
				} else if !toolCallAlreadyEmitted(resp.ToolCalls, call.ID) {
					tc := finalizeToolCall(call)
					resp.ToolCalls = append(resp.ToolCalls, tc)
					emit(out, StreamEvent{Kind: StreamToolCall, ToolCall: &tc})
				}
			}
		case "response.function_call_arguments.delta":
			pc := pending[evt.ItemID]
			if pc == nil {
				pc = byItem[evt.ItemID]
			}
			if pc != nil && evt.Delta != "" {
				pc.Arguments += evt.Delta
				emit(out, StreamEvent{
					Kind:       StreamToolInputDelta,
					ToolCallID: pc.ID,
					ToolName:   pc.Name,
					InputDelta: evt.Delta,
				})
			}
		case "response.function_call_arguments.done":
			pc := pending[evt.ItemID]
			if pc == nil {
				pc = byItem[evt.ItemID]
			}
			if pc != nil {
				if evt.Arguments != "" {
					pc.Arguments = evt.Arguments
				}
				if !toolCallAlreadyEmitted(resp.ToolCalls, pc.ID) {
					tc := finalizeToolCall(pc)
					resp.ToolCalls = append(resp.ToolCalls, tc)
					emit(out, StreamEvent{Kind: StreamToolCall, ToolCall: &tc})
				}
				delete(pending, pc.ID)
				delete(byItem, pc.ItemID)
			}
		case
			// Lifecycle events that carry no payload we need to act on.
			// Acknowledging them keeps the unknown-event log focused on
			// genuine surprises rather than steady-state noise.
			"response.created",
			"response.in_progress",
			"response.content_part.added",
			"response.content_part.done",
			"response.output_text.done",
			"response.reasoning_summary_part.added",
			"response.reasoning_summary_part.done",
			"response.reasoning_summary_text.done",
			"response.reasoning.done":
			// no-op
		case "response.completed":
			if usage := decodeUsage(evt.Response); usage != nil {
				resp.Usage = *usage
			}
			resp.StopReason = "end_turn"
		case "response.error", "error":
			errMsg := truncateOAuth(raw, 400)
			// If the failure is a transient provider hiccup (server_error,
			// overloaded, rate_limit, …) AND we haven't emitted any
			// user-visible content yet, hand it back as retryable WITHOUT
			// emitting terminal events — Stream's retry loop owns the decision
			// to re-issue or give up. This is the fix for the boss's
			// inbox-triage cron dying on a `server_error` at sequence_number:1.
			emittedContent := resp.Text != "" || len(resp.ToolCalls) > 0 ||
				len(pending) > 0 || len(byItem) > 0 || sawThinking
			if isTransientResponsesError(raw) && !emittedContent {
				return resp, &retryableStreamError{msg: errMsg}
			}
			emit(out, StreamEvent{Kind: StreamError, Err: errMsg})
			emit(out, StreamEvent{Kind: StreamComplete, StopReason: "error"})
			return resp, errors.New(errMsg)
		default:
			// Reasoning-bearing events have shifted across model generations
			// (gpt-5-* families especially), and OpenAI keeps shipping new
			// event names that carry the summary text. Anything ending in
			// `.delta` whose path contains "reasoning" is a reasoning chunk
			// - surface it so the ThinkingBlock fills in even on new
			// variants we haven't explicitly listed above. The narrow
			// substring guard avoids surfacing unrelated `.delta` events
			// (function_call_arguments etc. are handled in their own
			// cases). Unknown non-reasoning events are logged once-per
			// type to keep the next mismatch trivial to diagnose.
			t := evt.Type
			// Widen the net for reasoning-shaped events. OpenAI has
			// shipped reasoning content under "reasoning", "thinking",
			// "summary" - any *.delta with a Delta payload AND one of
			// those keywords is treated as thinking content. Better to
			// over-surface (you see the model's chain-of-thought
			// summary) than under-surface (empty bubble).
			if strings.HasSuffix(t, ".delta") && evt.Delta != "" {
				if strings.Contains(t, "reasoning") ||
					strings.Contains(t, "thinking") ||
					strings.Contains(t, "thought") ||
					strings.Contains(t, "summary") {
					emit(out, StreamEvent{Kind: StreamThinking, ThinkingDelta: evt.Delta})
					break
				}
			}
			logUnknownOAIEvent(t)
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		emit(out, StreamEvent{Kind: StreamError, Err: err.Error()})
		return resp, err
	}

	if resp.StopReason == "" {
		resp.StopReason = "end_turn"
	}
	emit(out, StreamEvent{Kind: StreamComplete, StopReason: resp.StopReason, Usage: &resp.Usage})
	return resp, nil
}

type pendingToolCall struct {
	ID        string // call_id — the id the finalized ToolCall carries
	ItemID    string // output item id (fc_…) — what function_call_arguments.delta keys on
	Name      string
	Arguments string
}

func decodePendingCall(raw json.RawMessage) *pendingToolCall {
	if len(raw) == 0 {
		return nil
	}
	var item struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil
	}
	if item.Type != "function_call" {
		return nil
	}
	id := item.CallID
	if id == "" {
		id = item.ID
	}
	return &pendingToolCall{ID: id, ItemID: item.ID, Name: item.Name, Arguments: item.Arguments}
}

func finalizeToolCall(pc *pendingToolCall) ToolCall {
	tc := ToolCall{ID: pc.ID, Name: pc.Name}
	if pc.Arguments != "" {
		_ = json.Unmarshal([]byte(pc.Arguments), &tc.Input)
	}
	if tc.Input == nil {
		tc.Input = map[string]any{}
	}
	return tc
}

// decodeMessageText pulls the concatenated assistant text out of an
// `output_item.done` payload. The Responses API ships message items as
// `{"type":"message","content":[{"type":"output_text","text":"…"}, …]}`,
// so we walk the content array and join every output_text segment. Any
// non-message item type (function_call, reasoning) returns the empty
// string - those are handled by their own decoders.
func decodeMessageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var item struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return ""
	}
	if item.Type != "message" {
		return ""
	}
	var b strings.Builder
	for _, c := range item.Content {
		if c.Type == "output_text" && c.Text != "" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

func responseItemToMessage(raw json.RawMessage) Message {
	var item struct {
		Type      string `json:"type"`
		Role      string `json:"role"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Output    any    `json:"output"`
		Content   []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return WithRawResponseItem(Message{Role: RoleSystem}, raw)
	}
	switch item.Type {
	case "message":
		var b strings.Builder
		for _, c := range item.Content {
			switch c.Type {
			case "input_text", "output_text":
				b.WriteString(c.Text)
			case "refusal":
				b.WriteString(c.Refusal)
			}
		}
		role := Role(item.Role)
		if role != RoleUser && role != RoleAssistant && role != RoleSystem {
			role = RoleSystem
		}
		return WithRawResponseItem(Message{Role: role, Content: b.String()}, raw)
	case "function_call", "custom_tool_call":
		input := map[string]any{}
		if item.Arguments != "" {
			_ = json.Unmarshal([]byte(item.Arguments), &input)
		}
		return WithRawResponseItem(Message{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{{
				ID:    item.CallID,
				Name:  item.Name,
				Input: input,
			}},
		}, raw)
	case "function_call_output", "custom_tool_call_output":
		var output string
		switch v := item.Output.(type) {
		case string:
			output = v
		case nil:
			output = ""
		default:
			if b, err := json.Marshal(v); err == nil {
				output = string(b)
			}
		}
		return WithRawResponseItem(Message{
			Role:       RoleTool,
			Content:    output,
			ToolCallID: item.CallID,
		}, raw)
	default:
		// Reasoning / compaction items don't map cleanly onto Infinity's
		// generic chat roles. Keep the raw item so OpenAI sees the canonical
		// compacted window on the next request.
		return WithRawResponseItem(Message{Role: RoleSystem}, raw)
	}
}

// toolCallAlreadyEmitted prevents the `output_item.done` fallback from
// double-appending a tool call that was already finalized via the
// `function_call_arguments.done` event in the normal streaming path.
func toolCallAlreadyEmitted(calls []ToolCall, id string) bool {
	if id == "" {
		return false
	}
	for _, c := range calls {
		if c.ID == id {
			return true
		}
	}
	return false
}

func decodeUsage(raw json.RawMessage) *TokenUsage {
	if len(raw) == 0 {
		return nil
	}
	var body struct {
		Usage struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil
	}
	if body.Usage.InputTokens == 0 && body.Usage.OutputTokens == 0 {
		return nil
	}
	// input_tokens INCLUDES cached tokens; subtract to get the full-priced
	// uncached portion and carry the cached count separately.
	cached := body.Usage.InputTokensDetails.CachedTokens
	uncached := body.Usage.InputTokens - cached
	if uncached < 0 {
		uncached = 0
	}
	return &TokenUsage{Input: uncached, Output: body.Usage.OutputTokens, CacheRead: cached}
}

func truncateOAuth(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// oauthFallbackModel is the last-resort Codex model used when even the
// configured default is rejected by the account/plan. codex-mini-latest is the
// smallest, most broadly-served Codex model (the same id tier nicknames like
// "haiku"/"mini" already map to), so it's the safest thing to fall back to.
// Override with LLM_OPENAI_OAUTH_FALLBACK_MODEL if a given account exposes a
// different floor model.
func oauthFallbackModel() string {
	if m := strings.TrimSpace(os.Getenv("LLM_OPENAI_OAUTH_FALLBACK_MODEL")); m != "" {
		return m
	}
	return "codex-mini-latest"
}

// tierNicknameToCodex maps cross-provider tier nicknames ONLY.
//
// "haiku" / "small" / "cheap" / "mini" - keywords sub-agents pass for
// cheap reasoning when they don't know which provider is wired. On the
// OpenAI OAuth path those translate to codex-mini-latest. "sonnet" /
// "opus" / "default" map to gpt-5-codex. Real model ids (anything with
// a "gpt-" or "o[1-9]" prefix, a dot, or anything that looks like an
// actual OpenAI model id) pass through with "" so the caller keeps
// the boss's exact choice. Settings ARE the truth.
//
// Returns "" when the input isn't a nickname this helper should touch.
func tierNicknameToCodex(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch m {
	case "haiku", "cheap", "small", "mini":
		return "codex-mini-latest"
	case "sonnet", "default", "medium",
		"opus", "premium", "large":
		return "gpt-5-codex"
	}
	return ""
}
