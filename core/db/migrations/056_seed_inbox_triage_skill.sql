-- 056_seed_inbox_triage_skill.sql
--
-- Seed the `inbox-triage` default skill plus a 6h cron that fires the
-- agent to run it. This is the canonical "Composio write out-loop" - the
-- agent lists Gmail, classifies messages, drafts replies, and queues each
-- draft as a Trust contract with a shared batch_id so the boss can
-- "Approve all N" from Studio's Trust panel.
--
-- Why a skill, not Go: per Rule #1 the *cognition* (what counts as
-- action-needed, how to draft, tone, sign-off) lives in the SKILL.md so
-- it can evolve via Voyager / GEPA without a code change. The Go side
-- already has the building blocks: composio__GMAIL_* via the MCP
-- bridge, ComposioGate's verb-pattern Trust queue, the batch_id column
-- (migration 055), and multi-account routing via <connected_accounts>.
--
-- The cron uses `isolated_agent_turn` job_kind: the cron executor spins
-- up an isolated agent turn with the prompt below and lets the agent
-- pick the right account and run the skill. No bespoke "inbox triage"
-- Go executor.
--
-- Idempotency: ON CONFLICT DO NOTHING for the skill rows; the cron
-- upserts on name so re-running this migration is safe and tweaks to
-- the schedule land cleanly.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source)
VALUES (
  'inbox-triage',
  'Triage the boss''s Gmail inbox: classify each recent message, draft replies for the ones that need action, and queue each draft as a Trust contract under a shared batch so the boss can approve them all at once. Reads via composio__GMAIL_LIST_MESSAGES; drafts via composio__GMAIL_CREATE_DRAFT. Multi-account aware - picks the primary account from <connected_accounts> unless the boss specifies one.',
  'medium',
  '[]'::jsonb,
  '["triage my inbox","triage gmail","run inbox triage","clear my inbox","draft replies to my emails"]'::jsonb,
  '[{"name":"account_id","type":"string","doc":"optional connected_account_id; if omitted the skill picks the primary Gmail account"},{"name":"max_messages","type":"integer","doc":"optional cap on messages to triage in this run; default 20"}]'::jsonb,
  '[{"name":"classified","type":"integer","doc":"messages classified this run"},{"name":"drafted","type":"integer","doc":"draft replies queued for approval"},{"name":"batch_id","type":"string","doc":"shared batch id for the drafts; usable to bulk-approve in Studio"},{"name":"skipped","type":"array","doc":"summaries of messages that did not need a draft (FYI / spam / boss-attention-only)"}]'::jsonb,
  0.85,
  'active',
  'manual'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'inbox-triage',
  '1.0.0',
  $skill$---
name: inbox-triage
version: "1.0.0"
description: Triage the boss's Gmail inbox - classify each recent message, draft replies for action-needed mail, and queue every draft as a Trust contract under a shared batch_id so the boss can bulk-approve. Multi-account aware via the <connected_accounts> overlay.
trigger_phrases:
  - triage my inbox
  - triage gmail
  - run inbox triage
  - clear my inbox
  - draft replies to my emails
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

# Inbox triage

You are clearing the boss's Gmail inbox. The goal is to drop the unread
count to zero by classifying everything and drafting concise replies for
the messages that actually need one.

This is a Composio write out-loop. The reads (LIST, GET) run inline;
every draft you create routes through the Trust queue automatically
(ComposioGate intercepts `composio__GMAIL_CREATE_DRAFT`). You group the
drafts by a single batch_id so the boss approves the whole run in one
tap from `/settings?section=trust`.

## 1. Pick the right account

Read the `<connected_accounts>` overlay. If the caller passed `account_id`,
use it directly. Otherwise pick the first Gmail account whose `identity`
matches the boss's known primary address (`kai@dopesoft.io` per the
identity memory). If multiple Gmail accounts have no identity yet, run
`resolve-connector-identities` first, then resume.

If no Gmail account is connected, finish with `classified: 0, drafted: 0`
and report `"no gmail account connected"`.

## 2. Plan the batch

You'll mint the batch_id AFTER the drafts are queued, by calling
`trust_batch_assign` once at the end. ComposioGate writes each draft
into mem_trust_contracts with `batch_id = NULL`; `trust_batch_assign`
stamps the N most recent NULL-batch contracts with a fresh UUID. The
Trust UI groups by batch_id and renders a single "Approve all N"
button per batch. Track the count of drafts you create this run so
step 6 can pass it in.

## 3. List recent messages

Call `composio__GMAIL_LIST_MESSAGES` with:

```
{
  connected_account_id: <picked account_id>,
  query: "is:unread newer_than:1d",
  max_results: <max_messages, default 20>
}
```

You only triage what arrived recently. The boss does NOT want you
clearing out month-old context.

## 4. Classify each message

For each message, fetch the body via `composio__GMAIL_FETCH_MESSAGE_BY_ID`
(or whatever the toolkit's GET verb is - find it via `tool_search "gmail
fetch message"` if the name differs). Bucket each into ONE of:

- **action**: the sender needs a reply. Boss has been pinged or asked a
  question. Draft a reply (next step).
- **fyi**: useful information but doesn't need a response (newsletter,
  notification, receipt). Add a one-line summary to the `skipped` list,
  do NOT draft.
- **spam**: junk / promotional / scam / unsubscribe-or-ignore. Add to
  `skipped` with reason "spam-like". Do NOT draft, do NOT delete (the
  boss might want to review).
- **boss-attention**: needs the boss to make a decision YOU shouldn't
  pre-empt (legal, financial above a casual threshold, personal
  relationship). Add to `skipped` with reason "needs boss judgment".

If you cannot classify, default to `boss-attention` - over-escalation
costs the boss one extra glance, mis-drafting can cost a relationship.

## 5. Draft replies for action-class messages

For each `action` message, call `composio__GMAIL_CREATE_DRAFT` with:

- `connected_account_id`: the picked account
- `thread_id`: from the listing
- `recipient_email` / `subject`: from the original message
- `body`: a concise reply matching the boss's voice (direct, lower-case
  start unless the message is formal, no fluff)

ComposioGate will intercept each CREATE_DRAFT call and queue a Trust
contract with `batch_id = NULL`. You'll stamp them all with a shared
batch_id in step 6.

If a draft body would exceed ~300 words, the message probably belongs in
`boss-attention` instead. Long replies are the boss's job.

## 6. Stamp the batch and report

After every draft you intended to create has been queued, call:

```
trust_batch_assign({ count: <number_of_drafts_you_created>, source: "composio_gate" })
```

This stamps the N most recent NULL-batch pending contracts with a fresh
UUID. Use the returned `batch_id` in your final report. If `count` was 0
(no drafts this run), skip this step.

Return:

```
{
  classified: <n>,
  drafted: <n>,
  batch_id: "<uuid>",
  skipped: [
    { thread_id, from, subject, reason },
    ...
  ]
}
```

Be terse. The boss will look at the Trust queue for the actual content.

## Hard rules

- **Never send.** You DRAFT. Sending happens only when the boss approves
  the Trust contract in Studio. If a draft verb fails, do NOT fall back
  to a send verb.
- **Never delete or trash a message** during triage. The classification
  is the answer; the message stays in the inbox until the boss decides.
- **One run = one batch_id.** Do not split into multiple batches even if
  the run drafts 30 replies; the boss wants one approve / deny per run.
- **Idempotency: skip threads already in the queue.** Before drafting,
  scan recent `mem_trust_contracts` (source = 'composio_gate') for an
  action_spec.input.thread_id matching the current thread. If you find a
  pending or recently-approved row, skip and add the thread to
  `skipped` with reason "already drafted earlier today". This prevents
  the 6h cron from re-drafting the same threads every tick.
- **Respect the budget.** If the daily-cost overlay shows >80% spent,
  cap max_messages at 5 for this run. The agent's own self-throttle.
$skill$,
  '',
  0.85,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('inbox-triage', '1.0.0')
ON CONFLICT (skill_name) DO NOTHING;

-- ── Cron: run the skill every 6 hours via an isolated agent turn ─────────
-- The cron executor spins up an isolated agent turn with this prompt; the
-- prompt routes the agent to the inbox-triage skill, which carries the
-- full cognition. No per-vendor Go.
INSERT INTO mem_crons
    (name, schedule, schedule_natural, job_kind, target, target_config,
     enabled, max_retries, backoff_seconds)
VALUES
    (
      'inbox-triage',
      '0 */6 * * *',
      'every 6 hours',
      'isolated_agent_turn',
      'Run the inbox-triage skill on the primary Gmail account. Use the active connected_account from the <connected_accounts> overlay (the one whose identity matches kai@dopesoft.io); if there is no usable account, exit cleanly and surface a finding instead of erroring. Cap max_messages at 20. Mint a fresh batch_id, queue all drafts under that batch, then report the {classified, drafted, batch_id, skipped} summary. Do not send any messages directly - drafts only.',
      '{}'::jsonb,
      TRUE,
      1,
      180
    )
ON CONFLICT (name) DO UPDATE SET
    schedule = EXCLUDED.schedule,
    schedule_natural = EXCLUDED.schedule_natural,
    job_kind = EXCLUDED.job_kind,
    target = EXCLUDED.target,
    target_config = EXCLUDED.target_config,
    enabled = EXCLUDED.enabled,
    max_retries = EXCLUDED.max_retries,
    backoff_seconds = EXCLUDED.backoff_seconds;

COMMIT;
