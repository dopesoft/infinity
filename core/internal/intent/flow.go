// Package intent implements IntentFlow - streaming demand detection.
//
// Every user observation runs through Classify which returns one of three
// control tokens:
//
//   • silent             - wait, the user is mid-thought
//   • fast_intervention  - low-latency understanding help (<1s response)
//   • full_assistance    - invoke memory + skills + take action
//
// The classifier is a prompt-engineered Claude Haiku call. It can be migrated
// to a fine-tuned model later once labelled data exists; the Provider/Decision
// surface stays stable across that migration.
package intent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// Token is one of three control tokens. Match the schema CHECK constraint.
type Token string

const (
	TokenSilent          Token = "silent"
	TokenFastIntervention Token = "fast_intervention"
	TokenFullAssistance  Token = "full_assistance"
)

func (t Token) Valid() bool {
	switch t {
	case TokenSilent, TokenFastIntervention, TokenFullAssistance:
		return true
	}
	return false
}

// Decision is the structured output of the classifier.
type Decision struct {
	Token            Token   `json:"token"`
	Confidence       float64 `json:"confidence"`
	Reason           string  `json:"reason"`
	SuggestedAction  string  `json:"suggested_action,omitempty"`
}

// Recall depth maps from a token to the memory tier we should access.
type Depth string

const (
	DepthNone      Depth = "none"
	DepthWorkspace Depth = "workspace"
	DepthUser      Depth = "user"
	DepthGlobal    Depth = "global"
)

// DepthFor maps a token to the deepest layer of memory we'll touch.
//   silent → none
//   fast   → workspace only
//   full   → user + global
func DepthFor(t Token) Depth {
	switch t {
	case TokenFastIntervention:
		return DepthWorkspace
	case TokenFullAssistance:
		return DepthGlobal
	}
	return DepthNone
}

// Detector classifies user input. It uses an LLM provider configured for a
// fast model - typically claude-haiku - and a strict JSON output prompt.
type Detector struct {
	provider llm.Provider
	model    string
	thresholdFast float64
	thresholdFull float64
	quietHours    *QuietHours

	mu sync.Mutex
}

type Config struct {
	Provider       llm.Provider
	Model          string
	ThresholdFast  float64
	ThresholdFull  float64
	QuietHours     *QuietHours
}

type QuietHours struct {
	Enabled  bool
	StartUTC int // 0-23
	EndUTC   int // 0-23
}

// IsQuiet returns true if the current UTC hour falls in the configured quiet
// window. Quiet hours force the decision to <silent> regardless of confidence.
func (q *QuietHours) IsQuiet(now time.Time) bool {
	if q == nil || !q.Enabled {
		return false
	}
	h := now.UTC().Hour()
	if q.StartUTC == q.EndUTC {
		return false
	}
	if q.StartUTC < q.EndUTC {
		return h >= q.StartUTC && h < q.EndUTC
	}
	// wrap (e.g. 22 → 6)
	return h >= q.StartUTC || h < q.EndUTC
}

// New builds a Detector with sane defaults.
func New(cfg Config) *Detector {
	if cfg.ThresholdFast <= 0 {
		cfg.ThresholdFast = 0.7
	}
	if cfg.ThresholdFull <= 0 {
		cfg.ThresholdFull = 0.85
	}
	if cfg.Model == "" {
		cfg.Model = "claude-haiku-4-5-20251001"
	}
	return &Detector{
		provider:      cfg.Provider,
		model:         cfg.Model,
		thresholdFast: cfg.ThresholdFast,
		thresholdFull: cfg.ThresholdFull,
		quietHours:    cfg.QuietHours,
	}
}

// Classify runs the LLM call. It returns a Decision and never an error from
// the LLM - we degrade to <silent> on any failure (fail-closed for proactive
// behaviour).
//
// recentContext should be the last few user/assistant turns concatenated; it
// disambiguates "the meeting is at 3pm" from a one-off statement.
func (d *Detector) Classify(ctx context.Context, observation, recentContext string) Decision {
	if d == nil || d.provider == nil {
		return Decision{Token: TokenSilent, Confidence: 0, Reason: "detector unconfigured"}
	}
	if d.quietHours.IsQuiet(time.Now()) {
		return Decision{Token: TokenSilent, Confidence: 1.0, Reason: "quiet hours"}
	}

	prompt := buildPrompt(observation, recentContext)
	systemPrompt := classifierSystem

	out := make(chan llm.StreamEvent, 16)
	respCh := make(chan llm.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		// IntentFlow uses the provider's default model - the boss's per-turn
		// override only applies to the main agent loop, not internal Haiku-style
		// classifiers.
		resp, err := d.provider.Stream(ctx, "", systemPrompt,
			[]llm.Message{{Role: llm.RoleUser, Content: prompt}},
			nil, out)
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	for range out {
		// drain stream - we only care about the final response
	}

	var resp llm.Response
	select {
	case resp = <-respCh:
	case err := <-errCh:
		return Decision{Token: TokenSilent, Reason: "llm error: " + err.Error()}
	case <-ctx.Done():
		return Decision{Token: TokenSilent, Reason: "ctx done"}
	}

	dec, err := parseDecision(resp.Text)
	if err != nil {
		return Decision{Token: TokenSilent, Reason: "parse error: " + err.Error()}
	}
	dec = d.applyThresholds(dec)
	return dec
}

func (d *Detector) applyThresholds(dec Decision) Decision {
	switch dec.Token {
	case TokenFullAssistance:
		if dec.Confidence < d.thresholdFull {
			dec.Token = TokenFastIntervention
		}
	case TokenFastIntervention:
		if dec.Confidence < d.thresholdFast {
			dec.Token = TokenSilent
		}
	}
	return dec
}

const classifierSystem = `You classify whether the assistant should intervene right now.

Output strict JSON with these keys:
- token: one of "silent" | "fast_intervention" | "full_assistance"
- confidence: 0.0-1.0
- reason: one short sentence
- suggested_action: optional one short imperative

Use:
- silent: user mid-thought; input doesn't suggest a need; recent intervention covered it; or unclear.
- fast_intervention: low-latency understanding help. Surfacing a quick fact, a number, a time, a memory. No tools.
- full_assistance: invoke memory, run skills, take action. Use when intent is clear and value is high.

Output ONLY the JSON object, no other text.`

func buildPrompt(observation, recentContext string) string {
	var b strings.Builder
	if recentContext != "" {
		b.WriteString("Recent context:\n")
		b.WriteString(recentContext)
		b.WriteString("\n\n")
	}
	b.WriteString("Latest observation:\n")
	b.WriteString(observation)
	return b.String()
}

func parseDecision(raw string) (Decision, error) {
	var d Decision
	raw = strings.TrimSpace(raw)
	// Strip optional code fence.
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimPrefix(raw, "```")
		raw = strings.TrimSuffix(raw, "```")
		raw = strings.TrimSpace(raw)
	}
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return d, err
	}
	if !d.Token.Valid() {
		return d, fmt.Errorf("invalid token %q", d.Token)
	}
	if d.Confidence < 0 {
		d.Confidence = 0
	} else if d.Confidence > 1 {
		d.Confidence = 1
	}
	return d, nil
}
