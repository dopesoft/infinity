// Package triage classifies inbound follow-ups (email, Slack, etc) so the
// dashboard chips (intent / mode / classification) populate themselves as
// part of the existing connector poll instead of needing a separate cron
// or a manual "triage my inbox" recipe.
//
// Shape mirrors core/internal/intent.Detector:
//   - A small wrapper around an llm.Provider configured for a fast model
//     (Haiku by default).
//   - A strict JSON-output prompt so parsing is deterministic.
//   - Fail-closed: any error returns an empty Decision and the caller
//     persists nothing - chips just don't appear for that row.
//
// Per CLAUDE.md Rule #1 the cognition is small and load-bearing-only;
// when we have real labelled data we can move this to a fine-tuned
// classifier or push the prompt into a default skill body. The interface
// stays the same.
package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// Decision is the structured chip metadata the dashboard renders.
//
// `Intent` is the urgency / what-the-message-wants signal (Studio
// renders "needs reply" amber, "fyi" blue, "urgent" red, etc).
// `Mode` is the recommended boss action (reply / read / skim / ignore).
// `Classification` is a free-form category for archiving / search
// ("newsletter", "personal", "automated", "transaction", ...).
type Decision struct {
	Intent         string `json:"intent"`
	Mode           string `json:"mode"`
	Classification string `json:"classification"`
}

// Triager runs a single Haiku classification per follow-up. Cheap to
// construct; callers usually keep one process-wide instance and call
// Classify asynchronously from the connector poller.
type Triager struct {
	provider llm.Provider
	model    string
}

type Config struct {
	Provider llm.Provider
	Model    string
}

// New builds a Triager. Returns nil when no provider is configured so
// callers can degrade gracefully (the chips just don't populate, but the
// row still lands on the dashboard).
//
// Model resolution: an explicit cfg.Model wins (the INFINITY_TRIAGE_MODEL
// override). When it's empty the Triager uses the provider's CURRENTLY
// SELECTED model at call time (see Classify) - i.e. whatever the boss
// picked in Settings. We deliberately do NOT pin Haiku here: classification
// is cheap, but the boss pays per inbound email and would rather spend his
// selected model than a hardcoded one. Set INFINITY_TRIAGE_MODEL only to
// opt back into a dedicated cheap classifier.
func New(cfg Config) *Triager {
	if cfg.Provider == nil {
		return nil
	}
	return &Triager{
		provider: cfg.Provider,
		model:    strings.TrimSpace(cfg.Model),
	}
}

// Subject + From + Body summarise the message to classify. Body is
// truncated to keep the prompt cheap; the classification doesn't need
// the full text of a 50KB newsletter to decide what category it is.
const bodyTruncate = 1200

func (t *Triager) Classify(ctx context.Context, subject, from, body string) Decision {
	if t == nil || t.provider == nil {
		return Decision{}
	}

	body = strings.TrimSpace(body)
	if len(body) > bodyTruncate {
		body = body[:bodyTruncate] + "…"
	}

	prompt := fmt.Sprintf(
		"FROM: %s\nSUBJECT: %s\nBODY:\n%s",
		safe(from), safe(subject), safe(body),
	)

	// Resolve the model at call time: explicit override wins, otherwise
	// track the provider's currently-selected model so a Settings change
	// propagates here without restarting the poller.
	model := t.model
	if model == "" {
		model = t.provider.Model()
	}

	out := make(chan llm.StreamEvent, 16)
	respCh := make(chan llm.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := t.provider.Stream(ctx, model, systemPrompt,
			[]llm.Message{{Role: llm.RoleUser, Content: prompt}},
			nil, out)
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()
	for range out {
		// drain stream; we only want the final response
	}

	var resp llm.Response
	select {
	case resp = <-respCh:
	case <-errCh:
		return Decision{}
	case <-ctx.Done():
		return Decision{}
	}

	d, err := parse(resp.Text)
	if err != nil {
		return Decision{}
	}
	return d
}

const systemPrompt = `You triage incoming personal messages (email, Slack, etc) for the boss's dashboard.

Return STRICT JSON only, no prose, no markdown fences, with EXACTLY these three keys:

  {
    "intent": "<one of: needs reply | fyi | urgent | question | scheduling | done>",
    "mode":   "<one of: reply | read | skim | ignore>",
    "classification": "<one of: personal | work | newsletter | promotion | transaction | notification | automated | spam | other>"
  }

Rules:
- "intent" describes what the message WANTS from the boss. "needs reply" when the sender is waiting on an answer; "urgent" when there's a real deadline / fire; "fyi" when it's informational only; "question" when there's an explicit ask; "scheduling" when it's about times/meetings; "done" when it's a confirmation/closing message.
- "mode" is the recommended action. Prefer "skim" or "ignore" for newsletters/promotions/automated notifications.
- "classification" is the message category. Bias towards "newsletter" / "promotion" / "automated" / "notification" when the From or content looks templated; reserve "personal" for one-to-one human messages and "work" for work-context exchanges.
- If genuinely uncertain pick the safest default ("fyi" / "skim" / "other").
- NEVER output any other keys, never include explanations, never wrap in code fences.`

func parse(text string) (Decision, error) {
	text = strings.TrimSpace(text)
	// Models sometimes wrap JSON in ```json fences despite instruction.
	if strings.HasPrefix(text, "```") {
		if i := strings.Index(text, "\n"); i >= 0 {
			text = text[i+1:]
		}
		if i := strings.LastIndex(text, "```"); i >= 0 {
			text = text[:i]
		}
		text = strings.TrimSpace(text)
	}
	var d Decision
	if err := json.Unmarshal([]byte(text), &d); err != nil {
		return Decision{}, err
	}
	d.Intent = strings.TrimSpace(strings.ToLower(d.Intent))
	d.Mode = strings.TrimSpace(strings.ToLower(d.Mode))
	d.Classification = strings.TrimSpace(strings.ToLower(d.Classification))
	if d.Intent == "" && d.Mode == "" && d.Classification == "" {
		return Decision{}, fmt.Errorf("triage returned empty decision")
	}
	return d, nil
}

func safe(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(none)"
	}
	return s
}
