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

// StripSecrets redacts known secret patterns and any <private>…</private>
// blocks. Returns the cleaned text plus a flag for whether redaction occurred.
func StripSecrets(text string) (string, bool) {
	original := text
	for _, p := range secretPatterns {
		text = p.ReplaceAllString(text, "[REDACTED]")
	}
	text = privateTagPattern.ReplaceAllString(text, "[PRIVATE CONTENT REMOVED]")
	return text, text != original
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
