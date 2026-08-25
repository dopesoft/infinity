package surface

import (
	"context"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// body_capture.go — the durable-message-body MECHANIC.
//
// Rule #1b says a mechanic must be guaranteed by code that runs regardless of
// which producer wrote the row. This one wasn't. The capture lived inside the
// `surface_item` TOOL, but the live path that actually surfaces the boss's mail
// is the deterministic Go triage in core/internal/inbox/triage.go, which calls
// Store.Upsert directly. So the mechanic never ran on the path that mattered:
// on 2026-08-25, 356 of 390 open follow-up emails had no stored body at all,
// and capture had been at ZERO for three straight weeks. Every one of those
// depended on the boss opening the card before the connector was revoked. If he
// didn't, the email was simply gone.
//
// The fix is the one the codebase already uses for cross-cutting mechanics
// (httpx.InstallDefault): install ONE fetcher at boot, and have the single
// chokepoint every one of the 18 producers flows through — Store.Upsert — do
// the capture. A new producer gets it by construction, with nothing to remember.

// MessageBodyFetcher retrieves a message's rendered body. Deliberately narrower
// than connectors.MessageFetcher (no attachments) so this package stays free of
// connector types and any vendor concept: it asks "give me the body for this
// id", never "call Gmail".
type MessageBodyFetcher interface {
	FetchMessageBody(ctx context.Context, source, accountHint, messageID string) (html, text string, err error)
}

var (
	bodyFetcherMu sync.RWMutex
	bodyFetcher   MessageBodyFetcher
)

// SetBodyFetcher installs the process-wide body fetcher. Called once at boot,
// alongside the dashboard's lazy fetcher, so every Store instance captures.
func SetBodyFetcher(f MessageBodyFetcher) {
	bodyFetcherMu.Lock()
	bodyFetcher = f
	bodyFetcherMu.Unlock()
}

// BodyFetcher returns the installed fetcher (nil when Composio isn't wired).
func BodyFetcher() MessageBodyFetcher {
	bodyFetcherMu.RLock()
	defer bodyFetcherMu.RUnlock()
	return bodyFetcher
}

// captureTimeout bounds one message fetch so a slow or hanging connector can
// never stall the triage run that's surfacing the email.
const captureTimeout = 20 * time.Second

// infoLog writes successes to stdout; Railway tags stderr as severity=error and
// a captured body is not an error.
var infoLog = log.New(os.Stdout, "", log.LstdFlags)

// IsMessageItem reports whether an item is a message a person sent the boss —
// the class whose real body must be captured durably. Mirrors the dashboard's
// read filter so capture and render agree on exactly which rows are messages.
func IsMessageItem(surfaceName, kind string) bool {
	switch strings.ToLower(strings.TrimSpace(surfaceName)) {
	case "followups", "inbox", "email":
		return strings.ToLower(strings.TrimSpace(kind)) == "email"
	}
	return false
}

// captureBody fills it.CachedHTML / it.CachedText with the REAL message body,
// and derives the list preview from it. Best-effort by design: a failure leaves
// whatever the producer supplied and is recorded on the row so it is visible
// and retryable, never silent.
//
// The fetched body always WINS over anything a recipe passed. A model asked to
// summarize will sometimes hand its summary over as the body; the Message pane
// must show the actual email, and the summary keeps its own place in `body`
// (the Context pane).
func (s *Store) captureBody(ctx context.Context, it *Item) {
	f := BodyFetcher()
	if f == nil || it == nil || it.ExternalID == "" || !IsMessageItem(it.Surface, it.Kind) {
		return
	}
	// Already captured on a previous run? Message bodies are immutable, so a
	// re-fetch buys nothing and costs a connector call. Triage re-surfaces the
	// same open mail on every pass, so without this the capture would re-pull
	// every open email several times a day for no reason.
	if s.hasStoredBody(ctx, it.Source, it.ExternalID) {
		return
	}
	fctx, cancel := context.WithTimeout(ctx, captureTimeout)
	defer cancel()

	html, text, err := f.FetchMessageBody(fctx, it.Source, MetaAccountHint(it.Metadata), it.ExternalID)
	if err != nil {
		// A failure to capture the boss's own mail is a real failure, not
		// noise. Name it on stderr AND on the row: the backfill sweep reads
		// these stamps to retry, and to stop retrying an account that is
		// genuinely gone instead of hammering it forever.
		s.stampCaptureFailure(it, err.Error())
		log.Printf("surface: body capture failed: id=%s source=%s err=%v", it.ExternalID, it.Source, err)
		return
	}
	if strings.TrimSpace(html) == "" && strings.TrimSpace(text) == "" {
		// Resolvable but empty. Distinguish it from a hard failure so the
		// backfill doesn't spin on a message that genuinely has no body.
		s.stampCaptureFailure(it, "upstream returned an empty body")
		return
	}
	it.CachedHTML = html
	it.CachedText = text
	applyBodyDerived(it, text, html)
	clearCaptureFailure(it)
}

// hasStoredBody reports whether this row already holds a captured message.
// Indexed lookup on the same (source, external_id) key the upsert uses. A
// query error answers false: capturing twice is wasteful, but skipping a
// capture because a lookup blipped would leave the card empty, which is the
// failure this whole mechanic exists to prevent.
func (s *Store) hasStoredBody(ctx context.Context, source, externalID string) bool {
	if s == nil || s.pool == nil {
		return false
	}
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(cached_html,'') <> '' OR COALESCE(cached_text,'') <> ''
		  FROM mem_surface_items
		 WHERE source = $1 AND external_id = $2
		 LIMIT 1
	`, source, externalID).Scan(&exists)
	if err != nil {
		return false
	}
	return exists
}

// applyBodyDerived fills the fields the boss SEES from the body we just
// captured, so a card is never a sender name with nothing under it:
//   - metadata.preview → the one-line snippet under the subject in the list.
//   - body             → the Context pane, when the producer left it empty.
//
// Both are deterministic slices of the real message. Nothing is invented, and
// an existing producer-authored value is never overwritten.
func applyBodyDerived(it *Item, text, html string) {
	snippet := Snippet(text, html, 240)
	if snippet == "" {
		return
	}
	if it.Metadata == nil {
		it.Metadata = map[string]any{}
	}
	if v, ok := it.Metadata["preview"].(string); !ok || strings.TrimSpace(v) == "" {
		it.Metadata["preview"] = snippet
	}
	if strings.TrimSpace(it.Body) == "" {
		it.Body = Snippet(text, html, 600)
	}
}

func (s *Store) stampCaptureFailure(it *Item, reason string) {
	if it.Metadata == nil {
		it.Metadata = map[string]any{}
	}
	attempts := 0
	switch v := it.Metadata["body_fetch_attempts"].(type) {
	case float64:
		attempts = int(v)
	case int:
		attempts = v
	}
	it.Metadata["body_fetch_attempts"] = attempts + 1
	it.Metadata["body_fetch_error"] = truncate(reason, 300)
	it.Metadata["body_fetch_last_try"] = time.Now().UTC().Format(time.RFC3339)
}

func clearCaptureFailure(it *Item) {
	if it.Metadata == nil {
		return
	}
	delete(it.Metadata, "body_fetch_error")
	delete(it.Metadata, "body_fetch_attempts")
	delete(it.Metadata, "body_fetch_last_try")
}

// MetaAccountHint pulls the connector account a producer stashed in metadata so
// the fetcher knows which mailbox to read. Exported because the backfill sweep
// resolves the same hint off a stored row.
func MetaAccountHint(m map[string]any) string {
	if m == nil {
		return ""
	}
	for _, k := range []string{"connected_account_id", "account", "mailbox"} {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Snippet renders a readable one-liner from a message body: prefers the plain
// text, falls back to stripping tags off the HTML, collapses whitespace, and
// cuts on a word boundary.
func Snippet(text, html string, max int) string {
	s := strings.TrimSpace(text)
	if s == "" {
		s = stripTags(html)
	}
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexAny(cut, " \t"); i > max/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " .,;:") + "…"
}

// stripTags removes markup and the script/style blocks whose contents would
// otherwise read as gibberish in a preview. Not a sanitizer: the output is only
// ever rendered as TEXT (the HTML body is stored separately and rendered by the
// viewer, which does its own sanitizing).
func stripTags(html string) string {
	if strings.TrimSpace(html) == "" {
		return ""
	}
	s := html
	for _, tag := range []string{"script", "style", "head"} {
		for {
			lo := strings.Index(strings.ToLower(s), "<"+tag)
			if lo < 0 {
				break
			}
			hi := strings.Index(strings.ToLower(s[lo:]), "</"+tag+">")
			if hi < 0 {
				s = s[:lo]
				break
			}
			s = s[:lo] + s[lo+hi+len(tag)+3:]
		}
	}
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
			b.WriteRune(' ')
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	out := b.String()
	for _, ent := range [][2]string{
		{"&nbsp;", " "}, {"&amp;", "&"}, {"&lt;", "<"}, {"&gt;", ">"},
		{"&quot;", `"`}, {"&#39;", "'"}, {"&zwnj;", ""}, {"&#8203;", ""},
	} {
		out = strings.ReplaceAll(out, ent[0], ent[1])
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
