package sessions

// renderable.go — the single definition of "this session has something to
// show", shared by everything that has an opinion about it.
//
// The boss's rule is that an empty session is of no use to him, so three
// different things have to agree on what "empty" means: the transcript endpoint
// that renders a session, the list that offers it to him, and the namer that
// decides whether a session is worth titling. Three copies of a SQL fragment
// drift on the first new hook, and the drift shows up as blank rows in his
// history. One const, three importers, no drift.

// ConversationHooksSQL is what the CONVERSATION is made of: the messages he
// and Jarvis actually exchanged. Two very different consumers need exactly
// this set and must never disagree about it - the transcript Studio renders,
// and the history Core replays to the model when a session is faulted back
// into memory.
//
// They DID disagree, on 2026-09-01, and it cost him a turn. AssistantMessage
// was added here for the UI while the model's rebuild kept its own hardcoded
// copy of the list; so two of Jarvis's own answers became invisible to him,
// and the next turn re-answered a point from three messages earlier. That is
// the drift this file exists to prevent, reintroduced by hardcoding the list
// somewhere else. One const, every consumer.
const ConversationHooksSQL = `'UserPromptSubmit', 'TaskCompleted', 'AssistantMessage', 'DashboardSeed'`

// RenderableHooksSQL is the hook whitelist the transcript query reads: the
// conversation, plus the tool cards that only the UI shows. An observation
// under any other hook_name is invisible in the UI no matter what it contains.
const RenderableHooksSQL = ConversationHooksSQL + `, 'PostToolUse', 'PostToolUseFailure'`

// HasRenderableSQL is a parameterless boolean SQL fragment expecting a
// mem_sessions row aliased `s` in scope. True when the transcript query would
// emit at least one message for that session.
//
// It mirrors the renderer's per-row skips exactly:
//   - a tool card without a tool_call_id is dropped, so it doesn't count
//   - any other hook with empty text is dropped, so it doesn't count
//   - an errored turn surfaces as the red card even with no observations, so it counts
const HasRenderableSQL = `(
	EXISTS (
		SELECT 1 FROM mem_observations o
		 WHERE o.session_id = s.id
		   AND (
		         (o.hook_name IN ('PostToolUse', 'PostToolUseFailure')
		            AND btrim(COALESCE(o.payload->>'tool_call_id', '')) <> '')
		      OR (o.hook_name IN (` + ConversationHooksSQL + `)
		            AND btrim(COALESCE(o.raw_text, '')) <> '')
		   )
	)
	OR EXISTS (
		SELECT 1 FROM mem_turns t
		 WHERE t.session_id = s.id
		   AND t.status = 'errored'
		   AND btrim(COALESCE(t.error, '')) <> ''
	)
)`
