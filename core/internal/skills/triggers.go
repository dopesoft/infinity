package skills

import (
	"sort"
	"strings"
	"unicode"
)

// MatchTriggers fuzzy-matches the user message against every trigger phrase
// across every active skill. Phrases scoring above 0.5 are returned, sorted by
// score (descending) and capped at limit.
//
// We use a hybrid token-overlap + substring score that's robust to small
// rephrasings ("weekly standup" vs "what did i do this week") without pulling
// in a fuzzy-string dependency.
func MatchTriggers(message string, skills []*Skill, limit int) []Match {
	if message == "" || len(skills) == 0 {
		return nil
	}

	msg := normalise(message)
	msgTokens := tokens(msg)

	var matches []Match
	seen := make(map[string]bool)
	for _, s := range skills {
		if s == nil || s.Status != StatusActive {
			continue
		}
		bestScore := 0.0
		bestPhrase := ""
		for _, phrase := range s.TriggerPhrases {
			if phrase == "" {
				continue
			}
			score := phraseScore(msg, msgTokens, phrase)
			if score > bestScore {
				bestScore = score
				bestPhrase = phrase
			}
		}
		if bestScore > 0.5 && !seen[s.Name] {
			matches = append(matches, Match{Skill: s, Score: bestScore, Phrase: bestPhrase})
			seen[s.Name] = true
		}
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].Score > matches[j].Score })
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

func phraseScore(msg string, msgTokens []string, phrase string) float64 {
	p := normalise(phrase)
	if p == "" {
		return 0
	}

	// Substring: full phrase appears verbatim → strongest signal.
	if strings.Contains(msg, p) {
		return 1.0
	}

	pTokens := tokens(p)
	if len(pTokens) == 0 {
		return 0
	}

	// Jaccard overlap on the token sets, clamped.
	have := make(map[string]bool, len(msgTokens))
	for _, t := range msgTokens {
		have[t] = true
	}
	overlap := 0
	for _, t := range pTokens {
		if have[t] {
			overlap++
		}
	}
	if overlap == 0 {
		return 0
	}
	return float64(overlap) / float64(len(pTokens))
}

func normalise(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func tokens(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Fields(s)
	out := parts[:0]
	for _, p := range parts {
		if len(p) <= 2 && !isMeaningfulShortToken(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func isMeaningfulShortToken(s string) bool {
	switch s {
	case "ai", "id", "go", "ok", "ip", "db", "ui":
		return true
	}
	return false
}
