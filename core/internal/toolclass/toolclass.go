// Package toolclass is the single source of truth for "what kind of signal
// does this tool's output carry?" It is a zero-dependency leaf package so any
// subsystem can consult it without import cycles: the proactive detectors
// (high-surprise curiosity, repeated-tool-error self-heal, skill-pattern
// authoring) and the plasticity Gym (which mines high-surprise predictions
// into training examples) all classify tools the same way.
//
// Why this exists: a deterministic classifier treated EVERY tool the same.
// It asked "should I rework the prompt around compact_context?" (an internal
// summariser with no prompt), flagged claude_code__Bash exit-1 as "this tool
// keeps failing" (a non-zero grep is normal coding work), proposed
// crystallising claude_code__Read -> Bash -> Read into a "skill" (interactive
// coding, not a recipe), and distilled operational failures (a composio fetch
// with a bad id, a loop-blocked compact_context) into Gym "lessons" the agent
// reads every turn. Each consumer's signal is only meaningful for a SUBSET of
// tools; ToolClass encodes which.
package toolclass

import "strings"

// ToolClass categorises a tool by the nature of its output, which determines
// which proactive / curriculum signals make sense for it.
type ToolClass int

const (
	// ClassActionable: the tool's behaviour is driven by a prompt/instruction
	// the agent controls — a skill, a workflow, the agent's own reasoning.
	// A surprising result here IS a real "rework the prompt" / curriculum signal.
	ClassActionable ToolClass = iota

	// ClassDataFetch: external connector / network reads (composio__*, github__*,
	// http fetch, web search). The output is variable third-party CONTENT, not a
	// status the agent can tune via prompt. A genuine repeated failure (auth
	// expired) is still actionable, but "surprise" in the content is noise and
	// it is not a behavioural lesson worth distilling.
	ClassDataFetch

	// ClassInternal: substrate plumbing — compact_context, recall, skills_history,
	// memory_*/mem_*, surface_*. The output is internal machinery; there is no
	// prompt to rework and no reusable domain recipe to crystallise.
	ClassInternal

	// ClassShellFileOps: shell + filesystem operations, whether over the Mac
	// bridge (claude_code__Bash/Read/Edit/Write) or via the native cloud-computer
	// twins (bash_run, fs_read, fs_edit, fs_save, ...). A non-zero exit or a
	// missing-path read is NORMAL agent work, not a tool malfunction; interactive
	// coding chains are not reusable skills; and these failures are not
	// behavioural lessons worth distilling. The bridged and native forms are the
	// same category and must classify identically.
	ClassShellFileOps
)

// Classify maps a tool name to its ToolClass. Matching is on the lowered name
// via stable prefixes/substrings so new tools in a family classify correctly
// without edits (e.g. any future composio__* read is DataFetch).
func Classify(name string) ToolClass {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case isShellFileOp(n):
		return ClassShellFileOps
	case isInternal(n):
		return ClassInternal
	case isDataFetch(n):
		return ClassDataFetch
	default:
		return ClassActionable
	}
}

// isShellFileOp covers shell execution and filesystem reads/writes — the Mac
// bridge tools (claude_code__*) and their native cloud-computer twins. Exit
// codes and missing-path reads are normal operation here, never a signal.
func isShellFileOp(n string) bool {
	switch n {
	case "bash_run", "fs_read", "fs_edit", "fs_save", "fs_write", "fs_list", "fs_delete":
		return true
	}
	return strings.HasPrefix(n, "claude_code__") || strings.HasPrefix(n, "fs_")
}

// isInternal covers substrate plumbing verbs whose output is internal
// machinery, never a prompt-tunable result or a reusable recipe.
func isInternal(n string) bool {
	switch n {
	case "compact_context", "recall", "remember", "skills_history",
		"surface_item", "surface_update", "surface_list",
		"question_list", "load_tools", "tool_search", "system_map",
		"mem_list", "mem_get", "extension_list", "followup_list":
		return true
	}
	return strings.HasPrefix(n, "memory_") || strings.HasPrefix(n, "mem_")
}

// isDataFetch covers external connector / network reads. composio__* / github__*
// are third-party API calls; the whole family is non-prompt-tunable, so we treat
// the prefix uniformly for surprise/curriculum purposes.
func isDataFetch(n string) bool {
	if strings.HasPrefix(n, "composio__") || strings.HasPrefix(n, "github__") {
		return true
	}
	if n == "read_email" {
		return true
	}
	// The agentic browser and extension-registered HTTP tools are the same
	// thing as an API read: whatever comes back is a third party's content, not
	// a status the agent can tune with a better prompt. They were missing here,
	// which meant a surprising web page counted as a curriculum signal.
	if strings.HasPrefix(n, "browser_") || strings.HasPrefix(n, "ext_") {
		return true
	}
	// Any remaining MCP tool (<server>__<tool>) is a third party's server
	// answering. Safe as a catch-all because Classify tests isShellFileOp
	// first, so the Mac coding bridge (claude_code__*) never reaches here — it
	// reads the boss's own machine and is a different kind of output entirely.
	if strings.Contains(n, "__") {
		return true
	}
	return strings.Contains(n, "fetch") || strings.Contains(n, "httpfetch") ||
		strings.Contains(n, "websearch") || strings.Contains(n, "web_search")
}

// statusOnly are the tools that classify as DataFetch by family but hand back a
// one-line status rather than any third-party content. They are named here so
// ReturnsExternalContent doesn't dress "browser closed" up as quoted material.
var statusOnly = map[string]bool{
	"browser_close":            true,
	"browser_request_takeover": true,
}

// ReturnsExternalContent reports whether this tool's output is text written by
// somebody other than the boss — an email body, a web page, a Slack message, an
// API payload.
//
// It is the trigger for the untrusted-content boundary in the agent loop, and
// it is a predicate here rather than a second classifier elsewhere because the
// question it asks ("whose words are these?") is the same question Classify
// already answers; a parallel copy would drift the first time a tool family is
// added. Deliberately excludes claude_code__* and the native filesystem twins:
// those read the boss's OWN machine and his own source, and wrapping his code
// as somebody else's speech would be both wrong and noisy.
func ReturnsExternalContent(tool string) bool {
	n := strings.ToLower(strings.TrimSpace(tool))
	if statusOnly[n] {
		return false
	}
	return Classify(n) == ClassDataFetch
}

// EligibleForHighSurprise reports whether a surprising outcome for this tool is
// a meaningful curriculum signal — i.e. worth a "rework the prompt?" curiosity
// question OR a Gym training example. Only ClassActionable qualifies: for fetch
// / internal / coding-bridge tools there is no prompt to rework, their content
// routinely trips the outcome classifier, and their genuine operational
// failures are not behavioural lessons.
func EligibleForHighSurprise(tool string) bool {
	return Classify(tool) == ClassActionable
}

// EligibleForRepeatedError reports whether "Tool X keeps failing. Fix it?" is a
// real self-heal signal. Coding-bridge tools are excluded: a non-zero
// claude_code__Bash exit (grep miss, failing build) or a Read on a missing path
// is normal agent work. Data-fetch and internal tools stay eligible — a
// repeatedly-failing connector or substrate call IS actionable.
func EligibleForRepeatedError(tool string) bool {
	return Classify(tool) != ClassShellFileOps
}

// SubstantiveForSkillPattern reports whether a tool counts as a "substantive"
// step when deciding if a recurring 3-tool chain is worth crystallising into a
// skill. Coding-bridge and internal tools don't count — a chain made only of
// claude_code__* (interactive coding) or substrate plumbing is not a reusable
// domain recipe.
func SubstantiveForSkillPattern(tool string) bool {
	switch Classify(tool) {
	case ClassShellFileOps, ClassInternal:
		return false
	default:
		return true
	}
}
