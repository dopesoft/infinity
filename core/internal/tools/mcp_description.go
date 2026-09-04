package tools

import "strings"

// mcpDescriptionMaxChars caps an MCP tool's description at registration.
//
// Remote servers write descriptions for THEIR model, not ours. Claude Code's
// own `Bash` tool ships a 10,069-char description (git commit etiquette, PR
// bodies, sandbox notes) that is guidance for Claude Code's loop and noise in
// Infinity's: measured against the live bridge on 2026-09-04, the four
// claude_code__* tools in the default loadout came to 17.7K chars of schema,
// ~2.5K tokens re-sent on every LLM call of every turn on every API brain,
// and re-cached at full price each time the tool array changed. The first
// paragraph carries what a tool does; the rest is the other harness's house
// rules. Schemas are never touched.
const mcpDescriptionMaxChars = 600

// trimToolDescription keeps the first paragraph of a description, capped at
// mcpDescriptionMaxChars, with an ellipsis when anything was cut.
func trimToolDescription(desc string) string {
	d := strings.TrimSpace(desc)
	if d == "" {
		return d
	}
	if i := strings.Index(d, "\n\n"); i > 0 {
		d = strings.TrimSpace(d[:i])
	}
	if len(d) > mcpDescriptionMaxChars {
		cut := mcpDescriptionMaxChars
		// Cut on a word boundary when one is near, so the tail never ends
		// mid-token.
		if j := strings.LastIndexByte(d[:cut], ' '); j > cut-40 {
			cut = j
		}
		d = strings.TrimSpace(d[:cut]) + "…"
	}
	return d
}
