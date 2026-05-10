package skills

import (
	"fmt"
	"strings"
)

// SuggestionPrefix builds a short system-prompt block listing skills whose
// trigger phrases matched the user's incoming message. The agent loop folds
// this into the system prompt so the model knows which skills are nearby.
//
// Returns the empty string when no skills matched — callers can concatenate
// unconditionally.
func SuggestionPrefix(matches []Match) string {
	if len(matches) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Suggested skills (from trigger match)\n")
	b.WriteString("These installed skills might be relevant to the user's message. ")
	b.WriteString("You may invoke any of them via skills.invoke; you decide whether the match is real.\n\n")
	for _, m := range matches {
		fmt.Fprintf(&b, "- **%s** (v%s, risk=%s, score=%.2f) — %s\n",
			m.Skill.Name, m.Skill.Version, m.Skill.RiskLevel, m.Score, m.Skill.Description)
	}
	return b.String()
}
