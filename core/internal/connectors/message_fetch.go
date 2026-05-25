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

// Attachment is one email attachment's display + download metadata. ID is the
// provider's internal attachment id (NOT the filename), needed to download.
type Attachment struct {
	Filename string `json:"name"`
	MimeType string `json:"mimeType,omitempty"`
	ID       string `json:"id"`
}

// sourceFetchSpec maps a follow-up source to its message-fetch verb. Keys
// are matched case-insensitively and also against common aliases the
// triager/surface producers use (e.g. "gmail_triage" → gmail).
var sourceFetchSpec = map[string]fetchSpec{
	"gmail": {
		toolkit:    "gmail",
		verb:       "GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID",
		idArg: "message_id",
		extra: map[string]any{"format": "full"},
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

// resolved bundles everything needed to call a Composio message/attachment
// verb for one item: the toolkit spec, the real provider message id, and the
// connected_account_id + entity the execute API requires.
type resolved struct {
	spec   fetchSpec
	realID string
	caID   string
	entity string
}

// resolve turns (source, accountHint, messageID) into a resolved bundle,
// applying the toolkit-from-prefix and composite-id rules. ok=false means the
// source isn't a fetchable toolkit (caller degrades quietly).
func (f *MessageFetcher) resolve(source, accountHint, messageID string) (resolved, bool) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return resolved{}, false
	}
	// Determine the TOOLKIT. The composite external_id encodes it as its first
	// segment ("gmail:ca_x:msgid"), which is authoritative - the surface row's
	// `source` column is the AUTHOR ("agent", "cron", a skill name), NOT the
	// toolkit. Prefer the prefix; fall back to `source` for raw poller ids.
	toolkitHint := source
	if i := strings.Index(messageID, ":"); i > 0 {
		toolkitHint = messageID[:i]
	}
	spec, ok := specForSource(toolkitHint)
	if !ok {
		spec, ok = specForSource(source)
	}
	if !ok {
		return resolved{}, false
	}
	// Composite ids ("gmail:ca_x:msgid") → real provider id + embedded account
	// (Composio rejects the composite with "Invalid id value").
	realID, embeddedAcct := normalizeGmailID(messageID)
	if realID == "" {
		return resolved{}, false
	}
	if embeddedAcct != "" {
		accountHint = embeddedAcct
	}
	// Composio 1811s unless BOTH connected_account_id AND the account entity
	// (user_id) ride on the request (same constraint the calendar provider has).
	caID, entity := f.resolveAccount(spec.toolkit, accountHint)
	return resolved{spec: spec, realID: realID, caID: caID, entity: entity}, true
}

// FetchMessage returns the HTML + plain-text bodies and attachment metadata of
// one message. Any field may be empty - callers fall back to the preview they
// already hold. A non-nil error means the fetch genuinely failed (transport /
// upstream); an unsupported source returns zero values + nil so the UI
// degrades quietly.
func (f *MessageFetcher) FetchMessage(ctx context.Context, source, accountHint, messageID string) (html, text string, attachments []Attachment, err error) {
	if f == nil || f.exec == nil {
		return "", "", nil, nil
	}
	r, ok := f.resolve(source, accountHint, messageID)
	if !ok {
		return "", "", nil, nil
	}
	args := map[string]any{r.spec.idArg: r.realID}
	for k, v := range r.spec.extra {
		args[k] = v
	}
	resp, err := f.exec.Execute(ctx, ExecuteRequest{
		Slug:               r.spec.verb,
		ConnectedAccountID: r.caID,
		UserID:             r.entity,
		Arguments:          args,
	})
	if err != nil {
		return "", "", nil, fmt.Errorf("fetch message: %w", err)
	}
	if resp == nil || !resp.Successful {
		msg := "unknown"
		if resp != nil && resp.Error != "" {
			msg = resp.Error
		}
		return "", "", nil, fmt.Errorf("fetch message: upstream unsuccessful: %s", msg)
	}
	html, text, attachments = extractMessage(resp.Data)
	return html, text, attachments, nil
}

// resolveAccount turns an account hint into a (connected_account_id, entity)
// pair. The hint may already BE a connected_account_id (ca_...), an
// email/identity, or an alias. We match it to a cached account to recover
// the entity (Composio user_id), which the execute API requires alongside
// the account id. Falls back to the hint verbatim with an empty entity when
// nothing resolves (degrades to the 1811 error → empty body → preview).
//
// Health redirect: surfaced follow-ups embed the connected_account_id that
// was live when they were captured ("gmail:ca_OLD:msgid"). If the boss later
// revokes+reconnects, that embedded id goes REVOKED and a new ACTIVE account
// is minted - so a literal use of the embedded id keeps 422ing forever. When
// the resolved account is dead/missing we re-point at a healthy account of
// the SAME mailbox (identity-matched), so old follow-ups render through the
// new connection. We only cross to a different account when the dead account's
// identity is unknown (single-mailbox case) - never silently fetch a
// different known mailbox (wrong message id → 404 / leak).
func (f *MessageFetcher) resolveAccount(toolkit, hint string) (caID, entity string) {
	hint = strings.TrimSpace(hint)
	caID = hint
	if f.cache == nil {
		return caID, ""
	}
	accounts := f.cache.AccountsByToolkit()[strings.ToLower(toolkit)]

	// Find the account the hint points at: a ca_ id is matched by id, anything
	// else by identity / alias; a bare hint with a single account uses it.
	var chosen *Account
	if strings.HasPrefix(hint, "ca_") {
		for _, a := range accounts {
			if a != nil && a.ID == hint {
				chosen = a
				break
			}
		}
	} else if hint != "" {
		needle := strings.ToLower(hint)
		for _, a := range accounts {
			if a == nil {
				continue
			}
			if strings.EqualFold(a.IdentityHint, hint) || strings.EqualFold(a.Alias, hint) ||
				strings.Contains(strings.ToLower(a.IdentityHint), needle) {
				chosen = a
				break
			}
		}
	}
	if chosen == nil && hint == "" && len(accounts) == 1 {
		chosen = accounts[0]
	}

	// Dead/missing → redirect to the healthiest same-toolkit account.
	if !accountActive(chosen) {
		if alt := bestActiveAccount(accounts, chosen); alt != nil {
			chosen = alt
		}
	}

	if chosen != nil {
		return chosen.ID, chosen.UserID
	}
	return caID, ""
}

// accountActive reports whether a Composio account is usable for execute.
// Composio stores the upstream state verbatim ("ACTIVE", "REVOKED", ...).
func accountActive(a *Account) bool {
	return a != nil && strings.EqualFold(strings.TrimSpace(a.Status), "ACTIVE")
}

// bestActiveAccount picks the healthiest replacement for a dead/missing
// account among same-toolkit accounts. If the dead account's identity is
// known, ONLY an ACTIVE account of the same identity qualifies (same mailbox →
// the embedded message id is still valid after a re-auth); we never cross to a
// different known mailbox. If the dead identity is unknown (or there is no
// dead account), we fall back to the most recently connected ACTIVE account -
// in the common single-mailbox case that's the reconnect we want.
func bestActiveAccount(accounts []*Account, dead *Account) *Account {
	deadIdentity := ""
	if dead != nil {
		deadIdentity = strings.TrimSpace(dead.IdentityHint)
	}
	var newest *Account
	for _, a := range accounts {
		if !accountActive(a) {
			continue
		}
		if deadIdentity != "" {
			if strings.EqualFold(strings.TrimSpace(a.IdentityHint), deadIdentity) {
				return a
			}
			continue // known mailbox, but this active account is a different one
		}
		if newest == nil || a.CreatedAt.After(newest.CreatedAt) {
			newest = a
		}
	}
	return newest
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

// extractMessage walks a Gmail message resource (however Composio wraps it)
// and returns the text/html body, the plain-text body, and attachment
// filenames. Defensive against wrapper nesting (data / response_data /
// message) and multipart trees of arbitrary depth. Falls back to Composio's
// pre-decoded top-level `messageText` when the payload has no text/plain part.
func extractMessage(raw json.RawMessage) (html, text string, attachments []Attachment) {
	if len(raw) == 0 {
		return "", "", nil
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", "", nil
	}
	// Body: walk the MIME tree for html + plain parts.
	if payload := findPayload(root); payload != nil {
		htmlData, textData := walkParts(payload)
		html = decodeB64URL(htmlData)
		text = decodeB64URL(textData)
	}
	// Plain-text fallback: Composio surfaces a pre-decoded `messageText` at
	// the top level (and under common wrappers). Prefer the richer of the two.
	if strings.TrimSpace(text) == "" {
		text = strings.TrimSpace(messageTextField(root))
	}
	// Attachments: name + mime + id so an attachment-only email (empty body)
	// still shows what it carries, and the UI can download each one by id.
	attachments = attachmentMeta(root)
	return html, text, attachments
}

// messageTextField pulls Composio's convenience plain-text field, tolerating
// the same wrapper nesting as findPayload.
func messageTextField(m map[string]any) string {
	if m == nil {
		return ""
	}
	if s, ok := m["messageText"].(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	for _, key := range []string{"data", "response_data", "message", "messageData", "result"} {
		if nested, ok := m[key].(map[string]any); ok {
			if s := messageTextField(nested); s != "" {
				return s
			}
		}
	}
	return ""
}

// attachmentMeta extracts attachment {name, mime, id} from the message
// resource, tolerating wrapper nesting. Real (named) attachments only -
// inline parts without a filename are skipped. The id is the provider's
// internal attachment id, needed to download the bytes later.
func attachmentMeta(m map[string]any) []Attachment {
	if m == nil {
		return nil
	}
	var out []Attachment
	if list, ok := m["attachmentList"].([]any); ok {
		for _, a := range list {
			am, ok := a.(map[string]any)
			if !ok {
				continue
			}
			name, _ := am["filename"].(string)
			if strings.TrimSpace(name) == "" {
				continue
			}
			id, _ := am["attachmentId"].(string)
			mime, _ := am["mimeType"].(string)
			out = append(out, Attachment{
				Filename: strings.TrimSpace(name),
				MimeType: strings.TrimSpace(mime),
				ID:       strings.TrimSpace(id),
			})
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, key := range []string{"data", "response_data", "message", "messageData", "result"} {
		if nested, ok := m[key].(map[string]any); ok {
			if atts := attachmentMeta(nested); len(atts) > 0 {
				return atts
			}
		}
	}
	return nil
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
