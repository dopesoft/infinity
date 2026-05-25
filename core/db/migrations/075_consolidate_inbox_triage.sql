-- 075_consolidate_inbox_triage.sql
--
-- ONE canonical email-triage skill. Context: the capability had fragmented
-- across 8 active skills (gmail-triage, triage_emails_to_dashboard,
-- always-surface-direct-human-email, dashboard-followup-dedupe,
-- gmail-preview-importance-filter, inbox-triage, …). They competed on the
-- same triggers, each spawned its own GEPA merge proposals, and a scheduled
-- run dismissed days of real follow-ups (the incident this PR fixes). Per
-- Rule #1 the capability is ONE recipe that gets improved, not re-minted.
--
-- This migration:
--   1. Bumps `inbox-triage` to v1.1.0. The new body folds in every triage
--      concern (read → classify → SURFACE to Follow-ups with dedup → draft →
--      report) and adds the hard rule that NOTHING automated may resolve a
--      follow-up. The prior 1.0.0 body drafted to Trust but never called
--      surface_item, which is why the Follow-ups card stayed empty even
--      when triage ran.
--   2. Points mem_skill_active at 1.1.0.
--   3. Archives the duplicate / fragment triage skills (soft - status flip,
--      reversible).
--   4. Rejects their open `candidate` proposals so Voyager/GEPA stop
--      optimizing clones (the "4 merges" the boss kept seeing).
--   5. Disables the redundant `triage-gmail-followups-central` cron - the
--      one whose 13/17/23 UTC runs dismissed the follow-ups. inbox-triage's
--      own 6h cron (migration 056) is the single scheduled path now.
--
-- The durable safety net lives in Go (surface_update / followup_dismiss
-- refuse to resolve follow-up emails on autonomous turns; SweepExpired
-- skips them) - this migration is the consolidation half.
--
-- Idempotent: version insert is ON CONFLICT DO NOTHING; active/skills/cron
-- use DO UPDATE; archive/reject are status flips guarded by name.

BEGIN;

-- 1. Canonical body: inbox-triage v1.1.0 ────────────────────────────────────
INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'inbox-triage',
  '1.1.0',
  $skill$---
name: inbox-triage
version: "1.1.0"
description: The single canonical Gmail triage recipe. Classify recent mail, SURFACE the messages awaiting the boss's reply to the Follow-ups dashboard (deduped, importance-ranked), AND draft replies queued under one Trust batch. Multi-account aware. Never resolves a follow-up itself - that is the boss's call.
trigger_phrases:
  - triage my inbox
  - triage gmail
  - run inbox triage
  - clear my inbox
  - draft replies to my emails
  - check my email
  - surface my follow-ups
inputs:
  - name: account_id
    type: string
    doc: optional connected_account_id; if omitted, pick the primary Gmail account from <connected_accounts>
  - name: max_messages
    type: integer
    doc: optional cap on messages to look at this run; default 20
outputs:
  - name: classified
    type: integer
  - name: surfaced
    type: integer
    doc: messages put on the Follow-ups dashboard this run
  - name: drafted
    type: integer
  - name: batch_id
    type: string
    doc: the shared UUID stamped on every draft trust contract this run produced
  - name: skipped
    type: array
risk_level: medium
network_egress: composio:gmail
confidence: 0.85
---

# Inbox triage (canonical)

You are triaging the boss's Gmail. This is the ONE triage recipe - it does
both jobs that used to be split across competing skills:

1. **Surface** every message a person is waiting on the boss to reply to onto
   the **Follow-ups** dashboard, so the boss sees his real inbox at a glance.
2. **Draft** concise replies for the action-needed ones and queue them under a
   single Trust batch for one-tap approval.

The reads (LIST, GET) run inline; every draft routes through the Trust queue
automatically (ComposioGate intercepts `composio__GMAIL_CREATE_DRAFT`).

## 1. Pick the right account

Read the `<connected_accounts>` overlay. If the caller passed `account_id`,
use it. Otherwise pick the Gmail account whose `identity` matches the boss's
primary address (`kai@dopesoft.io`). If multiple Gmail accounts have no
identity yet, run `resolve-connector-identities` first, then resume. If no
Gmail account is connected, finish with zeros and report
`"no gmail account connected"`.

## 2. List recent messages

Call `composio__GMAIL_LIST_MESSAGES` with `query: "is:unread newer_than:2d"`,
`max_results: <max_messages, default 20>`, and the picked
`connected_account_id`. Only triage recent mail - never sweep month-old
context.

## 3. Classify each message

Use the preview first; fetch the full body via the toolkit's GET verb
(`composio__GMAIL_FETCH_MESSAGE_BY_ID`, or find it via `tool_search "gmail
fetch message"`) only when the preview is ambiguous. Bucket each into ONE of:

- **action**: a person needs a reply (a question, a request, a direct human
  message). → SURFACE it (step 4) AND draft a reply (step 5).
- **awaiting**: a person is waiting on the boss but YOU should not draft
  (a decision only he can make - legal, financial, personal). → SURFACE it
  (step 4), do NOT draft.
- **fyi**: useful but no reply needed (newsletter, receipt, notification). →
  add a one-line summary to `skipped`. Do NOT surface, do NOT draft.
- **spam**: junk/promotional/scam. → add to `skipped` (reason "spam-like").
  Do NOT surface, do NOT draft, do NOT delete.

Rules of thumb: **any direct human email that is not obvious spam surfaces** -
when unsure between fyi and action, surface it. Over-surfacing costs the boss
one glance; missing a real follow-up costs a relationship.

## 4. Surface follow-ups to the dashboard

For each **action** and **awaiting** message, call `surface_item`:

```
surface_item({
  surface: "followups",
  kind: "email",                       // REQUIRED - the Follow-ups card only renders kind='email'
  external_id: "<gmail message id>",   // REQUIRED - dedupes across runs (upsert, never a duplicate row)
  title: "<sender name> - <subject>",
  importance: <0-100 from the preview>,
  importance_reason: "<one line>",
  source: "inbox-triage",
  url: "<thread url>",
  metadata: {
    from: "<sender>", subject: "<subject>", preview: "<snippet>",
    thread_url: "<url>", account: "<identity>", classification: "action|awaiting"
  }
})
```

`external_id` is the dedupe key: re-running this skill refreshes the same row
instead of creating a second. That is how the 6h cron stays idempotent on the
dashboard side. NEVER surface your own status notes here - operational notes
go to `surface: "system"`. Follow-ups is messages-from-people ONLY.

## 5. Draft replies for action-class messages

For each **action** message, call `composio__GMAIL_CREATE_DRAFT`
(`connected_account_id`, `thread_id`, `recipient_email`, `subject`, `body`).
Match the boss's voice: direct, lower-case start unless the message is formal,
no fluff. If a draft would exceed ~300 words, reclassify it as `awaiting`
(surface, don't draft) - long replies are the boss's job. ComposioGate queues
each draft with `batch_id = NULL`.

## 6. Stamp the batch and report

After the last draft is queued, call
`trust_batch_assign({ count: <drafts you created>, source: "composio_gate" })`
(skip if count is 0). Return:

```
{ classified, surfaced, drafted, batch_id, skipped: [{thread_id, from, subject, reason}] }
```

Be terse - the boss reviews actual content on the dashboard and Trust panel.

## Hard rules

- **NEVER resolve a follow-up.** Do NOT mark a Follow-ups email done/dismissed,
  do NOT call surface_update(status=done|dismissed) or followup_dismiss on a
  follow-up email, do NOT set an expiry on it. A follow-up sits OPEN until the
  boss replies or dismisses it himself (in the UI, or by telling you to in live
  chat). Drafting a reply is NOT "handled". On scheduled/unattended runs this
  is enforced and will error - that is intentional. Surfacing days of real mail
  and then sweeping it is exactly the failure this skill exists to prevent.
- **Never send.** You DRAFT. Sending happens only on the boss's approval.
- **Never delete or trash a message** during triage.
- **One run = one batch_id.** Even 30 drafts go under a single batch.
- **Idempotency.** `external_id` on surface_item dedupes follow-ups; before
  drafting, scan recent `mem_trust_contracts` (source='composio_gate') for a
  matching `action_spec.input.thread_id` and skip if already drafted today.
- **Respect the budget.** If the daily-cost overlay shows >80% spent, cap
  max_messages at 5 this run.
$skill$,
  '',
  0.85,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

-- 2. Activate 1.1.0 ──────────────────────────────────────────────────────────
INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('inbox-triage', '1.1.0')
ON CONFLICT (skill_name) DO UPDATE SET
  active_version = EXCLUDED.active_version,
  updated_at = NOW();

-- Refresh the catalog description so search/match reflect the merged scope.
UPDATE mem_skills
   SET description = 'The single canonical Gmail triage recipe: classify recent mail, surface the messages awaiting the boss''s reply to the Follow-ups dashboard (deduped by Gmail message id, importance-ranked), and draft replies queued under one Trust batch for one-tap approval. Multi-account aware. Never resolves a follow-up itself - that is the boss''s call.',
       updated_at = NOW()
 WHERE name = 'inbox-triage';

-- 3. Archive the duplicate / fragment triage skills (soft, reversible) ────────
-- gmail-triage + triage_emails_to_dashboard are full duplicates of the read/
-- triage/surface loop above. always-surface-direct-human-email,
-- dashboard-followup-dedupe, and gmail-preview-importance-filter are fragments
-- whose guidance is now folded into the canonical body (steps 3-4). One skill,
-- one source of truth.
UPDATE mem_skills
   SET status = 'archived', updated_at = NOW()
 WHERE name IN (
   'gmail-triage',
   'triage_emails_to_dashboard',
   'always-surface-direct-human-email',
   'dashboard-followup-dedupe',
   'gmail-preview-importance-filter'
 )
 AND status = 'active';

-- 4. Reject open candidate proposals for the consolidated skills so Voyager/
--    GEPA stop generating competing merges for clones.
UPDATE mem_skill_proposals
   SET status = 'rejected', decided_at = NOW()
 WHERE status = 'candidate'
   AND parent_skill IN (
     'gmail-triage',
     'triage_emails_to_dashboard',
     'scheduled-gmail-followup-triage',
     'always-surface-direct-human-email',
     'dashboard-followup-dedupe',
     'gmail-preview-importance-filter'
   );

-- 5. Disable the redundant dismissing cron. Its 13/17/23 UTC runs are what
--    swept the follow-ups; inbox-triage's own 6h cron (056) is the single
--    scheduled triage path now. Disabled (not deleted) so it's recoverable.
UPDATE mem_crons
   SET enabled = FALSE
 WHERE name = 'triage-gmail-followups-central';

COMMIT;
