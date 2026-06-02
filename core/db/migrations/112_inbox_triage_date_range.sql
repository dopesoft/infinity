-- 112_inbox_triage_date_range.sql — make inbox-triage honor an explicit time
-- window so a BACKFILL actually reaches back in time.
--
-- The boss: "I still havent been able to see my backfill of malabie industries
-- email from may 22nd that were skipped because the triage was broken."
--
-- Root cause: the v1.1 recipe hardcodes the Gmail query to
-- `is:unread newer_than:7d`. Today is 6 days past May 22, so `newer_than:7d`
-- structurally cannot reach May 22 — and `is:unread` drops anything the boss
-- already opened. A request that explicitly names a date range ("from may 22nd
-- until today", "backfill the last 30 days", "since June 1") was silently
-- clamped to the routine 7-day-unread window and the older mail never surfaced.
--
-- v1.2 keeps the routine default (is:unread newer_than:7d for cron / "check my
-- email") but adds an explicit-window path: when the request names a start date,
-- end date, or lookback, the recipe builds `after:YYYY/MM/DD [before:YYYY/MM/DD]`
-- from <current_time>, DROPS is:unread (a backfill must catch already-read mail
-- that was missed), and raises the bounded page cap so 1-2 weeks of mail fits.
-- Pure recipe change — zero Go. Goes live the moment this migration applies; no
-- core redeploy needed (skills_invoke reads the body from the DB).
--
-- Idempotent.

BEGIN;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'inbox-triage', 'v1.2-6-2-2026',
  $body$---
name: inbox-triage
version: "v1.2-6-2-2026"
description: The single canonical Gmail triage recipe. Discover every connected mailbox, surface messages awaiting the boss's reply to Follow-ups (deduped, importance-ranked, full body captured), draft replies under one Trust batch, and record per-mailbox coverage. Routine runs scan recent unread; an explicit date range / lookback triggers a bounded BACKFILL that reaches back in time and includes already-read mail. Paginated + lean (no 413). Multi-account, reconnect-proof. Never resolves a follow-up itself.
trigger_phrases: ['triage my inbox', 'check my email', 'triage gmail', 'inbox triage', 'check follow-ups', 'any important emails', 'backfill my email', 'triage email since', 'triage email from']
inputs: []
outputs: []
risk_level: medium
network_egress: 'composio'
confidence: 0.9
---

# Inbox triage (canonical)

You are triaging the boss's Gmail. This is the ONE triage recipe — it covers
EVERY connected mailbox and does both jobs that used to be split across ~17
competing skills:

1. **Surface** every message a person is waiting on the boss to reply to onto
   the **Follow-ups** dashboard, so he sees his real inbox at a glance.
2. **Draft** concise replies for the action-needed ones, queued under a single
   Trust batch for one-tap approval.

The reads (LIST, GET) run inline; every draft routes through the Trust queue
automatically (ComposioGate intercepts `composio__GMAIL_CREATE_DRAFT`).

## 0. Read the request — routine scan vs explicit BACKFILL

Before anything else, decide the time window from the boss's words and the
`<current_time>` block in your context (it carries today's date):

- **Routine** (default — "check my email", "triage my inbox", cron fire, no date
  mentioned): scan window is `is:unread newer_than:7d`, `max_messages` 20.
- **Backfill** (the request names a START date, an END date, or a lookback —
  "from may 22nd until today", "since June 1", "last 30 days", "backfill X"):
  build a Gmail **date range** instead. Convert the named dates to Gmail's
  `YYYY/MM/DD` form using today's date from `<current_time>`:
  - start → `after:YYYY/MM/DD` (the day named; "may 22nd" with a 2026 context →
    `after:2026/05/22`).
  - end → `before:YYYY/MM/DD` (omit when the request says "until today"/"to now").
  - a bare lookback ("last 30 days") → `newer_than:30d`.
  **Drop `is:unread`** for a backfill — mail the boss already opened but that was
  never triaged is exactly what he's asking you to resurface. Raise the bound to
  `max_messages` 60 and **up to 6 pages**. If the request names ONE mailbox
  (e.g. "malabie industries email" → the `malabieindustries.com` identity from
  step 1's account list), triage only that account.

Carry the chosen `query` and bounds into step 2.

## 1. Discover ALL mailboxes — never hardcode an account

Call `connector_accounts_list({ toolkit: "gmail" })`. It returns every ACTIVE
Gmail account with its `account_id` and `identity`. **Triage every one of them**
— loop steps 2–7 per account — UNLESS step 0 scoped the run to one named mailbox,
in which case match the requested name against the returned `identity` values and
triage just that `account_id`. Do NOT hardcode account ids: a reconnected mailbox
gets a fresh `account_id`, and the whole reason this skill exists is that a
hardcoded id silently dropped a mailbox for 9 days. If the list is empty, finish
with zeros and report `"no gmail account connected"`.

## 2. List messages (per account) — PAGINATE, fetch LEAN

For the current `account_id`, call `composio__GMAIL_LIST_MESSAGES` with the
`query` chosen in step 0 (routine: `is:unread newer_than:7d`; backfill: your
`after:`/`before:`/`newer_than:` range with NO `is:unread`), `max_results:
<max_messages>`, and that `connected_account_id`. LIST returns lean stubs (ids +
snippet) — keep it that way.

**Paginate, but bounded.** If the response carries a `next_page_token` (a.k.a.
`nextPageToken`) AND you have not yet collected `max_messages`, call
`GMAIL_LIST_MESSAGES` again with `page_token: <that token>` and append. STOP after
the page cap from step 0 (routine **3**, backfill **6**) OR once you've reached
`max_messages`, whichever comes first. Never loop unbounded.

**Never bulk-fetch bodies.** Do NOT call `GMAIL_FETCH_EMAILS` or any verb with
`include_payload` / `format: "full"` across the whole list — that is exactly what
returns **413 PayloadTooLarge**. Bodies are fetched one at a time, lazily, only
when a single preview is ambiguous (step 3). This holds for backfills too: a
wider date range means MORE stubs, never fuller ones.

## 3. Classify each message (preview first — full body is rare and CAPPED)

Classify cheaply from the LIST snippet/preview. Fetch the full body via the
single-message GET verb (`composio__GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID`, or find it
via `tool_search "gmail fetch message"`) ONLY when a preview is genuinely
ambiguous — **one message at a time, capped at 5 full-body fetches per account per
run.** Never batch full bodies. Bucket each into ONE of:

- **action**: a person needs a reply (a question, request, direct human
  message). → SURFACE (step 4) AND draft (step 5).
- **awaiting**: a person is waiting but only the boss can decide (legal,
  financial, personal). → SURFACE (step 4), do NOT draft.
- **fyi**: useful but no reply needed (newsletter, receipt, notification). →
  one-line summary into `skipped`. Do NOT surface or draft.
- **spam**: junk/promotional/scam. → `skipped` (reason "spam-like"). Do NOT
  surface, draft, or delete.

**Hard rule — any direct human email that is not obvious spam SURFACES.** When
unsure between fyi and action, surface it. Over-surfacing costs the boss one
glance; missing a real follow-up costs a relationship. Importance from the
preview: legal / finance / security / client / hiring / direct-question = high
(75–100); ordinary human reply-needed = medium (45–74); soft FYI you still
surfaced = low (≤44). On a backfill, a person who is STILL waiting (no later
reply in the thread) ranks higher — they have been waiting for days.

## 4. Surface follow-ups to the dashboard

For each **action** and **awaiting** message, call `surface_item`:

```
surface_item({
  surface: "followups",
  kind: "email",                       // REQUIRED — the Follow-ups card only renders kind='email'
  external_id: "gmail:<gmail message id>",  // REQUIRED — STABLE key: message id ONLY, no account id
  title: "<sender name> - <subject>",
  importance: <0-100 from the preview>,
  importance_reason: "<one line>",
  source: "inbox-triage",
  url: "<thread url>",
  metadata: {
    from: "<sender>", subject: "<subject>", preview: "<snippet>",
    thread_url: "<url>", account: "<connected_account_id>", classification: "action|awaiting"
  }
})
```

The `external_id` MUST be the bare `gmail:<message id>` form — the message id is
stable, so the row dedupes correctly even after a mailbox is revoked and
reconnected (a NEW account id must NOT resurface the same email as a duplicate).
Put the `connected_account_id` in `metadata.account` so the dashboard knows
which mailbox to read. You do NOT pass the body — the dashboard captures the
full HTML once, now, and stores it durably, so the boss can read it later even
if the account is later revoked.

NEVER surface status notes here — operational notes go to `surface: "system"`.
Follow-ups is messages-from-people ONLY.

## 5. Draft replies for action-class messages

For each **action** message, call `composio__GMAIL_CREATE_DRAFT`
(`connected_account_id`, `thread_id`, `recipient_email`, `subject`, `body`).
Match the boss's voice: direct, lower-case start unless formal, no fluff. If a
draft would exceed ~300 words or is legal/financial/strategic/sensitive,
reclassify as `awaiting` (surface, don't draft) — those are the boss's to write.
ComposioGate queues each draft with `batch_id = NULL`.

## 6. Record coverage for this mailbox

After finishing the account (even if nothing surfaced), call
`connector_coverage_mark({ account_id: "<connected_account_id>", toolkit:
"gmail", status: "ok" })`. If the account could not be scanned, call it with
`status: "error", error: "<trimmed reason>"` AND surface ONE `surface: "system"`
item titled "Inbox triage failure" for that mailbox, then continue to the next
account. This per-mailbox heartbeat is what powers the 12h staleness alarm — it
is how a silently-dropped inbox becomes visible the same day, not 9 days later.

## 7. Stamp the batch and report

After the last draft across all accounts, call
`trust_batch_assign({ count: <total drafts>, source: "composio_gate" })` (skip
if 0). Return terse:

```
{ mode: "routine|backfill", window: "<the gmail query used>",
  accounts: <n triaged>, classified, surfaced, drafted, batch_id,
  skipped: [{account, thread_id, from, subject, reason}] }
```

## Hard rules

- **NEVER resolve a follow-up.** Do NOT mark a Follow-ups email done/dismissed,
  do NOT call surface_update(status=done|dismissed) or followup_dismiss on one,
  do NOT set an expiry. A follow-up sits OPEN until the boss replies or dismisses
  it himself. Drafting a reply is NOT "handled". On scheduled/unattended runs
  this is enforced and will error — intentionally. Surfacing real mail then
  sweeping it is exactly the failure this skill prevents.
- **Never send.** You DRAFT. Sending happens only on the boss's approval.
- **Never delete or trash a message** during triage.
- **Never bulk-fetch full bodies.** LIST is lean; full bodies are lazy, single,
  and capped at 5/account/run. Bulk payload fetches 413 — that is a bug, not a
  retry. A backfill widens the DATE range, never the per-message payload.
- **A backfill honors the boss's dates.** Never silently clamp "since may 22" to
  the routine 7-day window — that is the exact bug this version fixes. Build the
  `after:`/`before:` range from his words and today's date.
- **Cover EVERY active mailbox** unless the request named one. A run that triages
  only one inbox when several are connected (and none was named) is a bug.
- **One run = one batch_id.** Even 30 drafts go under a single batch.
- **Idempotency.** `external_id` (gmail:<message id>) dedupes follow-ups; before
  drafting, scan recent `mem_trust_contracts` (source='composio_gate') for a
  matching `action_spec.input.thread_id` and skip if already drafted today.
- **Respect the budget.** If the daily-cost overlay shows >80% spent, cap
  max_messages at 5 per account this run (even on a backfill — report that you
  capped it so the boss can re-run for the rest).
$body$,
  '', 0.9, 'infinity_native'
)
ON CONFLICT (skill_name, version) DO NOTHING;

-- Repoint the pinned active version to v1.2.
INSERT INTO mem_skill_active (skill_name, active_version, pinned)
VALUES ('inbox-triage', 'v1.2-6-2-2026', TRUE)
ON CONFLICT (skill_name)
  DO UPDATE SET active_version = 'v1.2-6-2-2026', pinned = TRUE, updated_at = NOW();

-- Verification contract: prove the data pipe works for BOTH a routine window and
-- a date-range backfill without surfacing/drafting anything.
UPDATE mem_skills
   SET verify_contract = $vc$
{
  "scenario": "Discover connected Gmail mailboxes with connector_accounts_list({toolkit:\"gmail\"}). For the FIRST active account ONLY, call composio__GMAIL_LIST_MESSAGES TWICE, read-only: (1) routine query \"is:unread newer_than:7d\", max_results 10; (2) a backfill date-range query \"after:2026/05/22\" (no is:unread), max_results 10. Do NOT surface, draft, send, mark coverage, or modify anything. If BOTH LIST calls succeeded (ANY count, including zero, is a pass — you are proving the data pipe and that a date range is accepted, not the inbox), end with: VERIFY_RESULT: PIPELINE_OK routine <N1>, backfill <N2>. If either failed (auth, 413, no account, malformed query, etc.), end with: VERIFY_RESULT: FAILED <reason>.",
  "assert": "pipeline_ok",
  "min_artifacts": 0
}
$vc$::jsonb,
       last_evolved = NOW()
 WHERE name = 'inbox-triage';

COMMIT;
