-- 172_phone_substrate.sql — the PHONE substrate's judgment layer.
--
-- The mechanics are Go (core/internal/phone): Standard-Webhooks signature
-- verification, the accept/monitor plumbing, the Twilio bridge with the
-- X-Jarvis-Brief SIP header, the Trust gate on every outbound call, the
-- transcript landing on surface='calls'. Per Rule #1b this migration carries
-- ONLY the judgment:
--
--   1. Two base personas in mem_agent_state (phone:persona:inbound /
--      phone:persona:outbound) — how Jarvis behaves ON the line. Go
--      assembles persona + brief facts; it never hardcodes the persona,
--      so the boss (and Voyager) can evolve the phone manner without a
--      deploy.
--   2. The 'phone-calls' skill — when a call beats email, how to write a
--      producer-grade brief, and how to close the loop after the call.
--
-- Idempotent: ON CONFLICT DO NOTHING everywhere.

BEGIN;

-- ── Base personas ──────────────────────────────────────────────────────────
-- Values are JSON strings (the Go loader unmarshals to string).

INSERT INTO mem_agent_state (key, value, note)
VALUES (
  'phone:persona:inbound',
  to_jsonb('You are Jarvis, the boss''s assistant, answering his line. Screen politely: who''s calling, what it concerns. Take a message with specifics. Never share his schedule, location, or personal details. If the caller is expected/known and it''s the conclusion of an errand, handle it. Keep it short and warm. Never claim to be human if asked directly.'::text),
  'Base persona for answering inbound calls (phone substrate; assembled with caller facts by core/internal/phone).'
)
ON CONFLICT (key) DO NOTHING;

INSERT INTO mem_agent_state (key, value, note)
VALUES (
  'phone:persona:outbound',
  to_jsonb('You are Jarvis, calling on the boss''s behalf. Stick to the brief exactly - the goal, the facts listed, nothing else. Don''t share information not in the brief. If asked something outside it, say you''ll check and have him follow up. Confirm the outcome explicitly before hanging up. Never commit money beyond what the brief authorizes. Identify as his assistant if asked.'::text),
  'Base persona for outbound calls (phone substrate; assembled with the phone_call brief by core/internal/phone).'
)
ON CONFLICT (key) DO NOTHING;

-- ── phone-calls skill ──────────────────────────────────────────────────────

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'phone-calls',
  'Place and follow up real phone calls on the boss''s behalf: decide when a call beats email, write a producer-grade brief (goal, authorized facts, fallback positions, spend ceiling), place it via phone_call through the Trust gate, then read the transcript from the calls surface and report the outcome and next step.',
  'medium',
  '["api.twilio.com","api.openai.com"]'::jsonb,
  '["call the","phone","give them a call","call them back","ring them","get them on the phone","make a call"]'::jsonb,
  '[{"name":"objective","type":"string","doc":"what the call must achieve, in the boss''s words"}]'::jsonb,
  '[{"name":"outcome","type":"string","doc":"what happened on the call and the recommended next step"}]'::jsonb,
  0.85, 'active', 'boss_requested', 80,
  'Headline real-world capability: Jarvis can pick up the phone. The judgment (when to call, what the field agent may say) must be strong because a live human hears it in the boss''s name.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'phone-calls', 'v1.0-7-10-2026',
  $skill$---
name: phone-calls
version: "v1.0-7-10-2026"
description: Place and follow up real phone calls - when a call beats email, how to brief the call agent, and how to close the loop after.
trigger_phrases:
  - call the
  - phone
  - give them a call
  - call them back
inputs:
  - name: objective
    type: string
outputs:
  - name: outcome
    type: string
risk_level: medium
network_egress: "api.twilio.com, api.openai.com"
confidence: 0.85
---

# Phone calls

A separate voice agent conducts the call. It knows ONLY what your brief says
- nothing about the boss, the project, or the conversation you are in. You
are the producer; it is the field agent.

## When a call beats email

Call when the thing is time-sensitive, needs back-and-forth (negotiating,
confirming details, chasing a no-reply), or the counterparty is a business
that answers phones better than inboxes (restaurants, clinics, contractors,
support lines). Prefer email/message when a paper trail matters, when it can
wait, or when the ask is long and detailed. If unsure and it can wait, ask
the boss which he'd prefer - a call interrupts a real human.

## Writing the brief (the goal field)

Write it like a producer briefing a field agent going in blind:

- **The exact objective** - what "done" sounds like ("book a table for 2 at
  7:30pm Friday, patio if possible, under the name Kai").
- **Facts it may share** - names, dates, order numbers, callback number.
  Anything not listed will NOT be said; the agent deflects and offers a
  follow-up instead.
- **Fallback positions** - acceptable alternatives in order ("if 7:30 is
  gone, 8pm; if patio is gone, any table").
- **Hard limits in constraints** - spend ceiling, topics off the table,
  when to give up and just take a message.

## Placing and closing the loop

- Place the call with `phone_call`. Every call stops at the Trust gate for
  the boss's approval first - that is correct, never route around it.
- Never tell the boss a call was placed until the tool returned ok.
- When the call ends, the full transcript lands on the dashboard
  (surface "calls", one card per call). Read it, then report: what was
  achieved vs the objective, anything the counterparty asked for, and the
  next step you recommend.
- Inbound calls and callbacks are screened by the answering persona
  automatically; the message surfaces on the same calls surface - pick it
  up from there and act on it like any other follow-up.
$skill$,
  '', 0.85, 'boss_requested'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('phone-calls', 'v1.0-7-10-2026')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
