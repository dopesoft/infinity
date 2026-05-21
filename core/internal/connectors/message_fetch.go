package connectors

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// MessageFetcher pulls the full rendered content (HTML + plain text) of a
// single connector message on demand. It backs the dashboard's lazy
// "open a follow-up → render the real email" path: nothing is fetched at
// poll time (no payload bloat, no wasted API calls), so the HTML body is
// only retrieved when the boss actually opens an item.
//
// Per CLAUDE.md Rule #1 this is a GENERIC building block, not a per-vendor
// branch: the verb + argument mapping for each source lives in one
// config-driven table (sourceFetchSpec). Adding Slack/Linear later is one
// map entry, not a new code path.
type MessageFetcher struct {
	exec  *ExecuteClient
	cache *Cache
}

// NewMessageFetcher wires the Composio execute client (required) and the
// connectors cache (optional - used to resolve an email/alias hint to a
// connected_account_id). Returns nil when exec is nil so callers stay
// nil-safe and the endpoint degrades to "no rich message available".
func NewMessageFetcher(exec *ExecuteClient, cache *Cache) *MessageFetcher {
	if exec == nil {
		return nil
	}
	return &MessageFetcher{exec: exec, cache: cache}
}

// fetchSpec describes how to retrieve one message for a given source. The
// Gmail verb is the docs-recommended GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID with
// format=full (GMAIL_FETCH_EMAILS does NOT reliably return HTML - it lives
// base64url-encoded inside payload.parts, and only this verb guarantees
// complete content).
type fetchSpec struct {
	toolkit string
	verb    string
	// idArg is the request argument carrying the provider message id.
	idArg string
	extra map[string]any
}

// sourceFetchSpec maps a follow-up source to its message-fetch verb. Keys
// are matched case-insensitively and also against common aliases the
// triager/surface producers use (e.g. "gmail_triage" → gmail).
var sourceFetchSpec = map[string]fetchSpec{
	"gmail": {
		toolkit: "gmail",
		verb:    "GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID",
		idArg:   "message_id",
		extra:   map[string]any{"format": "full"},
	},
}

// specForSource normalises a free-form source string ("gmail", "gmail_triage",
// "inbox_triage", "email", ...) down to a known fetch spec. Anything that
// looks like Gmail routes to the Gmail verb; unknown sources return false.
func specForSource(source string) (fetchSpec, bool) {
	s := strings.ToLower(strings.TrimSpace(source))
	if spec, ok := sourceFetchSpec[s]; ok {
		return spec, true
	}
	// Alias fold: triage/inbox/email producers all describe Gmail today.
	if strings.Contains(s, "gmail") || s == "email" || s == "inbox" || strings.Contains(s, "mail") {
		return sourceFetchSpec["gmail"], true
	}
	return fetchSpec{}, false
}

// FetchMessage returns the (html, text) bodies of one message. Either may be
// empty - callers fall back to the plain-text preview they already hold.
// A non-nil error means the fetch genuinely failed (transport / upstream);
// an unsupported source returns ("", "", nil) so the UI degrades quietly.
func (f *MessageFetcher) FetchMessage(ctx context.Context, source, accountHint, messageID string) (html, text string, err error) {
	if f == nil || f.exec == nil {
		return "", "", nil
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return "", "", nil
	}
	spec, ok := specForSource(source)
	if !ok {
		return "", "", nil
	}

	// Surfaced external ids are composite for global uniqueness, e.g.
	// "gmail:ca_lBW_QmdjwQhz:19e479e3b0de7403". Extract the real provider
	// message id (Composio rejects the composite as "Invalid id value") and
	// the embedded connected_account_id, which is authoritative for which
	// account the message lives in.
	realID, embeddedAcct := normalizeGmailID(messageID)
	if realID == "" {
		return "", "", nil
	}
	if embeddedAcct != "" {
		accountHint = embeddedAcct
	}

	args := map[string]any{spec.idArg: realID}
	for k, v := range spec.extra {
		args[k] = v
	}

	// Composio rejects with code 1811 unless BOTH connected_account_id AND
	// the account's entity (user_id) ride on the request (same constraint
	// the calendar provider handles). Resolve both from the cache.
	caID, entity := f.resolveAccount(spec.toolkit, accountHint)
	resp, err := f.exec.Execute(ctx, ExecuteRequest{
		Slug:               spec.verb,
		ConnectedAccountID: caID,
		UserID:             entity,
		Arguments:          args,
	})
	if err != nil {
		return "", "", fmt.Errorf("fetch message: %w", err)
	}
	if resp == nil || !resp.Successful {
		msg := "unknown"
		if resp != nil && resp.Error != "" {
			msg = resp.Error
		}
		return "", "", fmt.Errorf("fetch message: upstream unsuccessful: %s", msg)
	}

	html, text = extractMessageBodies(resp.Data)
	return html, text, nil
}

// resolveAccount turns an account hint into a (connected_account_id, entity)
// pair. The hint may already BE a connected_account_id (ca_...), an
// email/identity, or an alias. We match it to a cached account to recover
// the entity (Composio user_id), which the execute API requires alongside
// the account id. Falls back to the hint verbatim with an empty entity when
// nothing resolves (degrades to the 1811 error → empty body → preview).
func (f *MessageFetcher) resolveAccount(toolkit, hint string) (caID, entity string) {
	hint = strings.TrimSpace(hint)
	caID = hint
	if f.cache == nil {
		return caID, ""
	}
	accounts := f.cache.AccountsByToolkit()[strings.ToLower(toolkit)]
	// When the hint isn't already a ca_ id, match it (email / identity /
	// alias) to one of the toolkit's accounts; or use the sole account.
	if !strings.HasPrefix(hint, "ca_") {
		var match *Account
		if hint != "" {
			needle := strings.ToLower(hint)
			for _, a := range accounts {
				if a == nil {
					continue
				}
				if strings.EqualFold(a.IdentityHint, hint) || strings.EqualFold(a.Alias, hint) ||
					(needle != "" && strings.Contains(strings.ToLower(a.IdentityHint), needle)) {
					match = a
					break
				}
			}
		}
		if match == nil && len(accounts) == 1 {
			match = accounts[0]
		}
		if match != nil {
			caID = match.ID
		}
	}
	// Recover the entity (user_id) for the chosen account id by scanning all
	// toolkits (mirrors serve.go's userIDFor used by the calendar provider).
	for _, accs := range f.cache.AccountsByToolkit() {
		for _, a := range accs {
			if a != nil && a.ID == caID {
				return caID, a.UserID
			}
		}
	}
	return caID, ""
}

// normalizeGmailID unpacks a (possibly composite) external id into the raw
// Gmail message id and any embedded connected_account_id. Surfaced ids look
// like "gmail:ca_xxx:19e479e3b0de7403" (provider:account:messageId); raw ids
// like "19e479e3b0de7403" pass through unchanged.
func normalizeGmailID(raw string) (msgID, account string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.Contains(raw, ":") {
		return raw, ""
	}
	parts := strings.Split(raw, ":")
	msgID = strings.TrimSpace(parts[len(parts)-1])
	for _, p := range parts {
		if strings.HasPrefix(p, "ca_") {
			account = p
		}
	}
	return msgID, account
}

// extractMessageBodies walks a Gmail message resource (however Composio
// wraps it) and returns the first text/html and text/plain bodies, decoded
// from base64url. Defensive against wrapper nesting (data / response_data /
// message) and multipart trees of arbitrary depth.
func extractMessageBodies(raw json.RawMessage) (html, text string) {
	if len(raw) == 0 {
		return "", ""
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", ""
	}
	payload := findPayload(root)
	if payload == nil {
		return "", ""
	}
	htmlData, textData := walkParts(payload)
	return decodeB64URL(htmlData), decodeB64URL(textData)
}

// findPayload locates the Gmail "payload" object, tolerating the common
// Composio wrappers around the raw Google API response.
func findPayload(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	if p, ok := m["payload"].(map[string]any); ok {
		return p
	}
	for _, key := range []string{"data", "response_data", "message", "messageData", "result"} {
		if nested, ok := m[key].(map[string]any); ok {
			if p := findPayload(nested); p != nil {
				return p
			}
		}
	}
	return nil
}

// walkParts recurses a MIME part tree, returning the first encountered
// text/html and text/plain body data strings (still base64url-encoded).
func walkParts(node map[string]any) (htmlData, textData string) {
	mime, _ := node["mimeType"].(string)
	mime = strings.ToLower(mime)
	if body, ok := node["body"].(map[string]any); ok {
		if data, _ := body["data"].(string); data != "" {
			switch {
			case strings.HasPrefix(mime, "text/html"):
				htmlData = data
			case strings.HasPrefix(mime, "text/plain"):
				textData = data
			}
		}
	}
	if parts, ok := node["parts"].([]any); ok {
		for _, p := range parts {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			h, t := walkParts(pm)
			if htmlData == "" {
				htmlData = h
			}
			if textData == "" {
				textData = t
			}
		}
	}
	return htmlData, textData
}

// decodeB64URL decodes Gmail's base64url body data, tolerating presence or
// absence of padding. Returns "" on any decode failure.
func decodeB64URL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return string(b)
	}
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return string(b)
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return string(b)
	}
	return ""
}
