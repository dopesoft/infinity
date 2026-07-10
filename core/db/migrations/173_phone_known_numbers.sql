-- 173_phone_known_numbers.sql
--
-- Caller recognition for Jarvis's phone line. phone:known_numbers is a
-- boss-editable JSON object mapping phone numbers → an identity note that
-- gets injected into the call instructions when that number calls (matched
-- on the last 10 digits, so formatting never matters). The NOTE is the
-- judgment ("who this is and how to treat them") — it lives here as data,
-- never in Go (Rule #1b). Adding a person = one state_set, no deploy.
--
-- Seeded with the boss's cell (the number his first test call came from,
-- 2026-07-10). ON CONFLICT DO NOTHING so a hand-edited list is never
-- clobbered by a re-run.

BEGIN;

INSERT INTO mem_agent_state (key, value, note)
VALUES (
  'phone:known_numbers',
  '{
    "+16095003990": "This is Khaya - the boss himself, the man you work for. Drop the screening posture entirely: greet him warmly by name as his Jarvis, speak freely, and do whatever he asks within your abilities. He does not need to be vetted, and you never take a message FROM him - you take instructions."
  }'::jsonb,
  'Caller-ID recognition for the phone line: number -> who it is + how to treat them. Managed via state_set.'
)
ON CONFLICT (key) DO NOTHING;

COMMIT;
