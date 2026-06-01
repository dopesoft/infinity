package proactive

import "strings"

// tool_policy.go is the single source of truth for "what kind of signal does
// this tool's output carry?" The three proactive detectors (high-surprise
// curiosity, repeated-tool-error self-heal, skill-pattern authoring) all
// consult it instead of hand-rolling per-tool denylists. Adding a tool to a
// class is a one-line edit here; no detector ever grows a per-vendor branch.
//
// Why this exists: the dashboard filled with 12 un-dismissable curiosity
// questions because the detectors treated EVERY tool the same. They asked
// "should I rework the prompt around compact_context?" (an internal summariser
// with no prompt), flagged claude_code__Bash exit-1 as "this tool keeps
// failing" (a non-zero grep is normal coding work), and proposed crystallising
// claude_code__Read -> Bash -> Read into a "skill" (interactive coding, not a
// recipe). Each detector's question is only meaningful for a SUBSET of tools;
// ToolClass encodes which.

// ToolClass categorises a tool by the nature of its output, which determines
// which proactive questions make sense for it.
type ToolClass int

const (
	// ClassActionable: the tool's behaviour is driven by a prompt/instruction
	// the agent controls — a skill, a workflow, the agent's own reasoning.
	// A surprising result here IS a real "rework the prompt" signal.
	ClassActionable ToolClass = iota

	// ClassDataFetch: external connector / network reads (composio__*, gmail/
	// http fetch, web search). The output is variable third-party CONTENT,
	// not a status the agent can tune via prompt. A genuine repeated failure
	// (auth expired) is still actionable, but "surprise" in the content is noise.
	ClassDataFetch

	// ClassInternal: substrate plumbing — compact_context, recall, skills_history,
	// memory_*/mem_*, surface_*. The output is internal machinery; there is no
	// prompt to rework and no reusable domain recipe to crystallise.
	ClassInternal

	// ClassCodingBridge: claude_code__* over the Mac bridge. Non-zero bash
	// exits and Read misses are NORMAL agent work, not tool malfunctions, and
	// interactive coding chains are not reusable skills.
	ClassCodingBridge
)

// ClassifyTool maps a tool name to its ToolClass. Matching is on the lowered
// name via stable prefixes/substrings so new tools in a family classify
// correctly without edits (e.g. any future composio__* read is DataFetch).
func ClassifyTool(name string) ToolClass {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasPrefix(n, "claude_code__"):
		return ClassCodingBridge

	case isInternalTool(n):
		return ClassInternal

	case isDataFetchTool(n):
		return ClassDataFetch

	default:
		return ClassActionable
	}
}

// isInternalTool covers substrate plumbing verbs whose output is internal
// machinery, never a prompt-tunable result or a reusable recipe.
func isInternalTool(n string) bool {
	switch n {
	case "compact_context", "recall", "remember", "skills_history",
		"surface_item", "surface_update", "surface_list",
		"question_list", "load_tools", "tool_search", "system_map",
		"mem_list", "mem_get", "extension_list", "followup_list":
		return true
	}
	return strings.HasPrefix(n, "memory_") || strings.HasPrefix(n, "mem_")
}

// isDataFetchTool covers external connector / network reads. composio__* are
// all third-party API calls; the *_FETCH_* / *_GET_* read verbs in particular
// return content, but the whole family is non-prompt-tunable so we treat the
// prefix uniformly for high-surprise purposes.
func isDataFetchTool(n string) bool {
	if strings.HasPrefix(n, "composio__") || strings.HasPrefix(n, "github__") {
		return true
	}
	if strings.Contains(n, "fetch") || strings.Contains(n, "httpfetch") ||
		strings.Contains(n, "websearch") || strings.Contains(n, "web_search") {
		return true
	}
	return false
}

// EligibleForHighSurprise reports whether a "Tool X returned something
// unexpected, should I rework the prompt around it?" curiosity question is
// meaningful for this tool. Only ClassActionable tools qualify — for fetch /
// internal / coding-bridge tools there is no prompt to rework, and their
// output content routinely trips the outcome classifier into a false "error".
func EligibleForHighSurprise(tool string) bool {
	return ClassifyTool(tool) == ClassActionable
}

// EligibleForRepeatedError reports whether "Tool X keeps failing. Fix it?"
// is a real self-heal signal. Coding-bridge tools are excluded: a non-zero
// claude_code__Bash exit (grep miss, failing build) or a Read on a missing
// path is normal agent work, not a broken tool. Data-fetch and internal
// tools stay eligible — a repeatedly-failing connector or substrate call IS
// actionable (reconnect, fix the bug).
func EligibleForRepeatedError(tool string) bool {
	return ClassifyTool(tool) != ClassCodingBridge
}

// SubstantiveForSkillPattern reports whether a tool counts as a "substantive"
// step when deciding if a recurring 3-tool chain is worth crystallising into
// a skill. Coding-bridge and internal tools don't count — a chain made only of
// claude_code__* (interactive coding) or substrate plumbing is not a reusable
// domain recipe. Used to extend lowValueToolSignature.
func SubstantiveForSkillPattern(tool string) bool {
	switch ClassifyTool(tool) {
	case ClassCodingBridge, ClassInternal:
		return false
	default:
		return true
	}
}
