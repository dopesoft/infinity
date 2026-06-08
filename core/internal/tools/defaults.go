package tools

import "context"

// RegisterDefaults wires native tools (no MCP yet - those come via mcp.go).
// Phase 2 expands this list. Phase 1 starts empty so the chat path works
// even when no API keys for tool providers are configured.
func RegisterDefaults(ctx context.Context, r *Registry) {
	if t, err := NewHTTPFetchFromEnv(); err == nil {
		r.Register(t)
	}
	if t, err := NewWebSearchFromEnv(); err == nil {
		r.Register(t)
	}
	if t, err := NewCodeExecFromEnv(); err == nil {
		r.Register(t)
	}
	// Discipline tools - always available regardless of env config because
	// they're the foundation of the lazy-loading pattern.
	r.Register(&ToolSearch{Registry: r})
	r.Register(&LoadTools{Registry: r})
	r.Register(&UnloadTools{})
}

// CorePinnedTools is the set of tools whose schemas are ALWAYS shipped to
// the LLM regardless of session state. These are the discipline primitives
// the model needs to reach for the rest of the system - without them it
// can't discover, load, or compact. Keep this list short.
func CorePinnedTools() []string {
	return []string{
		"tool_search",
		"load_tools",
		"unload_tools",
		"compact_context",
		"delegate",
		// background_build - fire-and-forget heavy work on the settings
		// model. Pinned so it's ALWAYS available, especially in the metered
		// voice session, where the realtime model must be able to hand off
		// a build without a tool_search round-trip.
		"background_build",
		// Memory tools - Jarvis's whole edge ("recall before you answer,
		// remember what matters"). These MUST be in hand every turn, never
		// dormant. The registered names are recall / remember / forget (see
		// memory_tools.go); earlier this list pinned memory_search /
		// memory_recall, which no tool answers to, so the pins were no-ops
		// and the memory tools silently fell to the dormant catalog -
		// contradicting the soul's "memory is your edge" principle.
		"recall",
		"remember",
		"forget",
		// system_map is the agent's introspection of "which UI surface is
		// backed by which table is operated on by which tool". Pinned so
		// the agent can resolve any "do X on my dashboard / queue / list"
		// request without prompt-level memorization. Lightweight, read-
		// only, cheap to keep loaded.
		"system_map",
		// domain_hint_add lets the agent extend system_map's topology
		// without a deploy. Pinned alongside system_map so the loop is
		// closed in one turn - introspect, learn, persist.
		"domain_hint_add",
		// mem_substrate - generic read/write over any mem_* table.
		// Pinned so the agent can act on any newly-discovered surface
		// in a single iteration without first calling load_tools.
		"mem_list",
		"mem_act",
		"action_register",
		// Plan substrate ("the Cortex"). Planning is load-bearing for any
		// non-trivial task and the verify-before-done reflex must always be
		// in hand - pinned so the agent never has to tool_search for it
		// before laying out or advancing a plan.
		"plan_create",
		"plan_update",
		"plan_verify",
		"plan_get",
	}
}

// DefaultLoadedTools is the curated baseline set every fresh session opens
// with. Beyond the pinned core, this is what the model has on tap before
// it ever calls tool_search. Anything not in this list lives in the
// dormant catalog (one line per tool in the system prompt) and is loadable
// on demand. Tune this list - it's the dial between "ready out of the
// box" and "context budget".
func DefaultLoadedTools() []string {
	return []string{
		// Web reach - cheap and universally useful.
		"web_search",
		"http_fetch",
		// media_job - the generic media-producing runner. Loaded by default so
		// "make me an image/video" works out of the box: the generate-media
		// skill crafts a higgsfield command and hands it to media_job, which
		// tracks/downloads/indexes the assets into the Media tab + Library.
		"media_job",
		// Document generation - "make me a spreadsheet/report/deck" should
		// work out of the box (boss wants it default). Renders via the
		// workspace bridge's baked helpers; no-op if no workspace wired.
		"document_create",
		// Claude Code bridge - most boss sessions touch the home Mac.
		"claude_code__Read",
		"claude_code__Write",
		"claude_code__Edit",
		"claude_code__Bash",
		"claude_code__Grep",
		"claude_code__Glob",
		"claude_code__LS",
		// Skill self-authoring. These let the agent crystallize repeated
		// multi-step recipes into named skills mid-conversation. The
		// SkillProposalCard component in Studio pattern-matches these
		// tool names to render an inline Approve/Edit/Dismiss card, so
		// they need to be available without a tool_search round-trip.
		"skill_propose",
		"skill_proposal_get",
		"skill_optimize",
	}
}

// NewDefaultActiveSet returns an ActiveSet seeded with the discipline
// core (pinned) plus the curated default loadout. Used by the agent
// loop when a session is first created.
func NewDefaultActiveSet() *ActiveSet {
	s := NewActiveSet(CorePinnedTools())
	s.Load(DefaultLoadedTools(), 0)
	return s
}
