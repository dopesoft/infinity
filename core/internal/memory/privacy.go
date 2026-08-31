package memory

import (
	"regexp"
	"strings"
)

// Secret regex patterns. Order matters - more specific first.
// Add new patterns here as new providers add new key formats.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-ant-[a-zA-Z0-9_\-]{16,}`),                                 // Anthropic
	regexp.MustCompile(`sk-[a-zA-Z0-9]{32,}`),                                        // OpenAI / generic
	regexp.MustCompile(`xoxb-[A-Za-z0-9\-]+`),                                        // Slack bot
	regexp.MustCompile(`xoxp-[A-Za-z0-9\-]+`),                                        // Slack user
	regexp.MustCompile(`ghp_[a-zA-Z0-9]{36,}`),                                       // GitHub PAT
	regexp.MustCompile(`gho_[a-zA-Z0-9]{36,}`),                                       // GitHub OAuth
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                                           // AWS access key
	regexp.MustCompile(`AIza[0-9A-Za-z\-_]{35}`),                                     // Google
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]+PRIVATE KEY-----.*?-----END [A-Z ]+PRIVATE KEY-----`),
	regexp.MustCompile(`Bearer [A-Za-z0-9_\-\.=]+`),                                  // Authorization headers
	// Browser-session cookies handed over for paywall-free reads (web-reach):
	// these are live credentials and must never persist in plaintext memory.
	regexp.MustCompile(`(?s)\{"url":"[^"]*cookie-editor[^"]*","version":\s*\d+,"data":"[A-Za-z0-9+/=]+"\}`), // Cookie-Editor encrypted export blob
	regexp.MustCompile(`(?i)auth_token=[a-f0-9]{30,}`),                               // Twitter/X session
	regexp.MustCompile(`(?i)ct0=[a-f0-9]{30,}`),                                      // Twitter/X CSRF
	regexp.MustCompile(`(?i)reddit_session=[^\s;"']+`),                               // Reddit session
	regexp.MustCompile(`(?i)token_v2=[^\s;"']+`),                                     // Reddit token
	regexp.MustCompile(`(?i)(TWITTER_AUTH_TOKEN|TWITTER_CT0|REDDIT_COOKIE)=\S+`),     // env-var forms
}

var privateTagPattern = regexp.MustCompile(`(?s)<private>.*?</private>`)

// panCandidate matches a 13-19 digit run allowing spaces or dashes between
// groups, which is how a card number appears on a page, in a form value, or
// read aloud. It is deliberately loose: the Luhn check below is what decides.
var panCandidate = regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`)

// cvcNear matches a security code only when a nearby word says that is what it
// is. Three or four bare digits are far too common to redact on sight, and
// over-redacting numbers would quietly corrupt ordinary memories.
var cvcNear = regexp.MustCompile(`(?i)\b(?:cvc|cvv|cvv2|csc|security\s+code|card\s+code)\b\W{0,12}(\d{3,4})\b`)

// luhn reports whether a digit string passes the checksum every real card
// number satisfies. This is what separates a card from an order id, a phone
// number, or a tracking code, so a PAN gets redacted and an invoice number
// survives.
func luhn(digits string) bool {
	sum, alt := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		c := digits[i]
		if c < '0' || c > '9' {
			return false
		}
		d := int(c - '0')
		if alt {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return len(digits) >= 13 && len(digits) <= 19 && sum%10 == 0
}

// stripCards removes card numbers and contextual security codes.
//
// This is not only for the checkout feature. browser_observe's output becomes
// an observation's raw_text, which is embedded and stored, so before this a
// card number typed into any page was shipped to the embedding provider and
// written to the memory graph in the clear. The phone path had a card regex
// for its transcripts; nothing else did.
func stripCards(text string) string {
	text = panCandidate.ReplaceAllStringFunc(text, func(m string) string {
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, m)
		if !luhn(digits) {
			return m // an order number, a tracking id, an ordinary long number
		}
		return "[REDACTED CARD]"
	})
	return cvcNear.ReplaceAllStringFunc(text, func(m string) string {
		i := strings.LastIndexFunc(m, func(r rune) bool { return r < '0' || r > '9' })
		return m[:i+1] + "[REDACTED]"
	})
}

// StripSecrets redacts known secret patterns and any <private>…</private>
// blocks. Returns the cleaned text plus a flag for whether redaction occurred.
func StripSecrets(text string) (string, bool) {
	original := text
	for _, p := range secretPatterns {
		text = p.ReplaceAllString(text, "[REDACTED]")
	}
	text = stripCards(text)
	text = privateTagPattern.ReplaceAllString(text, "[PRIVATE CONTENT REMOVED]")
	return text, text != original
}

// StripSecretsDeep redacts every string anywhere inside a decoded JSON value:
// map values, array elements, nested structures.
//
// The capture pipeline ran StripSecrets over an observation's text and then
// persisted its payload untouched, and the payload is where the verbatim tool
// INPUT lives. So a redacted transcript sat next to an unredacted copy of the
// same secret in the same row. Anything crossing into storage goes through
// here now, so "we redact observations" is true of the whole observation.
func StripSecretsDeep(v any) any {
	switch t := v.(type) {
	case string:
		out, _ := StripSecrets(t)
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = StripSecretsDeep(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = StripSecretsDeep(val)
		}
		return out
	default:
		return v
	}
}

// IsLikelySecret returns true if `s` looks like it contains a known secret.
// Used by hooks before persisting raw payloads.
func IsLikelySecret(s string) bool {
	for _, p := range secretPatterns {
		if p.MatchString(s) {
			return true
		}
	}
	return strings.Contains(s, "<private>")
}
