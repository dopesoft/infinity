-- 116_inbox_triage_deterministic.sql
--
-- Reference implementation of Rule #1b (skills are JUDGMENT-only; mechanics
-- live in code). Rewrites the inbox-triage skill body down to pure judgment.
-- Every mechanic it used to express as prose is now enforced by code and
-- cannot be dropped by the LLM:
--   • "capture the full email body"  -> surface_item ALWAYS fetches the real
--                                       Gmail body for follow-up emails.
--   • "attach actions"               -> surface_item stamps the default
--                                       Draft/Archive/Snooze action set.
--   • "never send / drafts only"     -> ComposioGate ungates drafts and gates
--                                       sends; drafting never needs approval.
--   • "never resolve/dismiss"        -> tools.IsAutonomous blocks it.
--   • "stable external_id / upsert"  -> surface_item upserts by Gmail id.
-- So the recipe no longer needs to manage them — and a run can no longer
-- silently break by omitting a sentence.
--
-- Ships as a NEW active version (v1.5-deterministic). Idempotent: ON CONFLICT
-- updates the body, and the active pointer is set unconditionally.

BEGIN;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'inbox-triage',
  'v1.5-deterministic',
  $SKILL$# inbox-triage

## Purpose
Find the emails across the boss's Gmail accounts that genuinely need HIS reply, surface them, and draft a reply when one is clearly warranted. Your job here is JUDGMENT only — which mail matters and what a good reply says. The mechanics (discovering mailboxes, fetching the real email body, stamping the action buttons, keying by Gmail id, never sending, never auto-dismissing) are enforced by the tools and cannot break, so you neither manage them nor can drop them.

## Trigger
When the boss asks to triage Gmail, sweep his inboxes, or review mail across connected accounts.

## What you decide (this is the whole job)
1. Of the recent mail across the active mailboxes, which messages GENUINELY await the boss's reply? Ignore FYI, notifications, receipts, newsletters, and threads already handled by someone else.
2. For each that qualifies: how important is it (0-100), and why, in one tight line?
3. When a reply is clearly warranted: what should it say? Draft it in the boss's voice.

## How
- Discover the live mailboxes, then read recent mail per the boss's window (default: recent unread). Under tight context, call Gmail tools directly rather than delegating large per-mailbox prompts.
- For each qualifying message, call `surface_item` on the `followups` surface with: the subject as `title`, a one-line `importance_reason`, and a short `body` saying why it needs the boss. That is ALL you pass — the tool fetches the real email, stamps the Draft/Archive/Snooze actions, and keys the row by the Gmail id on its own. Do not paste the email body or a summary into the body field; the body is your one-line "why", not the email.
- When a reply is warranted, draft it and save it as a Gmail draft. (Drafts never send and never need approval — enforced.)
- Close by reporting which mailboxes you covered and which you couldn't reach.

## Not your call (enforced by the tools — don't attempt it)
Sending mail, or resolving / dismissing a follow-up. Those decisions are the boss's.
$SKILL$,
  '',
  0.9,
  'manual'
)
ON CONFLICT (skill_name, version) DO UPDATE SET skill_md = EXCLUDED.skill_md;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('inbox-triage', 'v1.5-deterministic')
ON CONFLICT (skill_name) DO UPDATE SET active_version = EXCLUDED.active_version, updated_at = NOW();

UPDATE mem_skills
   SET description = 'Judgment-only Gmail triage: decide which mail needs the boss''s reply, surface it, and draft a reply when warranted. All mechanics (real-body fetch, action buttons, never-send, never-dismiss, stable ids) are enforced by the tools, not this recipe (Rule #1b).'
 WHERE name = 'inbox-triage';

COMMIT;
