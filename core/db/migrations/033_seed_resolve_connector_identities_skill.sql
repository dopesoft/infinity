-- 033_seed_resolve_connector_identities_skill.sql
--
-- Seed the `resolve-connector-identities` default skill. This is the
-- generic, toolkit-agnostic recipe the agent runs to learn the real
-- upstream identity (email / handle / username / login) of every
-- connected_account it sees in the <connected_accounts> block.
--
-- Why a skill, not Go: Composio's listing API does not reliably surface
-- the OAuth identity (Gmail returns ca_xxx with no emailAddress; Slack
-- with no user handle). Hardcoding "for gmail call GMAIL_GET_PROFILE,
-- for slack call SLACK_AUTH_TEST, ..." in Go would force a code change
-- every time Composio onboards a new toolkit. Putting the heuristic in
-- a SKILL.md keeps the cognition where it belongs - in versioned text
-- the agent reads, follows, and Voyager can evolve - while the durable
-- infrastructure (the `connector_identity_set` tool + the identity
-- overlay in <connected_accounts>) stays generic in Go.
--
-- Trigger surfaces:
--   1. The system-prompt overlay (cache.SystemPromptBlock) tells the
--      agent to invoke this skill whenever it sees accounts with no
--      identity. So the very next turn after deploy / after a new
--      account is connected, the agent self-resolves.
--   2. Boss can fire manually with one of the trigger phrases.
--   3. Composes naturally with the heartbeat tick - when the cache
--      reports missing identities, a heartbeat checklist (future) can
--      surface a finding that points at this skill.
--
-- Idempotency: ON CONFLICT DO NOTHING throughout; never clobbers a
-- Voyager-evolved version.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source)
VALUES (
  'resolve-connector-identities',
  'Discover and persist the real upstream identity (email / handle / username / login) for every connected_account that has no identity in the <connected_accounts> overlay. Generic across toolkits - finds the toolkit''s identity verb on its own, calls it once per account, and writes the result back via connector_identity_set so future turns see the identity automatically.',
  'low',
  '[]'::jsonb,
  '["resolve my connector identities","learn my connected account identities","what email is connected","figure out which email/account is connected","which gmail is which","identify my connected accounts","fix unknown account identities"]'::jsonb,
  '[]'::jsonb,
  '[{"name":"resolved","type":"array","doc":"list of {account_id, toolkit_slug, identity, source_verb} entries that were resolved this run; empty when nothing was unresolved"},{"name":"unresolved","type":"array","doc":"list of {account_id, toolkit_slug, reason} entries that could NOT be resolved (no profile verb, verb errored, response had no identifiable field) - surface these so the boss can investigate or alias them manually"}]'::jsonb,
  0.95,
  'active',
  'manual'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'resolve-connector-identities',
  '1.0.0',
  $skill$---
name: resolve-connector-identities
version: "1.0.0"
description: Discover and persist the real upstream identity (email / handle / username / login) for every connected_account that has no identity in the <connected_accounts> overlay. Generic across toolkits - finds the toolkit's identity verb on its own, calls it once per account, and writes the result back via connector_identity_set so future turns see the identity automatically.
trigger_phrases:
  - resolve my connector identities
  - learn my connected account identities
  - what email is connected
  - figure out which email/account is connected
  - which gmail is which
  - identify my connected accounts
  - fix unknown account identities
inputs: []
outputs:
  - name: resolved
    type: array
    doc: list of {account_id, toolkit_slug, identity, source_verb} entries that were resolved this run; empty when nothing was unresolved
  - name: unresolved
    type: array
    doc: list of {account_id, toolkit_slug, reason} entries that could NOT be resolved (no profile verb, verb errored, response had no identifiable field) - surface these so the boss can investigate or alias them manually
risk_level: low
network_egress: none
confidence: 0.95
---

# Resolve connector identities

Your <connected_accounts> overlay shows every Composio account the boss has
connected, with `id`, `alias` (the boss-chosen short label), and sometimes
`identity` (the real upstream handle - Gmail's emailAddress, Slack's user, etc.).
Composio's listing API doesn't reliably populate `identity`, so when an account
has no `identity` the boss's next turn lands without knowing *which* email or
workspace each account refers to.

Your job: for every account missing its identity, find the right "profile" verb
in that toolkit's catalog, call it once, pull the canonical handle from the
response, and persist via `connector_identity_set`. After this runs once per
account the identity sticks forever (it's stored in `infinity_meta`) - boot,
restart, and future turns all see it.

This recipe is **generic**. Do not assume any specific toolkit. Apply the same
algorithm to Gmail today, Notion tomorrow, and whatever Composio adds next year.

## 1. Enumerate what's unresolved

Read the `<connected_accounts>` overlay in your system prompt. Build a list of
`{account_id, toolkit_slug}` for every account whose `identity` field is empty
or missing. If the list is empty, finish immediately and report
`resolved: [], unresolved: []` - you have nothing to do.

## 2. For each unresolved account, find the toolkit's identity verb

Each toolkit pre-registers its verbs under `composio__<TOOLKIT>_<VERB>`. The
verb that returns the connected user's profile follows one of a small handful
of naming patterns. In order of preference:

1. `*_GET_PROFILE` (Gmail's `GMAIL_GET_PROFILE` is the canonical case)
2. `*_AUTH_TEST` (Slack's `SLACK_AUTH_TEST`)
3. `*_GET_AUTHENTICATED_USER` (GitHub's `GITHUB_GET_AUTHENTICATED_USER`)
4. `*_GET_ME` / `*_ME`
5. `*_CURRENT_USER` / `*_WHOAMI`
6. `*_USER_INFO` / `*_USERINFO`
7. `*_GET_USER` (only when there's no narrower option above)

Use `tool_search` with a query like `"<toolkit_slug> profile"` or
`"<toolkit_slug> get me"` to surface the candidate verb, then `load_tools` it
so it's available. If `tool_search` returns multiple matches, prefer the one
whose verb name contains PROFILE → AUTH_TEST → AUTHENTICATED → ME → CURRENT →
WHOAMI → USERINFO → USER, in that order. If nothing in the catalog matches
any of these patterns, the toolkit doesn't expose a profile verb - record
the account in `unresolved` with `reason: "no profile verb in toolkit catalog"`
and move on.

## 3. Call the verb with the connected_account_id

Invoke the verb you found, passing `connected_account_id: "<the ca_xxx>"` from
the unresolved account. Use the toolkit's verb directly - do not reach for
`composio__COMPOSIO_REMOTE_BASH_TOOL` or `_REMOTE_WORKBENCH`. Those are
fallbacks for toolkits whose verbs failed to pre-register; profile verbs do
not need them.

If the verb call errors (connection inactive, rate-limited, permission denied),
record the account in `unresolved` with `reason: "<the error message, trimmed>"`
and move on. Do not retry - one shot per account per run.

## 4. Extract the canonical handle from the response

Pull the first non-empty value from these candidate fields, in this order:

1. `emailAddress` (Gmail's exact field name)
2. `email`
3. `user.email`, `profile.email`, `data.email` (one level of nesting)
4. `login` (GitHub)
5. `user.login`, `user.name`
6. `username`, `user.username`
7. `handle`, `user.handle`
8. `display_name`, `name`, `user.name`

The first field that holds a non-empty string is the identity. Trim it. Skip
generic placeholders like "me", "user", "anonymous" - if the only candidate
is one of those, record the account in `unresolved` with
`reason: "response had no usable identity field"`.

## 5. Persist via connector_identity_set

For each `{account_id, identity}` pair you resolved, call:

```
connector_identity_set({account_id: "<ca_xxx>", identity: "<the handle>"})
```

This writes to the `connectors_identities` blob in `infinity_meta` and the
cache picks it up on its next refresh tick (≤60s). Future turns render
`identity="<the handle>"` in `<connected_accounts>` automatically - you will
never have to re-resolve unless the boss disconnects and reconnects.

## 6. Report

Reply with two lists:

- **resolved**: each `{account_id, toolkit_slug, identity, source_verb}` you
  successfully wrote.
- **unresolved**: each `{account_id, toolkit_slug, reason}` you could not
  resolve, with the specific reason (no profile verb, error message, missing
  field). The boss may want to set the alias manually or investigate.

Be concise - one line per account. The boss does not need explanation; the
data is the answer.

## Hard rules - read before acting

- **Idempotent.** Skip any account whose `identity` is already set. Never
  overwrite a non-empty identity unless the boss explicitly asks you to
  re-resolve. If you re-resolve, pass the new value to
  `connector_identity_set`; passing empty string clears it.
- **One call per unresolved account.** Do not retry on error in this run.
  Surface the failure and let a future invocation (or the boss) decide.
- **Toolkit-agnostic.** Never hardcode a toolkit slug in your prompt
  reasoning - the same code path applies to Gmail, Notion, Stripe, anything.
  If you find yourself thinking "for Gmail I'll do X but for Slack I'll do Y,"
  back up: the algorithm is the same; only the verb name differs, and you
  found that with `tool_search`.
- **Dormant or pending accounts.** If an account's `status` in the overlay
  is not ACTIVE (e.g. INITIATED, FAILED, REVOKED), skip it and record
  `reason: "status=<X>, account not active"` in unresolved. Hitting an
  inactive account wastes a call and returns 401.
- **Never invent identities.** If the verb response has no usable field,
  the account is unresolved. Do not guess from the alias, the account id,
  or the toolkit name.
$skill$,
  '',
  0.95,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('resolve-connector-identities', '1.0.0')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
