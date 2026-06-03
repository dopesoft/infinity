// Package inbox is the deterministic inbox-triage skill.
//
// This is the reference shape for every cron going forward (the boss's ask):
// a deterministic skill, NOT an agent tool-loading loop. The plumbing is plain
// Go — fetch, parse, surface — and the LLM is used for exactly ONE judgment
// (which emails need the boss's attention/reply), in a SINGLE batched call.
// That's why it's reliable: no skills_invoke dance, no per-tool load/decay, no
// loop guard, no 60 concurrent classify calls. Mirrors the africa CRM pattern.
//
//	fetch (broad, all active Gmail accounts)  → code
//	decide which need a reply (one LLM call)  → the brain (Settings model)
//	surface the winners via the surface store → code (Draft/Archive/Snooze + HTML)
package inbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/dopesoft/infinity/core/internal/connectors"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/surface"
)

// Deps are the deterministic building blocks the skill orchestrates. All are
// generic; none are triage-specific. Wired once in serve.go.
type Deps struct {
	Exec    *connectors.ExecuteClient
	Cache   *connectors.Cache
	Fetcher *connectors.MessageFetcher // for durable HTML body at surface time
	LLM     llm.Provider               // the boss's Settings model — the one judgment
	Surface *surface.Store
	Logger  *slog.Logger
}

// Config is the per-run tuning carried in the cron's target_config.
type Config struct {
	Query      string `json:"query"`       // Gmail search; broad on purpose — the LLM filters, not the query
	MaxResults int    `json:"max_results"` // per account
}

// Summary is the run report (lands on the cron's mem_runs row).
type Summary struct {
	Accounts int `json:"accounts"`
	Fetched  int `json:"fetched"`
	Surfaced int `json:"surfaced"`
}

type email struct {
	msgID     string
	from      string
	subject   string
	snippet   string
	accountID string
}

type decision struct {
	Index      int    `json:"index"`
	NeedsReply bool   `json:"needs_reply"`
	Reason     string `json:"reason"`
	Importance int    `json:"importance"`
}

// Run executes one triage pass. Deterministic end-to-end except the single
// batched classify call. Best-effort per account/email so one failure never
// aborts the rest.
func Run(ctx context.Context, d Deps, cfg Config) (Summary, error) {
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	if d.Exec == nil || d.Cache == nil || d.Surface == nil {
		return Summary{}, fmt.Errorf("inbox triage not configured (exec/cache/surface missing)")
	}
	if strings.TrimSpace(cfg.Query) == "" {
		cfg.Query = "in:inbox newer_than:3d"
	}
	if cfg.MaxResults <= 0 {
		cfg.MaxResults = 40
	}

	accounts := d.Cache.ActiveAccountsByToolkit("gmail")
	var emails []email
	for _, a := range accounts {
		resp, err := d.Exec.Execute(ctx, connectors.ExecuteRequest{
			Slug:               "GMAIL_FETCH_EMAILS",
			ConnectedAccountID: a.ID,
			UserID:             a.UserID, // entity — Composio 1811s without it
			Arguments: map[string]any{
				"query":       cfg.Query,
				"max_results": cfg.MaxResults,
				"verbose":     true,
			},
		})
		if err != nil {
			log.Warn("inbox triage fetch", "account", a.ID, "err", err)
			continue
		}
		if !resp.Successful {
			log.Warn("inbox triage fetch", "account", a.ID, "err", resp.Error)
			continue
		}
		emails = append(emails, parseEmails(resp.Data, a.ID)...)
	}

	sum := Summary{Accounts: len(accounts), Fetched: len(emails)}
	if len(emails) == 0 {
		return sum, nil
	}

	// The one judgment: which of these need the boss? One batched LLM call.
	decisions := d.classify(ctx, emails)

	for i, em := range emails {
		dec, ok := decisions[i]
		if !ok || !dec.NeedsReply {
			continue
		}
		it := &surface.Item{
			Surface:          "followups",
			Kind:             "email",
			Source:           "gmail",
			ExternalID:       em.msgID, // store canonicalizes to gmail:<id>, dedups on rerun
			Title:            firstNonEmpty(em.subject, "(no subject)"),
			Subtitle:         em.from,
			ImportanceReason: dec.Reason,
			Metadata:         map[string]any{"from": em.from, "account": em.accountID},
			Actions:          defaultEmailActions(),
		}
		if dec.Importance > 0 {
			imp := dec.Importance
			it.Importance = &imp
		}
		// NOTE: we deliberately do NOT fetch the HTML body here. Doing it inline,
		// per email, in a loop made the run hang for minutes on a parade of slow
		// Composio calls. The dashboard's /api/followups/message endpoint already
		// fetches + caches the full HTML lazily the first time the boss opens the
		// email, so the body is there when he needs it — and the run stays fast
		// (decision + upsert only, seconds not minutes).
		if _, err := d.Surface.Upsert(ctx, it); err != nil {
			log.Warn("inbox triage surface", "msg", em.msgID, "err", err)
			continue
		}
		sum.Surfaced++
	}
	return sum, nil
}

// classify makes ONE call to the Settings model and returns index→decision.
// Robust JSON extraction (the model may wrap in prose/fences). On any failure
// it returns an empty map — the run surfaces nothing rather than dumping junk.
func (d Deps) classify(ctx context.Context, emails []email) map[int]decision {
	out := map[int]decision{}
	if d.LLM == nil || len(emails) == 0 {
		return out
	}
	var b strings.Builder
	for i, em := range emails {
		fmt.Fprintf(&b, "%d. FROM: %s | SUBJECT: %s | SNIPPET: %s\n",
			i, oneLine(em.from), oneLine(em.subject), oneLine(truncate(em.snippet, 240)))
	}

	model := d.LLM.Model()
	stream := make(chan llm.StreamEvent, 16)
	respCh := make(chan llm.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := d.LLM.Stream(ctx, model, classifySystemPrompt,
			[]llm.Message{{Role: llm.RoleUser, Content: b.String()}}, nil, stream)
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()
	for range stream {
		// drain; we only want the final response
	}

	var resp llm.Response
	select {
	case resp = <-respCh:
	case <-errCh:
		return out
	case <-ctx.Done():
		return out
	}

	decisions := parseDecisions(resp.Text)
	for _, dec := range decisions {
		if dec.Index >= 0 && dec.Index < len(emails) {
			out[dec.Index] = dec
		}
	}
	return out
}

const classifySystemPrompt = `You are the boss's inbox triage. You are given a numbered list of recent emails (FROM, SUBJECT, SNIPPET).

Decide which ones genuinely need the BOSS'S personal attention or a reply. A human writing to him directly, a real question, something awaiting his decision = needs_reply true. Marketing, newsletters, promotions, receipts, automated notifications, "no-reply" senders, social updates = needs_reply false.

Return STRICT JSON ONLY — a single array, no prose, no markdown fences — with one object per email:

[{"index": <int>, "needs_reply": <bool>, "reason": "<one short line on why>", "importance": <0-100>}]

Include EVERY index. importance: 80+ urgent/personal, 50-79 notable, <50 routine. Be strict: when in doubt that a human is actually waiting on him, set needs_reply false.`

// parseEmails extracts messages from a GMAIL_FETCH_EMAILS response (loose shape,
// same fields the connector poller reads).
func parseEmails(raw json.RawMessage, accountID string) []email {
	if len(raw) == 0 {
		return nil
	}
	var env struct {
		Messages []map[string]any `json:"messages"`
		Items    []map[string]any `json:"items"`
		Results  []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil
	}
	items := env.Messages
	if len(items) == 0 {
		items = env.Items
	}
	if len(items) == 0 {
		items = env.Results
	}
	out := make([]email, 0, len(items))
	for _, m := range items {
		id := mapString(m, "messageId", "id", "message_id", "remote_id")
		if id == "" {
			continue
		}
		out = append(out, email{
			msgID:     id,
			from:      mapString(m, "sender", "from", "from_name"),
			subject:   mapString(m, "subject", "title"),
			snippet:   firstNonEmpty(mapString(m, "preview", "snippet"), mapString(m, "messageText", "body", "text")),
			accountID: accountID,
		})
	}
	return out
}

func parseDecisions(text string) []decision {
	text = strings.TrimSpace(text)
	// Extract the JSON array even if wrapped in prose/fences.
	if i := strings.Index(text, "["); i >= 0 {
		if j := strings.LastIndex(text, "]"); j > i {
			text = text[i : j+1]
		}
	}
	var out []decision
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil
	}
	return out
}

// defaultEmailActions mirrors tools.defaultEmailActions — the one-tap controls
// every surfaced email gets. Kept in sync intentionally; the intents are read
// by /api/surface/action which runs them as an agent turn (Draft never sends).
func defaultEmailActions() []surface.Action {
	return []surface.Action{
		{ID: "draft_reply", Label: "Draft reply", Style: "primary",
			Intent: "Draft a reply to this email for the boss to review and edit. Create it ONLY as a Gmail draft on the same thread — do NOT send it. When the draft is saved, surface_update this item so the boss can see a reply is ready to review."},
		{ID: "archive", Label: "Archive", Style: "default",
			Intent: "Archive this email in Gmail (remove it from the inbox, keep it in All Mail), then mark this follow-up done via surface_update."},
		{ID: "snooze", Label: "Snooze 1d", Style: "default",
			Intent: "Snooze this follow-up for one day: surface_update its snoozed_until to ~24h from now so it drops off the dashboard and resurfaces tomorrow."},
	}
}

func mapString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
