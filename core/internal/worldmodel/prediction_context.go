package worldmodel

import (
	"context"
	"fmt"
	"path"
	"strings"
)

// PredictionContext (steal B) builds a short world-model causal-context string
// for a pending high-risk tool call, so the pre-act prediction in
// hooks/predict.go becomes a forward simulation grounded in what the agent knows
// about the entities involved — not just a guess from the tool name.
//
// It is deliberately bounded: a couple of indexed entity lookups (the call is
// already on the LLM-drafted prediction path, which dwarfs this cost, and runs
// async off the agent loop). Returns "" when nothing relevant is known, so the
// prediction falls back to the plain prompt.
func PredictionContext(ctx context.Context, s *Store, toolName string, input map[string]any) string {
	if s == nil {
		return ""
	}
	refs := candidateRefs(input)
	if len(refs) == 0 {
		return ""
	}
	const maxEntities = 3
	seen := make(map[string]bool, maxEntities)
	var lines []string
	for _, ref := range refs {
		if len(lines) >= maxEntities {
			break
		}
		ents, err := s.SearchEntities(ctx, ref, "", 2)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e == nil || seen[e.ID] {
				continue
			}
			seen[e.ID] = true
			// GetEntity resolves the causal links; fall back to the search hit.
			full, gerr := s.GetEntity(ctx, e.ID)
			if gerr != nil || full == nil {
				full = e
			}
			lines = append(lines, formatEntityForPrediction(full))
			if len(lines) >= maxEntities {
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

// formatEntityForPrediction renders one entity + its top causal edges compactly.
func formatEntityForPrediction(e *Entity) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s (%s)", e.Name, e.Kind)
	if s := strings.TrimSpace(e.Summary); s != "" {
		if len(s) > 140 {
			s = s[:140] + "…"
		}
		fmt.Fprintf(&b, ": %s", s)
	}
	for i, lv := range e.Links {
		if i >= 2 { // cap edges per entity
			break
		}
		dir := "→"
		if lv.Direction == "in" {
			dir = "←"
		}
		fmt.Fprintf(&b, "\n    %s %s %s", dir, lv.Relation, lv.OtherName)
	}
	return b.String()
}

// candidateRefs pulls distinctive string referents out of a tool input — names,
// paths (and their basenames), repos, urls — that are worth matching against the
// world model. Bounded to a few so the lookups stay cheap.
func candidateRefs(input map[string]any) []string {
	if len(input) == 0 {
		return nil
	}
	const maxRefs = 3
	seen := make(map[string]bool, maxRefs)
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if len(v) < 3 || len(v) > 80 || seen[strings.ToLower(v)] {
			return
		}
		// skip pure-numeric / obviously non-entity tokens
		if strings.IndexFunc(v, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
			return
		}
		seen[strings.ToLower(v)] = true
		out = append(out, v)
	}
	for _, raw := range input {
		if len(out) >= maxRefs {
			break
		}
		s, ok := raw.(string)
		if !ok {
			continue
		}
		add(s)
		// For path-like values, also offer the basename (e.g. "loop.go").
		if strings.Contains(s, "/") {
			if base := path.Base(s); base != s {
				add(base)
			}
		}
	}
	return out
}
