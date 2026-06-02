-- 099_inbox_triage_pagination.sql — make inbox-triage actually bring back data.
--
-- The boss: "U ABSOLUTELY NEED TO INSTALL PAGINATION MAN WTF?!?! HOW DO WE BUILD
-- A SKILL AND THEN WE DONT ENSURE IT CAN BRING BACK DATA."
--
-- The v1.0 recipe called GMAIL_LIST_MESSAGES once with max_results:20, never
-- looped next_page_token, and left the door open to bulk full-body fetches that
-- 413'd (PayloadTooLarge). This pins a v1.1 that:
--   - PAGINATES (bounded: ≤3 pages or max_messages, whichever first),
--   - fetches LEAN (LIST stubs only; never bulk full payloads / include_payload),
--   - caps lazy single-message body fetches at 5/account/run.
-- Pure recipe change — zero Go.
--
-- It is also the FIRST consumer of the Pillar 3 verification gate (migration
-- 098): a read-only verify_contract proves the Gmail data pipe works (any count,
-- including an empty inbox) and FAILS loudly on auth/413 — exactly the breakage
-- the boss kept seeing surfaced as cryptic JSON.
--
-- Idempotent.

BEGIN;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'inbox-triage', 'v1.1-6-1-2026',
  $body$---
name: inbox-triage
version: "v1.1-6-1-2026"
description: The single canonical Gmail triage recipe. Discover every connected mailbox, surface messages awaiting the boss's reply to Follow-ups (deduped, importance-ranked, full body captured), draft replies under one Trust batch, and record per-mailbox coverage. Paginated + lean (no 413). Multi-account, reconnect-proof. Never resolves a follow-up itself.
trigger_phrases: ['triage my inbox', 'check my email', 'triage gmail', 'inbox triage', 'check follow-ups', 'any important emails']
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

## 1. Discover ALL mailboxes — never hardcode an account

Call `connector_accounts_list({ toolkit: "gmail" })`. It returns every ACTIVE
Gmail account with its `account_id` and `identity`. **Triage every one of them**
— loop steps 2–7 per account. Do NOT hardcode account ids and do NOT triage
only one inbox: a reconnected mailbox gets a fresh `account_id`, and the whole
reason this skill exists is that a hardcoded id silently dropped a mailbox for 9
days. If the caller passed a specific `account_id`, triage just that one. If the
list is empty, finish with zeros and report `"no gmail account connected"`.

## 2. List recent unread (per account) — PAGINATE, fetch LEAN

For the current `account_id`, call `composio__GMAIL_LIST_MESSAGES` with
`query: "is:unread newer_than:7d"`, `max_results: <max_messages, default 20>`,
and that `connected_account_id`. LIST returns lean stubs (ids + snippet) — keep
it that way; the 7-day window plus the coverage alarm (step 6) cover anything
older.

**Paginate, but bounded.** If the response carries a `next_page_token` (a.k.a.
`nextPageToken`) AND you have not yet collected `max_messages`, call
`GMAIL_LIST_MESSAGES` again with `page_token: <that token>` and append. STOP after
at most **3 pages** OR once you've reached `max_messages`, whichever comes first.
Never loop unbounded.

**Never bulk-fetch bodies.** Do NOT call `GMAIL_FETCH_EMAILS` or any verb with
`include_payload` / `format: "full"` across the whole list — that is exactly what
returns **413 PayloadTooLarge**. Bodies are fetched one at a time, lazily, only
when a single preview is ambiguous (step 3).

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
surfaced = low (≤44).

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
{ accounts: <n triaged>, classified, surfaced, drafted, batch_id,
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
  retry.
- **Cover EVERY active mailbox.** A run that triages only one inbox when several
  are connected is a bug — that is how mail gets missed.
- **One run = one batch_id.** Even 30 drafts go under a single batch.
- **Idempotency.** `external_id` (gmail:<message id>) dedupes follow-ups; before
  drafting, scan recent `mem_trust_contracts` (source='composio_gate') for a
  matching `action_spec.input.thread_id` and skip if already drafted today.
- **Respect the budget.** If the daily-cost overlay shows >80% spent, cap
  max_messages at 5 per account this run.
$body$,
  '', 0.9, 'infinity_native'
)
ON CONFLICT (skill_name, version) DO NOTHING;

-- Repoint the pinned active version to v1.1.
INSERT INTO mem_skill_active (skill_name, active_version, pinned)
VALUES ('inbox-triage', 'v1.1-6-1-2026', TRUE)
ON CONFLICT (skill_name)
  DO UPDATE SET active_version = 'v1.1-6-1-2026', pinned = TRUE, updated_at = NOW();

-- Read-only verification contract (Pillar 3 / migration 098). Proves the Gmail
-- data pipe works without surfacing or drafting anything; PIPELINE_OK on any
-- count (empty inbox is fine — we're proving retrieval, not the inbox), FAILED on
-- auth/413/etc.
UPDATE mem_skills
   SET verify_contract = $vc$
{
  "scenario": "Discover connected Gmail mailboxes with connector_accounts_list({toolkit:\"gmail\"}). For the FIRST active account ONLY, call composio__GMAIL_LIST_MESSAGES with query \"is:unread newer_than:7d\", max_results 10, and that connected_account_id. Classify the returned previews into action/awaiting/fyi/spam counts. Do NOT surface, draft, send, mark coverage, or modify anything. If the LIST call succeeded (ANY count, including zero, is a pass — you are proving the data pipe, not the inbox), end with: VERIFY_RESULT: PIPELINE_OK listed <N> messages, classified <counts>. If it failed (auth, 413, no account, etc.), end with: VERIFY_RESULT: FAILED <reason>.",
  "assert": "pipeline_ok",
  "min_artifacts": 0
}
$vc$::jsonb,
       last_evolved = NOW()
 WHERE name = 'inbox-triage';

COMMIT;
