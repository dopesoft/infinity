package proactive

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/connectors"
	"github.com/jackc/pgx/v5/pgxpool"
)

// coverageStaleAfter is how long a connected mailbox may go without a
// successful triage pass before the heartbeat raises a finding. The triage
// cron runs every 6h, so 12h = two missed cycles — long enough to avoid
// flapping on a single slow run, short enough that a silently-dropped mailbox
// becomes visible the same day instead of (as happened on 2026-05-28) nine
// days later.
const coverageStaleAfter = 12 * time.Hour

// coverageGraceAfterConnect gives a freshly-connected (or reconnected)
// account one full triage cycle before we expect a coverage row, so the
// alarm doesn't fire the instant the boss links a new mailbox.
const coverageGraceAfterConnect = 7 * time.Hour

// ConnectorCoverageChecklist is the safety net that turns a silent mailbox
// blind spot into a visible alarm. For every ACTIVE Gmail account in the
// live connector cache it checks mem_connector_coverage (written per-account
// by connector_coverage_mark at the end of each triage pass). An account is
// stale when it has no coverage row at all (yet has been connected past the
// grace window) or its last successful pass is older than coverageStaleAfter.
//
// This is precisely the gap that dropped the lawyer email: the malabie inbox
// was reconnected (new account id) but no triage cron covered it for ~9 days,
// while the cron that DID run reported "ok" because it was scanning a
// different mailbox. Per-cron status can't see that; per-account coverage can.
//
// Zero toolkit-specific cognition lives here — the checklist only knows how to
// detect staleness and ask the agent to run inbox-triage. The recipe carries
// the how. No-ops when cache/pool are nil so chat-only deployments don't break.
func ConnectorCoverageChecklist(cache *connectors.Cache, pool *pgxpool.Pool) Checklist {
	return func(ctx context.Context, h *Heartbeat) ([]Finding, error) {
		if cache == nil || pool == nil {
			return nil, nil
		}
		active := cache.ActiveAccountsByToolkit("gmail")
		if len(active) == 0 {
			// Empty-from-failure must never read as empty-from-truth. If the
			// connector backend is erroring, "0 active accounts" does NOT mean the
			// boss has no mailboxes — it means we're BLIND. Silently resolving the
			// alarm here is exactly what let a multi-day Composio 401 hide: triage
			// reported "no new mail" and this safety net agreed there was nothing to
			// cover. Raise it instead, so a connector outage is loud within a tick.
			if le := strings.TrimSpace(cache.Status().LastError); le != "" {
				return []Finding{{
					Kind:  "surprise",
					Title: "I can't see your connected accounts right now",
					Detail: strings.Join([]string{
						"connector backend error: " + truncate(le, 240),
						"impact: inbox triage and every connector-backed cron is running BLIND — they cannot read your mail or calendar until this clears",
						"action: check Settings → Connectors; the Composio API may be rejecting our auth (a 401 here silently zeroes every mailbox)",
						"why surfaced: a blind run otherwise looks identical to an empty inbox and never reaches you",
					}, "\n"),
					PreApproved: false,
					Source:      "connector_backend_error",
					SourceTag:   "connector_backend_error",
				}}, nil
			}
			ResolveSourceTag(ctx, pool, "connector_coverage_gap")
			ResolveSourceTag(ctx, pool, "connector_backend_error")
			return nil, nil
		}
		// Connector backend is answering (we have active accounts) — clear any
		// prior "can't see your accounts" alarm.
		ResolveSourceTag(ctx, pool, "connector_backend_error")

		// Per-account coverage for this toolkit: last pass time AND status, so a
		// mailbox whose last fetch ERRORED is caught even when its timestamp is
		// fresh (a fresh-but-failed pass is still a blind mailbox).
		type cov struct {
			at     time.Time
			status string
			errMsg string
		}
		covByID := map[string]cov{}
		rows, err := pool.Query(ctx, `
			SELECT account_id, last_triaged_at, last_status, COALESCE(last_error,'')
			FROM mem_connector_coverage
			WHERE toolkit = 'gmail'
		`)
		if err == nil {
			for rows.Next() {
				var id, status, emsg string
				var at time.Time
				if scanErr := rows.Scan(&id, &at, &status, &emsg); scanErr == nil {
					covByID[id] = cov{at: at, status: status, errMsg: emsg}
				}
			}
			rows.Close()
		}

		now := time.Now().UTC()
		var stale []string
		for _, a := range active {
			c, seen := covByID[a.ID]
			if seen {
				// A mailbox whose last pass FAILED is blind regardless of how
				// recent it was — flag it on status, not just on staleness.
				if c.status == "error" {
					stale = append(stale, coverageErrLabel(a, c.errMsg))
					continue
				}
				if now.Sub(c.at) > coverageStaleAfter {
					stale = append(stale, coverageLabel(a, now.Sub(c.at)))
				}
				continue
			}
			// No coverage row ever. Only flag once the account has been
			// connected long enough to have been triaged at least once.
			if a.CreatedAt.IsZero() || now.Sub(a.CreatedAt) > coverageGraceAfterConnect {
				stale = append(stale, coverageLabel(a, 0))
			}
		}

		if len(stale) == 0 {
			ResolveSourceTag(ctx, pool, "connector_coverage_gap")
			return nil, nil
		}

		title := fmt.Sprintf("I have not checked %d of your mailboxes lately", len(stale))
		if len(stale) == 1 {
			title = "I have not checked 1 of your mailboxes lately"
		}
		detail := strings.Join([]string{
			"stale: " + strings.Join(stale, "; "),
			"action: run inbox triage now via `skills_invoke({name:\"inbox-triage\"})`, then execute every step it returns this turn",
			"why: these connected mailboxes have no recent successful triage pass — a reconnect or disabled cron can silently drop a mailbox from coverage, which is how an important email gets missed",
			"safety: triage is draft-only and never resolves a follow-up; safe to run unattended",
		}, "\n")
		return []Finding{{
			Kind:        "surprise",
			Title:       title,
			Detail:      detail,
			PreApproved: true,
			Source:      "connector_coverage_gap",
			// Stable tag so a later tick with a different count auto-resolves
			// the earlier finding instead of stacking duplicates.
			SourceTag: "connector_coverage_gap",
		}}, nil
	}
}

func coverageLabel(a *connectors.Account, age time.Duration) string {
	who := strings.TrimSpace(a.IdentityHint)
	if who == "" {
		who = strings.TrimSpace(a.Alias)
	}
	if who == "" {
		who = a.ID
	}
	if age <= 0 {
		return who + " (never)"
	}
	return fmt.Sprintf("%s (%dh ago)", who, int(age.Hours()))
}

// coverageErrLabel renders a mailbox whose last triage pass errored, naming the
// upstream reason so the alarm is actionable ("…last pass failed: composio 401").
func coverageErrLabel(a *connectors.Account, errMsg string) string {
	who := strings.TrimSpace(a.IdentityHint)
	if who == "" {
		who = strings.TrimSpace(a.Alias)
	}
	if who == "" {
		who = a.ID
	}
	if e := strings.TrimSpace(errMsg); e != "" {
		return fmt.Sprintf("%s (last pass failed: %s)", who, truncate(e, 120))
	}
	return who + " (last pass failed)"
}
