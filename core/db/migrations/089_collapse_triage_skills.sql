-- 089_collapse_triage_skills.sql — collapse Gmail triage to ONE flexible skill,
-- and consolidate to ONE account-discovering cron.
--
-- Why (the 2026-05-28 incident + the boss's "no fucking duplicates" mandate):
-- email triage had fragmented into ~17 overlapping skills (gmail-triage,
-- triage_emails_to_dashboard, load-then-sweep-gmail, repeat-gmail-fetch-sweep,
-- scheduled-gmail-followup-triage, run-email-triage-and-check-railway, plus
-- -update shadows and an auto-generated triplet_… junk skill) and several
-- competing crons with account ids HARDCODED in their target strings. When the
-- malabieindustries mailbox was reconnected (new account id), the cron kept
-- pointing at the dead id, the inbox went un-triaged for ~9 days, and the
-- lawyer's email was never surfaced. Migrations 075/076/080 swept duplicates
-- by hand three times and they grew back.
--
-- This migration makes inbox-triage the SINGLE canonical recipe and rewrites
-- its body to be reconnect-proof and multi-account:
--   * discovers live mailboxes via connector_accounts_list (no hardcoded ids),
--   * dedupes on the STABLE gmail:<messageId> key (survives reconnect),
--   * records per-mailbox coverage via connector_coverage_mark (feeds the new
--     12h staleness alarm),
--   * folds in the 3 distinct sub-recipes (always-surface-direct-human-email,
--     gmail-preview-importance-filter, dashboard-followup-dedupe).
-- It then ARCHIVES every duplicate skill and consolidates to one clean cron
-- whose target lists NO account ids.
--
-- Regrowth is now prevented in code (proposals.FindDuplicateSkill consulted by
-- both skill_create and the Voyager extractor; the nightly
-- DetectSkillFragmentation backstop), so this is the last hand-sweep.
--
-- Reversible: archived skills are soft-retired (status='archived'), not deleted.

BEGIN;

-- 1. Rewrite the canonical inbox-triage body (active, pinned version).
UPDATE mem_skill_versions
SET skill_md = $body$---
name: inbox-triage
version: "v1.0-5-25-2026"
description: The single canonical Gmail triage recipe. Discover every connected mailbox, surface messages awaiting the boss's reply to Follow-ups (deduped, importance-ranked, full body captured), draft replies under one Trust batch, and record per-mailbox coverage. Multi-account, reconnect-proof. Never resolves a follow-up itself.
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

## 2. List recent unread (per account)

For the current `account_id`, call `composio__GMAIL_LIST_MESSAGES` with
`query: "is:unread newer_than:7d"`, `max_results: <max_messages, default 20>`,
and that `connected_account_id`. The 7-day window comfortably covers the 6h cron
cadence plus a missed cycle; the coverage alarm (below) catches anything longer.

## 3. Classify each message (preview first)

Use the preview to classify cheaply; fetch the full body via the GET verb
(`composio__GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID`, or find it via
`tool_search "gmail fetch message"`) ONLY when the preview is ambiguous. Bucket
each into ONE of:

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
- **Cover EVERY active mailbox.** A run that triages only one inbox when several
  are connected is a bug — that is how mail gets missed.
- **One run = one batch_id.** Even 30 drafts go under a single batch.
- **Idempotency.** `external_id` (gmail:<message id>) dedupes follow-ups; before
  drafting, scan recent `mem_trust_contracts` (source='composio_gate') for a
  matching `action_spec.input.thread_id` and skip if already drafted today.
- **Respect the budget.** If the daily-cost overlay shows >80% spent, cap
  max_messages at 5 per account this run.
$body$,
    promoted_at = NOW()
WHERE skill_name = 'inbox-triage' AND version = 'v1.0-5-25-2026';

-- 2. Canonical description (carries the multi-account + never-resolve intent).
UPDATE mem_skills
SET description = 'The single canonical Gmail triage recipe. Discover every connected mailbox (connector_accounts_list), surface messages awaiting the boss''s reply to Follow-ups (deduped on stable message id, importance-ranked, full body captured), draft replies under one Trust batch, and record per-mailbox coverage. Multi-account, reconnect-proof. Never resolves a follow-up itself - that is the boss''s call.'
WHERE name = 'inbox-triage';

-- 3. Keep inbox-triage active + pinned against future drift.
INSERT INTO mem_skill_active (skill_name, active_version, pinned)
VALUES ('inbox-triage', 'v1.0-5-25-2026', TRUE)
ON CONFLICT (skill_name) DO UPDATE
  SET active_version = 'v1.0-5-25-2026', pinned = TRUE, updated_at = NOW();

-- 4. Archive every duplicate / folded / shadow triage skill (soft retire).
--    Leaves the two UNRELATED triage skills (false-positive-curiosity-triage,
--    surprise-output-triage) untouched — they are not email triage.
UPDATE mem_skills
SET status = 'archived'
WHERE status = 'active'
  AND name IN (
    'gmail-triage',
    'gmail-triage-update',
    'triage_emails_to_dashboard',
    'triage_emails_to_dashboard-update',
    'load-then-sweep-gmail',
    'repeat-gmail-fetch-sweep',
    'run-email-triage-and-check-railway',
    'scheduled-gmail-followup-triage',
    'inbox-triage-update',
    'harden-inbox-triage-cron',
    'always-surface-direct-human-email',
    'gmail-preview-importance-filter',
    'dashboard-followup-dedupe',
    'triplet_composio__gmail_fetch_emails_composio__gmail_fetch_emails_composio__gmail_fetch_emails'
  );

-- 5. Reject any still-open triage skill proposals so they don't re-activate.
UPDATE mem_skill_proposals
SET status = 'rejected'
WHERE status IN ('candidate', 'pending', 'draft')
  AND (
    name ILIKE '%triage%' OR name ILIKE '%gmail%' OR name ILIKE '%inbox%'
    OR name ILIKE '%followup%' OR name ILIKE '%sweep%'
  )
  AND name NOT ILIKE '%curiosity%'
  AND name NOT ILIKE '%surprise%';

-- 6. Consolidate to ONE account-discovering cron. Rewrite the hardened cron's
--    target to invoke the skill (which now discovers mailboxes itself) with NO
--    account ids in the string, and keep the legacy central cron disabled.
UPDATE mem_crons
SET target = 'Triage the boss''s Gmail across ALL connected mailboxes. Your FIRST action must be skills_invoke({name:"inbox-triage"}); the returned recipe is your instruction set — EXECUTE every step this turn. The recipe discovers the live mailboxes itself via connector_accounts_list — do NOT hardcode account ids, and cover every active Gmail account, not just one. Surface each message awaiting the boss''s reply to the Follow-ups dashboard, draft replies under one fresh Trust batch (never send), and call connector_coverage_mark per mailbox. Never resolve or dismiss a follow-up — that is the boss''s call. This is a Gmail + dashboard task only: do NOT use claude_code, bash, the filesystem, browser, or git.',
    schedule_natural = 'every 6 hours across all connected Gmail inboxes',
    enabled = TRUE
WHERE name = 'inbox-triage-hardened';

UPDATE mem_crons SET enabled = FALSE WHERE name = 'triage-gmail-followups-central';

COMMIT;
