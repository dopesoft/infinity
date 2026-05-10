package skills

// MatchAndPrefix is the agent.SkillMatcher entry-point: fuzzy-match the
// message against trigger phrases and return a system-prompt chunk listing
// the suggested skills. Empty string when nothing matches.
//
// This satisfies the agent.SkillMatcher interface without dragging the agent
// package into a depend-cycle on this package.
func (r *Registry) MatchAndPrefix(message string, limit int) string {
	if r == nil {
		return ""
	}
	matches := r.Match(message, limit)
	return SuggestionPrefix(matches)
}
