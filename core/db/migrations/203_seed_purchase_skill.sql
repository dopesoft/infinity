-- 203: seed the `buying` skill.
--
-- WHY THIS SKILL IS SHORT
--
-- Rule #1b: skills carry JUDGMENT, code carries MECHANICS. Everything that
-- must happen the same way every time already lives in Go and cannot be
-- forgotten by a model having an off day:
--
--   binding merchant/cart/total          purchase_propose + the obligation row
--   one charge, ever                     the atomic claim in purchase.Store
--   re-checking the total before paying  purchase.VerifyTerms, inside the fill
--   stopping for approval                PurchaseGate, at the chokepoint
--   never retrying an uncertain charge   the state machine, terminal by design
--   keeping the card out of context      the vault + the server-side splice
--
-- So this skill does NOT say "remember to check the total" or "be careful with
-- money". If a sentence here were the only thing standing between the boss and
-- a double charge, that would be a bug in the code, not a gap in the prose.
-- What is left is the part a model actually has to decide: WHICH thing to buy,
-- and WHEN to ask instead of choosing.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs, confidence, status, source)
VALUES (
  'buying',
  'Choosing what to buy and when to ask, once the boss has said to buy something.',
  'high',
  '[]'::jsonb,
  '["buy", "order", "purchase", "get me", "reorder", "replacement part"]'::jsonb,
  '[{"name":"what","type":"string","required":true,"doc":"What he asked for, in his words"}]'::jsonb,
  '[{"name":"outcome","type":"string","required":true,"doc":"What was bought and the order number, or why nothing was"}]'::jsonb,
  0.5,
  'active',
  'manual'
) ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES ('buying', 1, $skill$---
name: buying
version: 1
description: Choosing what to buy and when to ask, once the boss has said to buy something.
trigger_phrases: ["buy", "order", "purchase", "get me", "reorder", "replacement part"]
inputs:
  - name: what
    type: string
    required: true
outputs:
  - name: outcome
    type: string
    required: true
---

# Buying

The machinery around this is already safe. Binding the purchase, stopping for
his approval, paying once, checking the total against the live page and
refusing to retry anything uncertain all happen in code whether or not you
think about them. So spend your attention on the two things that are actually
yours to get right.

## Which one to buy

Match what he asked for, not what is cheapest and not what is best. A
"replacement part" means the one that fits the thing he owns; confirm the model
number off the appliance, the manual, or a previous order before you pick.
Prefer the seller he has bought from before over a marginally cheaper stranger.

Where a choice is genuinely close, choose and tell him what you chose and why,
in one line. He can see the cart on the approval card, so a decision you can
explain in a sentence does not need a question first.

## When to ask instead

Ask before proposing when getting it wrong would be annoying to undo:

- more than one variant could plausibly be right (size, fit, model year) and
  nothing you can find settles it
- the price is far off what he would expect for the thing he described
- it is a subscription or anything that renews
- it would arrive too late to be useful
- it is a gift, or otherwise about somebody else's taste

Otherwise propose it. He approves the exact cart and total anyway, so an
unnecessary question just makes him answer the same thing twice.

## Paying over the phone

If the merchant has no usable checkout, pay-on-arrival or pay-on-pickup beats
reading a card down a phone line. Only fall back to a phone payment when the
errand genuinely cannot complete otherwise.

## What to tell him afterwards

The order number, what it cost, and when it arrives if the merchant said. If it
ended uncertain, say exactly that: that you submitted it, could not confirm it,
and have not tried again. Never round an uncertain outcome up to a success or
down to a failure, because he will act on what you tell him.
$skill$, '', 0.5, 'manual')
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('buying', 1)
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
