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
// and the ThinkingBlock goes silent — the next deploy's logs show
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
//     backend may change — when it does, update the request shape here and
//     bump the user-agent so we can correlate failures in the audit log.
//   - The body uses OpenAI's Responses API JSON. Streamed events arrive as
//     SSE with `event: <name>` + `data: <json>` pairs; we parse the small
//     subset we care about and discard the rest.
type OpenAIOAuth struct {
	store          *OAuthStore
	model          string
	httpClient     *http.Client
	apiBase        string
	authBase       string
	clientID       string
	scopes         string
	redirectURI    string
	refreshLead    time.Duration

	// refreshMu serializes refresh attempts so a burst of concurrent turns
	// doesn't trigger N parallel /oauth/token calls that all rotate the
	// refresh token and invalidate each other.
	refreshMu sync.Mutex
}

const (
	// Codex CLI's public OAuth client. Override via OPENAI_OAUTH_CLIENT_ID
	// when OpenAI rotates this identifier (rare but it has happened).
	defaultOpenAIClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultOpenAIAuthBase    = "https://auth.openai.com"
	defaultOpenAIAPIBase     = "https://chatgpt.com/backend-api/codex"
	// Scopes must include the connectors scopes Codex CLI requests —
	// without them the issuer routes you to the platform project picker
	// instead of the subscription-org consent screen.
	defaultOpenAIScopes = "openid profile email offline_access api.connectors.read api.connectors.invoke"
	defaultOpenAIRedirectURI = "http://localhost:1455/auth/callback"
	defaultOpenAIRefreshLead = 2 * time.Minute
)

func NewOpenAIOAuth(store *OAuthStore, model string) *OpenAIOAuth {
	if model == "" {
		// Default to the Codex roster — ChatGPT-account OAuth (the
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
		httpClient:  &http.Client{Timeout: 0}, // streaming — no overall timeout
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
// refresh — keeps the OAuth contract in one place.
func (o *OpenAIOAuth) ClientID() string    { return o.clientID }
func (o *OpenAIOAuth) AuthBase() string    { return o.authBase }
func (o *OpenAIOAuth) APIBase() string     { return o.apiBase }
func (o *OpenAIOAuth) RedirectURI() string { return o.redirectURI }
func (o *OpenAIOAuth) Scopes() string      { return o.scopes }

// --- PKCE helpers (shared with the HTTP start handler) ----------------------

// GeneratePKCE returns a (verifier, challenge) pair where the challenge is
// the URL-safe base64-encoded SHA256 of the verifier — the S256 method.
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
// instead bind the resulting token to the user's ChatGPT subscription org —
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
		return tok, errors.New("openai_oauth: token expired and no refresh_token stored — reconnect ChatGPT")
	}

	o.refreshMu.Lock()
	defer o.refreshMu.Unlock()

	// Re-check under lock — another goroutine may have refreshed while we
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
// without verifying signatures — we only use these for identity display in
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
		// Some providers pad the segment — retry tolerant.
		if p, perr := base64.URLEncoding.DecodeString(parts[1] + strings.Repeat("=", (4-len(parts[1])%4)%4)); perr == nil {
			payload = p
		} else {
			return "", ""
		}
	}
	var claims struct {
		Sub               string `json:"sub"`
		Email             string `json:"email"`
		ChatGPTAccountID  string `json:"https://api.openai.com/auth/chatgpt_account_id"`
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
	var resp Response

	tok, err := o.refreshIfNeeded(ctx)
	if err != nil {
		emit(out, StreamEvent{Kind: StreamError, Err: err.Error()})
		return resp, err
	}

	effectiveModel := o.model
	if model != "" {
		// OAuth path serves the Codex roster, NOT the API's full
		// gpt-5 / gpt-5-mini lineup. We have our own normalizer so a
		// per-call "haiku" or "gpt-5" from a sub-agent maps to
		// gpt-5-codex / codex-mini-latest instead of a model the
		// subscription plan can't actually run.
		if normalized := normalizeCodexModel(model); normalized != "" {
			effectiveModel = normalized
		}
		// Unknown name stays as the configured default; if the model
		// *also* turns out to be one the account can't serve, the
		// retry-on-400 path below catches it.
	}
	// Defense in depth: even o.model could be wrong if someone set
	// LLM_MODEL_OPENAI_OAUTH=gpt-5 manually. Final-mile normalize.
	if codex := normalizeCodexModel(effectiveModel); codex != "" {
		effectiveModel = codex
	}

	httpResp, attemptErr := o.attemptStream(ctx, tok, effectiveModel, system, messages, tools)

	// Self-heal: if Codex rejected the per-call model override (typical
	// reasons: it's an Anthropic nickname like "haiku", a deprecated id,
	// or a model the boss's plan doesn't expose), retry ONCE with the
	// provider's configured default so an upstream bad guess never tanks
	// the whole turn. Anything else surfaces as before.
	if attemptErr == nil && httpResp.StatusCode == 400 && effectiveModel != o.model && o.model != "" {
		raw, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		bodyStr := string(raw)
		if looksLikeModelRejection(bodyStr) {
			unknownOAIEventLog.Printf(
				"openai_oauth: model %q rejected, retrying with default %q (reason: %s)",
				effectiveModel, o.model, truncateOAuth(bodyStr, 200),
			)
			httpResp, attemptErr = o.attemptStream(ctx, tok, o.model, system, messages, tools)
		} else {
			// Re-emit the original body for the non-retry path below.
			httpResp.Body = io.NopCloser(bytes.NewReader(raw))
		}
	}

	if attemptErr != nil {
		emit(out, StreamEvent{Kind: StreamError, Err: attemptErr.Error()})
		return resp, attemptErr
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(httpResp.Body)
		err := fmt.Errorf("openai_oauth: status=%d body=%s", httpResp.StatusCode, truncateOAuth(string(raw), 400))
		emit(out, StreamEvent{Kind: StreamError, Err: err.Error()})
		return resp, err
	}

	return readResponsesSSE(httpResp.Body, out)
}

// attemptStream issues a single /responses request for the given model
// and returns the raw HTTP response. Pulled out of Stream so the caller
// can inspect the status, decide whether to retry with a different
// model, and reissue without duplicating header / payload assembly.
func (o *OpenAIOAuth) attemptStream(
	ctx context.Context,
	tok OAuthToken,
	model, system string,
	messages []Message,
	tools []ToolDef,
) (*http.Response, error) {
	body := buildResponsesRequest(model, system, messages, tools)
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

// looksLikeModelRejection identifies a 400 body whose root cause is the
// model name (rather than e.g. malformed payload). Codex returns a few
// distinct phrasings — "is not supported when using Codex with a ChatGPT
// account", "model_not_found", "does not exist", "invalid model" — so we
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
func buildResponsesRequest(model, system string, messages []Message, tools []ToolDef) map[string]any {
	input := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
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

	body := map[string]any{
		"model":  model,
		"input":  input,
		"stream": true,
		"store":  false,
	}
	if system != "" {
		body["instructions"] = system
	}
	// Reasoning-capable models compute thinking tokens internally regardless
	// of this flag, but the SUMMARY text only streams when we explicitly
	// request it. Without `reasoning.summary` the UI's "Jarvis is thinking"
	// pill stays empty even though the model is actually reasoning. We ask
	// for "auto" so the upstream picks whichever shape its current model
	// supports (concise/detailed). Skipped for non-reasoning models
	// (gpt-4o, gpt-4.1) where the param would error.
	if modelSupportsReasoning(model) {
		body["reasoning"] = map[string]any{
			"summary": "auto",
		}
	}
	if len(tools) > 0 {
		apiTools := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
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
		body["tools"] = apiTools
	}
	return body
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

// readResponsesSSE consumes the SSE stream and emits StreamEvents. We accept
// both the explicit `event:` line variants and bare `data:` payloads with a
// `type` discriminator, since the Responses API uses both shapes across model
// versions.
func readResponsesSSE(r io.Reader, out chan<- StreamEvent) (Response, error) {
	var resp Response
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), 4*1024*1024)

	pending := make(map[string]*pendingToolCall)

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
			Type     string          `json:"type"`
			Delta    string          `json:"delta"`
			Text     string          `json:"text"`
			Response json.RawMessage `json:"response"`
			Item     json.RawMessage `json:"item"`
			ItemID   string          `json:"item_id"`
			Arguments string         `json:"arguments"`
			Output    json.RawMessage `json:"output"`
		}
		if err := json.Unmarshal([]byte(raw), &evt); err != nil {
			continue
		}

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
			// has shipped — extra unknown ones get ignored silently below.
			"response.reasoning.delta",
			"response.reasoning_summary.delta",
			"response.reasoning_summary_text.delta",
			"response.reasoning_summary_part.delta",
			"response.reasoning_text.delta":
			if evt.Delta != "" {
				emit(out, StreamEvent{Kind: StreamThinking, ThinkingDelta: evt.Delta})
			}
		case "response.output_item.added":
			if call := decodePendingCall(evt.Item); call != nil {
				pending[call.ID] = call
			}
		case "response.output_item.done":
			// Fallback path: some Responses-API model variants (and partial
			// streams under load) finalize an output item without first
			// emitting per-token deltas. The final `output_item.done` event
			// carries the complete item, so we mine it for any text or
			// function call we haven't already surfaced — without this the
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
			if pc := pending[evt.ItemID]; pc != nil {
				pc.Arguments += evt.Delta
			}
		case "response.function_call_arguments.done":
			if pc := pending[evt.ItemID]; pc != nil {
				if evt.Arguments != "" {
					pc.Arguments = evt.Arguments
				}
				tc := finalizeToolCall(pc)
				resp.ToolCalls = append(resp.ToolCalls, tc)
				emit(out, StreamEvent{Kind: StreamToolCall, ToolCall: &tc})
				delete(pending, evt.ItemID)
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
			emit(out, StreamEvent{Kind: StreamError, Err: errMsg})
			emit(out, StreamEvent{Kind: StreamComplete, StopReason: "error"})
			return resp, errors.New(errMsg)
		default:
			// Reasoning-bearing events have shifted across model generations
			// (gpt-5-* families especially), and OpenAI keeps shipping new
			// event names that carry the summary text. Anything ending in
			// `.delta` whose path contains "reasoning" is a reasoning chunk
			// — surface it so the ThinkingBlock fills in even on new
			// variants we haven't explicitly listed above. The narrow
			// substring guard avoids surfacing unrelated `.delta` events
			// (function_call_arguments etc. are handled in their own
			// cases). Unknown non-reasoning events are logged once-per
			// type to keep the next mismatch trivial to diagnose.
			t := evt.Type
			if strings.Contains(t, "reasoning") && strings.HasSuffix(t, ".delta") && evt.Delta != "" {
				emit(out, StreamEvent{Kind: StreamThinking, ThinkingDelta: evt.Delta})
				break
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
	ID        string
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
	return &pendingToolCall{ID: id, Name: item.Name, Arguments: item.Arguments}
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
// string — those are handled by their own decoders.
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
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil
	}
	if body.Usage.InputTokens == 0 && body.Usage.OutputTokens == 0 {
		return nil
	}
	return &TokenUsage{Input: body.Usage.InputTokens, Output: body.Usage.OutputTokens}
}

func truncateOAuth(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// normalizeCodexModel maps a model id / nickname onto the Codex roster
// served via ChatGPT-account OAuth. The OAuth path REJECTS plain
// "gpt-5" / "gpt-5-mini" etc. with HTTP 400; only the codex-tagged
// variants are exposed to subscription clients.
//
// Mapping table (deliberately conservative — when in doubt, return
// gpt-5-codex which is the standard Codex CLI default and supports
// tool use + the largest context window the subscription plan offers):
//
//	gpt-5, gpt-5-codex, codex-large, opus, premium, large,
//	sonnet, default, medium                    → gpt-5-codex
//	gpt-5-mini, codex-mini-latest, codex-mini,
//	haiku, cheap, small, mini                  → codex-mini-latest
//	o4-mini, o3-mini                           → codex-mini-latest
//	chatgpt-*, o1-*                            → pass through (special-case
//	                                              ids the subscription serves
//	                                              directly when they exist)
//
// Returns "" if the input doesn't look like anything the OAuth path
// can serve — caller falls back to o.model (which we've already
// final-mile-normalized above).
func normalizeCodexModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return ""
	}
	// Already a Codex-roster id → pass through.
	if strings.HasPrefix(m, "gpt-5-codex") ||
		strings.HasPrefix(m, "codex-mini") ||
		strings.HasPrefix(m, "codex-large") ||
		strings.HasPrefix(m, "chatgpt-") {
		return model
	}
	// Tier nicknames + the un-suffixed gpt-5 ids that 400 against the
	// OAuth path get remapped to the closest Codex variant.
	switch {
	case strings.HasPrefix(m, "gpt-5-mini"),
		strings.HasPrefix(m, "o4-mini"),
		strings.HasPrefix(m, "o3-mini"):
		return "codex-mini-latest"
	case strings.HasPrefix(m, "gpt-5"):
		return "gpt-5-codex"
	}
	switch m {
	case "haiku", "cheap", "small", "mini":
		return "codex-mini-latest"
	case "sonnet", "default", "medium",
		"opus", "premium", "large":
		return "gpt-5-codex"
	}
	// Unknown — let the caller decide. The retry-on-400 path will
	// fall back to o.model, which we've also final-mile normalized.
	return ""
}
