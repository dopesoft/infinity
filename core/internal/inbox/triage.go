// honesty-machinery: do-not-revert — the triage truthfulness gate (blind run ≠
// "no new mail"). See the CLAUDE.md self-healing hard rule + memory project_sentry_blind_revert.
// Auto-rollback paths (sentry, verify cron, revert-veto) MUST never revert this.

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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dopesoft/infinity/core/internal/connectors"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/plan"
	"github.com/dopesoft/infinity/core/internal/surface"
)

// planTitle is the headline of the visible step plan + matches the cron name so
// the run card and the plan card read the same thing.
const planTitle = "Inbox triage"

// Deps are the deterministic building blocks the skill orchestrates. All are
// generic; none are triage-specific. Wired once in serve.go.
type Deps struct {
	Exec    *connectors.ExecuteClient
	Cache   *connectors.Cache
	Fetcher *connectors.MessageFetcher // for durable HTML body at surface time
	LLM     llm.Provider               // the boss's Settings model — the one judgment
	Surface *surface.Store
	Plan    *plan.Store // visible step timeline (Read → Decide → Surface)
	Pool    *pgxpool.Pool
	Logger  *slog.Logger
}

// Config is the per-run tuning carried in the cron's target_config.
type Config struct {
	Query      string `json:"query"`       // Gmail search; broad on purpose — the LLM filters, not the query
	MaxResults int    `json:"max_results"` // per account
	// SessionID is the run's session id, minted by the scheduler and already
	// stamped on the run's mem_runs row. We reuse it as the step plan's
	// session_id so the board folds the running cron + its plan into one live
	// step-timeline card. Empty (e.g. an ad-hoc call) → we mint our own.
	SessionID string `json:"-"`
}

// Summary is the run report (lands on the cron's mem_runs row).
type Summary struct {
	Accounts  int      `json:"accounts"`
	Fetched   int      `json:"fetched"`
	Surfaced  int      `json:"surfaced"`
	Examples  []string `json:"examples,omitempty"`   // "Sender about \"Subject\"" for surfaced mail — the substance the boss actually wants
	SessionID string   `json:"session_id,omitempty"` // ties the cron run card to the step plan
}

// Human renders the run report the way Jarvis would say it to the boss —
// naming actual senders and subjects when mail was flagged, never bare
// counters ("Triaged 12 email(s) across 4 mailbox(es)" is dev-speak).
func (s Summary) Human() string {
	box := "your inbox"
	if s.Accounts > 1 {
		box = fmt.Sprintf("your %d mailboxes", s.Accounts)
	}
	switch {
	case s.Fetched == 0:
		return "I checked " + box + " and there was no new mail."
	case s.Surfaced == 0:
		return fmt.Sprintf("I read %s in %s. None of them need a reply from you.", nEmails(s.Fetched), box)
	default:
		flagged := fmt.Sprintf("%d that need", s.Surfaced)
		if s.Surfaced == 1 {
			flagged = "1 that needs"
		}
		msg := fmt.Sprintf("I read %s in %s and flagged %s your reply", nEmails(s.Fetched), box, flagged)
		if len(s.Examples) > 0 {
			ex := s.Examples
			if len(ex) > 2 {
				ex = ex[:2]
			}
			msg += ", including " + strings.Join(ex, " and ")
		}
		msg += ". They are waiting in your Follow-ups."
		return msg
	}
}

func nEmails(n int) string {
	if n == 1 {
		return "1 new email"
	}
	return fmt.Sprintf("%d new emails", n)
}

// senderName trims "Namecheap Support <support@namecheap.com>" to the human
// part, falling back to the raw address.
func senderName(from string) string {
	if i := strings.IndexByte(from, '<'); i > 0 {
		if name := strings.Trim(strings.TrimSpace(from[:i]), `"'`); name != "" {
			return name
		}
	}
	return strings.Trim(strings.TrimSpace(from), "<>")
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
		cfg.MaxResults = 20
	}
	if cfg.MaxResults > 20 {
		// GMAIL_FETCH_EMAILS 413s ("payload too large") on a big inbox above
		// ~25 messages; cap well under that. The LLM is the filter, not volume.
		cfg.MaxResults = 20
	}

	// Visible step timeline — the SAME RUNNING-card plan UI the boss liked: this
	// skill writes a real mem_plans row with steps, so PlanTimeline renders the
	// step list, live statuses, and a red FAILED step if one breaks. Not a new
	// UI — we feed the existing one. Best-effort; a nil plan store just skips it.
	var sum Summary
	var s1, s2, s3 string
	if d.Plan != nil {
		d.sweepStalePlans(ctx) // clear any prior orphaned inbox plan — no pile-up
		// Reuse the scheduler's session id (already on the run row) so the board
		// folds run + plan into one live card; mint one only for ad-hoc callers.
		sid := strings.TrimSpace(cfg.SessionID)
		if sid == "" {
			sid = uuid.NewString()
		}
		pl, err := d.Plan.Create(ctx, sid, planTitle,
			"Read your inboxes, decide which emails need your reply, and surface those.", "",
			[]plan.NewStepInput{
				{Title: "Read your inboxes"},
				{Title: "Decide what needs your reply"},
				{Title: "Surface reply-worthy emails"},
			})
		if err != nil {
			// Don't swallow it — a silent create failure is why a running triage
			// card had no step timeline. Log loud; the run still proceeds.
			log.Error("inbox triage: step plan create failed; running without a step timeline", "err", err)
		} else if len(pl.Steps) >= 3 {
			sum.SessionID = sid
			s1, s2, s3 = pl.Steps[0].ID, pl.Steps[1].ID, pl.Steps[2].ID
		}
	}
	mark := func(id, status, summary string) {
		if d.Plan != nil && id != "" {
			_, _ = d.Plan.MarkStep(ctx, id, status, summary)
		}
	}

	// Step 1 — read the inboxes (broad fetch; the LLM is the filter, not the query).
	mark(s1, plan.StepInProgress, "")
	accounts := d.Cache.ActiveAccountsByToolkit("gmail")
	// A BLIND run (no mailbox reachable) is NOT an empty inbox, and must never be
	// reported as one. Returning a friendly "no new mail" when we actually
	// couldn't see the inbox is what silently hid a multi-day Composio 401 from
	// the boss — the run logged ok/nothing_needed and never surfaced or pinged.
	// So we count REAL fetch successes: only when at least one mailbox answered
	// can a zero result mean "genuinely empty". Zero successful fetches ⇒ fail
	// loud (return an error) so classifyOutcome marks the run failed → it lands
	// in "Surfaced by Jarvis", pings, and gives self-heal a real signal to fix.
	var emails []email
	fetchOK := 0
	var lastErr string
	for _, a := range accounts {
		resp, err := fetchEmailBatch(ctx, d.Exec, a, cfg)
		if err != nil {
			lastErr = err.Error()
			log.Error("inbox triage fetch", "account", a.ID, "err", err)
			d.markCoverage(ctx, "gmail", a, "error", err.Error())
			continue
		}
		if !resp.Successful {
			lastErr = resp.Error
			log.Error("inbox triage fetch", "account", a.ID, "err", resp.Error)
			d.markCoverage(ctx, "gmail", a, "error", resp.Error)
			continue
		}
		fetchOK++
		// Per-MAILBOX coverage (not just per-cron status): records that THIS
		// account was actually scanned, so ConnectorCoverageChecklist can see a
		// silently-dropped mailbox the same day. The migration intended the recipe
		// to write this; the deterministic Go path never did (Rule #1c).
		d.markCoverage(ctx, "gmail", a, "ok", "")
		emails = append(emails, parseEmails(resp.Data, a.ID)...)
	}
	sum.Accounts = len(accounts)
	sum.Fetched = len(emails)
	mailboxes := "your inbox"
	if len(accounts) > 1 {
		mailboxes = fmt.Sprintf("your %d mailboxes", len(accounts))
	}

	// Blind: not a single mailbox could be read. Surface it as a failure, never
	// as silence.
	if fetchOK == 0 {
		var reason string
		switch {
		case len(accounts) == 0:
			reason = "no active Gmail connection — reconnect Gmail in Settings → Connectors"
		case lastErr != "":
			reason = "every mailbox fetch failed upstream: " + truncate(lastErr, 200)
		default:
			reason = "every mailbox fetch failed upstream"
		}
		mark(s1, plan.StepFailed, "I couldn't read "+mailboxes+" — "+reason+".")
		mark(s2, plan.StepSkipped, "Skipped — I couldn't read the inbox.")
		mark(s3, plan.StepSkipped, "Skipped — I couldn't read the inbox.")
		return sum, fmt.Errorf("inbox triage could not reach any mailbox: %s", reason)
	}

	if len(emails) == 0 {
		mark(s1, plan.StepDone, "No new mail in "+mailboxes+".")
		mark(s2, plan.StepDone, "Nothing to decide.")
		mark(s3, plan.StepDone, "Nothing to surface.")
		return sum, nil
	}
	mark(s1, plan.StepDone, fmt.Sprintf("I read %s in %s.", nEmails(len(emails)), mailboxes))

	// Step 2 — the one judgment: which need the boss's reply? One batched LLM call.
	mark(s2, plan.StepInProgress, "")
	decisions := d.classify(ctx, emails)
	if len(decisions) == 0 {
		mark(s2, plan.StepFailed, "The model didn't return a usable decision — surfaced nothing rather than guess.")
		// Close out the last step so the plan reaches a terminal state (failed)
		// instead of lingering 'paused' until the next run sweeps it.
		mark(s3, plan.StepSkipped, "Skipped — nothing to surface after the decision step failed.")
		return sum, fmt.Errorf("classify returned no decisions")
	}
	needReply := 0
	for _, dec := range decisions {
		if dec.NeedsReply {
			needReply++
		}
	}
	mark(s2, plan.StepDone, fmt.Sprintf("I flagged %d of the %d as needing your reply.", needReply, len(emails)))

	// Step 3 — surface the winners (decision + upsert only; HTML loads lazily on
	// open via /api/followups/message, so the run stays fast and can't hang).
	mark(s3, plan.StepInProgress, "")
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
		if _, err := d.Surface.Upsert(ctx, it); err != nil {
			log.Warn("inbox triage surface", "msg", em.msgID, "err", err)
			continue
		}
		sum.Surfaced++
		sum.Examples = append(sum.Examples,
			fmt.Sprintf("%s about %q", senderName(em.from), firstNonEmpty(em.subject, "(no subject)")))
	}
	if sum.Surfaced == 1 {
		mark(s3, plan.StepDone, "I put 1 email in your Follow-ups.")
	} else {
		mark(s3, plan.StepDone, fmt.Sprintf("I put %d emails in your Follow-ups.", sum.Surfaced))
	}
	return sum, nil
}

// markCoverage upserts this account's triage pass into mem_connector_coverage —
// the SAME contract the connector_coverage_mark agent tool writes — so the
// deterministic Go triage path feeds ConnectorCoverageChecklist instead of
// leaving the coverage table empty (which left the safety net blind). Records
// last_status so a mailbox whose fetch ERRORED is flagged even when its
// timestamp is fresh. Best-effort: a coverage write must never abort triage.
func (d Deps) markCoverage(ctx context.Context, toolkit string, a *connectors.Account, status, errText string) {
	if d.Pool == nil || a == nil {
		return
	}
	identity := strings.TrimSpace(a.IdentityHint)
	if identity == "" {
		identity = strings.TrimSpace(a.Alias)
	}
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO mem_connector_coverage
		  (toolkit, account_id, identity, last_triaged_at, last_status, last_error, updated_at)
		VALUES ($1, $2, NULLIF($3,''), NOW(), $4, NULLIF($5,''), NOW())
		ON CONFLICT (toolkit, account_id) DO UPDATE SET
		  identity        = COALESCE(NULLIF(EXCLUDED.identity,''), mem_connector_coverage.identity),
		  last_triaged_at = NOW(),
		  last_status     = EXCLUDED.last_status,
		  last_error      = EXCLUDED.last_error,
		  updated_at      = NOW()
	`, toolkit, a.ID, identity, status, strings.TrimSpace(errText))
	if err != nil && d.Logger != nil {
		d.Logger.Warn("inbox triage coverage mark", "account", a.ID, "err", err)
	}
}

func fetchEmailBatch(ctx context.Context, exec *connectors.ExecuteClient, a *connectors.Account, cfg Config) (*connectors.ExecuteResponse, error) {
	if exec == nil || a == nil {
		return nil, fmt.Errorf("inbox triage fetch not configured")
	}
	call := func(max int) (*connectors.ExecuteResponse, error) {
		return exec.Execute(ctx, connectors.ExecuteRequest{
			Slug:               "GMAIL_FETCH_EMAILS",
			ConnectedAccountID: a.ID,
			UserID:             a.UserID, // entity — Composio 1811s without it
			Arguments: map[string]any{
				"query":       cfg.Query,
				"max_results": max,
				// Keep the list response concise. Full HTML/text is fetched lazily
				// when the boss opens a surfaced item; verbose list payloads 413 on
				// large inboxes before the LLM ever gets to decide.
				"verbose": false,
			},
		})
	}
	resp, err := call(cfg.MaxResults)
	if isPayloadTooLarge(err) || (resp != nil && !resp.Successful && isPayloadTooLargeText(resp.Error)) {
		return call(10)
	}
	return resp, err
}

func isPayloadTooLarge(err error) bool {
	return err != nil && isPayloadTooLargeText(err.Error())
}

func isPayloadTooLargeText(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "413") || strings.Contains(s, "payloadtoolarge") || strings.Contains(s, "payload too large")
}

// sweepStalePlans clears any prior "Inbox triage" plan still active/paused (an
// orphan from a crashed run) so the step plans never pile up on the board.
func (d Deps) sweepStalePlans(ctx context.Context) {
	if d.Pool == nil {
		return
	}
	_, _ = d.Pool.Exec(ctx,
		`UPDATE mem_plans SET status='failed', updated_at=NOW() WHERE title=$1 AND status IN ('active','paused')`,
		planTitle)
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
	text, err := llm.Complete(ctx, d.LLM, model, classifySystemPrompt, b.String())
	if err != nil {
		return out
	}

	decisions := parseDecisions(text)
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
			Intent: "Draft a reply to this email for the boss to review and edit. Create it ONLY as a Gmail draft on the same thread - do NOT send it. When the draft is saved, call surface_update with metadata.draft set to the exact drafted reply text and leave status open so the boss can review/send it."},
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
