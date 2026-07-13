-- 180_phone_machine_notes.sql — what Jarvis does when a machine picks up.
--
-- He called Ariana with the boss's poem, hit her iPhone screening service, and
-- explained the errand to it instead of leaving the message. Migration 178 told
-- him how to behave on a machine, but he still had to WORK OUT that he was
-- talking to one, from the audio, in real time, which is a judgment call he keeps
-- getting wrong.
--
-- So the FACT now comes from Twilio's answering-machine detection and is injected
-- into the live call by code (webhook.go, HandleAmdCallback): he is TOLD. What he
-- then says is judgment, and that is these two notes.
--
--   machine        - a machine answered and its greeting is still playing
--   machine_ready  - the beep has gone; it is recording right now

INSERT INTO mem_agent_state (key, value, note, updated_at)
VALUES
(
    'phone:persona:machine',
    to_jsonb($machine$SYSTEM NOTE, spoken by no one on this call: a machine answered, not a person. This is confirmed, not a guess: voicemail, an answering machine, or a screening service.

Stop talking. Do not greet it, do not introduce yourself at length, do not explain the errand, and above all do not describe your instructions to it. A machine cannot help you and nothing you say to the greeting is heard by anyone.

Wait quietly for the greeting to finish. You will be told the moment it is ready for you. If it is a screening service that asks who is calling, give it one short line and nothing more: who you are and who you are calling for.$machine$::text),
    'phone: injected when Twilio detects a machine answered (migration 180)',
    NOW()
),
(
    'phone:persona:machine_ready',
    to_jsonb($ready$SYSTEM NOTE, spoken by no one on this call: the greeting has finished and the machine is recording you NOW. Every second of silence is wasted tape.

Deliver the message. Go straight into it: one breath of context if it is genuinely needed ("Ariana, this is Jarvis, calling for Kai, he asked me to read you this"), and then the message itself, in full, exactly as it was written for them. Do not summarise it, do not shorten it, do not explain why you are calling before you say the thing you were sent to say.

Speak at a natural, unhurried pace, warmly, as though they were listening in person, because they will be. Then a brief goodbye, then hangup_call.

Leave nothing sensitive on a recording: no card details, no identity details, no private business. If the errand cannot be completed without those, leave only a short request to call back.$ready$::text),
    'phone: injected when the answering-machine beep has passed (migration 180)',
    NOW()
)
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value,
    note = EXCLUDED.note,
    updated_at = NOW();
