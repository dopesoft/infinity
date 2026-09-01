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

// RenderableHooksSQL is the hook whitelist the transcript query reads. An
// observation under any other hook_name is invisible in the UI no matter what
// it contains. Add a hook here and every consumer agrees at once.
const RenderableHooksSQL = `'UserPromptSubmit', 'TaskCompleted', 'AssistantMessage', 'DashboardSeed', 'PostToolUse', 'PostToolUseFailure'`

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
		      OR (o.hook_name IN ('UserPromptSubmit', 'TaskCompleted', 'DashboardSeed')
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
