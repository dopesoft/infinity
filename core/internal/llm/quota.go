package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Plan-quota exhaustion (2026-08-26).
//
// The boss's ChatGPT Plus brain returned 429 `usage_limit_reached` (resets in
// 3.8h) and his Claude Max plan behind code_agent returned "You're out of
// extra usage". Both were treated as TRANSIENT: the OAuth provider retried the
// same dead request three times, three background builds kept hammering it,
// code_agent reported the Claude failures as ok, and the chat surfaced a
// "brief server hiccup" that was really a spent plan. A spent plan is a
// different thing from a hiccup: retrying is pointless until it resets, the
// boss needs to be told in those words, and a standby brain (an API key that
// is already wired) should carry the conversation meanwhile.
//
// This file is the one shared vocabulary for that: a typed error every
// provider returns instead of retrying, and a process-wide ledger of which
// providers are spent until when. failover.go reads the ledger to route.

// QuotaError is returned by a provider when the PLAN's allowance is spent
// (as opposed to a per-minute rate limit, which stays transient). ResetsAt is
// zero when the provider did not say.
type QuotaError struct {
	Provider string
	ResetsAt time.Time
	Detail   string
}

func (e *QuotaError) Error() string {
	// "usage limit reached" is the phrase errs.Humanize keys on; keep it.
	if e.ResetsAt.IsZero() {
		return fmt.Sprintf("%s: usage limit reached (%s)", e.Provider, e.Detail)
	}
	return fmt.Sprintf("%s: usage limit reached, resets %s (%s)", e.Provider, FormatLocalClock(e.ResetsAt), e.Detail)
}

// AsQuota unwraps err to a *QuotaError when it is one.
func AsQuota(err error) (*QuotaError, bool) {
	var q *QuotaError
	if errors.As(err, &q) && q != nil {
		return q, true
	}
	return nil, false
}

// defaultQuotaHold is how long a provider is treated as spent when it reports
// exhaustion without a reset time. Short on purpose: the next call after the
// hold re-probes the provider for real, so a wrong guess costs one request.
const defaultQuotaHold = 15 * time.Minute

type quotaEntry struct {
	until  time.Time
	detail string
}

var (
	quotaMu        sync.Mutex
	quotaExhausted = map[string]quotaEntry{}
	// quotaRecovered remembers providers whose hold expired so the next call
	// can announce "back on X" exactly once.
	quotaRecovered = map[string]bool{}
)

// MarkExhausted records that provider is spent until `until` (zero → the
// default hold). Returns true when this is a NEW exhaustion (not an extension
// of one already recorded), so callers announce the switch once.
func MarkExhausted(provider string, until time.Time, detail string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return false
	}
	if until.IsZero() || !until.After(time.Now()) {
		until = time.Now().Add(defaultQuotaHold)
	}
	quotaMu.Lock()
	defer quotaMu.Unlock()
	_, had := quotaExhausted[provider]
	quotaExhausted[provider] = quotaEntry{until: until, detail: detail}
	return !had
}

// ClearExhausted forgets a provider's hold (a successful call proves it back).
func ClearExhausted(provider string) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	quotaMu.Lock()
	defer quotaMu.Unlock()
	delete(quotaExhausted, provider)
	delete(quotaRecovered, provider)
}

// Exhausted reports whether provider is currently spent, and until when. An
// expired hold is dropped here and flagged for a one-time recovery notice.
func Exhausted(provider string) (until time.Time, detail string, active bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	quotaMu.Lock()
	defer quotaMu.Unlock()
	e, ok := quotaExhausted[provider]
	if !ok {
		return time.Time{}, "", false
	}
	if !e.until.After(time.Now()) {
		delete(quotaExhausted, provider)
		quotaRecovered[provider] = true
		return time.Time{}, "", false
	}
	return e.until, e.detail, true
}

// takeRecovered returns true exactly once after a provider's hold expired.
func takeRecovered(provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	quotaMu.Lock()
	defer quotaMu.Unlock()
	if quotaRecovered[provider] {
		delete(quotaRecovered, provider)
		return true
	}
	return false
}

// ResetQuotaLedgerForTest clears all state; tests only.
func ResetQuotaLedgerForTest() {
	quotaMu.Lock()
	defer quotaMu.Unlock()
	quotaExhausted = map[string]quotaEntry{}
	quotaRecovered = map[string]bool{}
}

// quotaFromOpenAIBody recognises the Codex backend's plan-exhaustion reply:
//
//	429 {"error":{"type":"usage_limit_reached","message":"The usage limit has
//	been reached","plan_type":"plus","resets_at":1787800427,...}}
//
// Anything else (a per-minute 429 without that type, a 5xx) stays transient.
func quotaFromOpenAIBody(provider string, status int, body string) (*QuotaError, bool) {
	if status != 429 {
		return nil, false
	}
	var payload struct {
		Error struct {
			Type     string `json:"type"`
			Message  string `json:"message"`
			PlanType string `json:"plan_type"`
			ResetsAt int64  `json:"resets_at"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, false
	}
	if payload.Error.Type != "usage_limit_reached" {
		return nil, false
	}
	q := &QuotaError{Provider: provider}
	if payload.Error.ResetsAt > 0 {
		q.ResetsAt = time.Unix(payload.Error.ResetsAt, 0)
	}
	plan := strings.TrimSpace(payload.Error.PlanType)
	if plan == "" {
		q.Detail = "the ChatGPT plan's usage allowance is spent"
	} else {
		q.Detail = "the ChatGPT " + plan + " plan's usage allowance is spent"
	}
	return q, true
}

// UserLocation is the boss's time zone (INFINITY_USER_TIMEZONE, IANA name;
// America/Chicago by default), the single frame every user-facing clock time
// in Core is spoken in.
func UserLocation() *time.Location {
	tz := strings.TrimSpace(os.Getenv("INFINITY_USER_TIMEZONE"))
	if tz == "" {
		tz = "America/Chicago"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

// FormatLocalClock renders t the way the boss reads times ("10:13pm"), adding
// the weekday when it is not today.
func FormatLocalClock(t time.Time) string {
	loc := UserLocation()
	t = t.In(loc)
	now := time.Now().In(loc)
	clock := strings.ToLower(t.Format("3:04pm"))
	if t.YearDay() != now.YearDay() || t.Year() != now.Year() {
		return t.Format("Mon ") + clock
	}
	return clock
}

// resetClockRe matches the reset clause vendors put in human copy, e.g. Claude
// Code's "You're out of extra usage · resets 6:50pm (America/Chicago)".
var resetClockRe = regexp.MustCompile(`(?i)resets?\s+(?:at\s+)?(\d{1,2}(?::\d{2})?\s*[ap]m)(?:\s*\(([^)]+)\))?`)

// ParseResetClock pulls a "resets 6:50pm (America/Chicago)" clause out of
// vendor copy and returns the next occurrence of that wall-clock time. ok is
// false when there is no such clause; the caller then uses the default hold.
func ParseResetClock(text string, now time.Time) (time.Time, bool) {
	m := resetClockRe.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}, false
	}
	loc := UserLocation()
	if m[2] != "" {
		if l, err := time.LoadLocation(strings.TrimSpace(m[2])); err == nil {
			loc = l
		}
	}
	clock := strings.ToLower(strings.ReplaceAll(m[1], " ", ""))
	layout := "3:04pm"
	if !strings.Contains(clock, ":") {
		layout = "3pm"
	}
	parsed, err := time.ParseInLocation(layout, clock, loc)
	if err != nil {
		return time.Time{}, false
	}
	local := now.In(loc)
	at := time.Date(local.Year(), local.Month(), local.Day(), parsed.Hour(), parsed.Minute(), 0, 0, loc)
	if !at.After(now) {
		at = at.Add(24 * time.Hour)
	}
	return at, true
}
