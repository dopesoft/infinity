// Package proposals - evaluator.go: empirical pick when skill merges
// conflict (Opt 3).
//
// The shape of this loop matches GEPA's Pareto principle but at one
// scale smaller: when UpsertCandidate detects len(conflicts) > 0,
// instead of trusting Haiku's blended body we present THREE candidates
// (existing draft, new proposal, merged blend) to a critic-style LLM
// pass and pick the winner. Costs ~$0.001 per conflict, runs in
// seconds, and the rest of the time the merge is clean and this
// never fires.
//
// Full GEPA Pareto evaluation (with synthetic test runs against each
// candidate) is what the voyager/optimizer.go path does for newly
// proposed skills. That's expensive (~$0.05-0.20 per run) and only
// triggered by Voyager autotrigger today. This file is the lighter,
// in-the-merge-loop evaluator - same architectural pattern at a
// cheaper price point.

package proposals

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

const evaluatorModel = "claude-haiku-4-5-20251001"

const evaluatorSystem = `You are a senior staff engineer reviewing three candidate skill bodies for the same parent skill. Pick the one that best balances clarity, correctness, and operational completeness.

Output ONLY the label of the winning candidate on a single line:
  existing
  proposed
  merged

If none is clearly best, output the label whose body is most internally consistent (fewest contradicting steps).`

// HaikuMergeEvaluator implements MergeEvaluator. Constructed with a
// Drafter (same interface UpsertCandidate uses for the merge call) so
// boot wiring stays simple.
type HaikuMergeEvaluator struct {
	drafter Drafter
	logger  *slog.Logger
}

func NewHaikuMergeEvaluator(drafter Drafter, logger *slog.Logger) *HaikuMergeEvaluator {
	if logger == nil {
		logger = slog.Default()
	}
	return &HaikuMergeEvaluator{drafter: drafter, logger: logger}
}

// EvaluateSkillMerge asks Haiku to pick the strongest of the three
// candidates. Returns the body of the winner. On any failure, returns
// "" so UpsertCandidate falls back to Haiku's blended merged body
// (the prior behavior before Opt 3).
func (e *HaikuMergeEvaluator) EvaluateSkillMerge(ctx context.Context, parentSkill string, candidates []SkillCandidate) (string, error) {
	if e == nil || e.drafter == nil {
		return "", nil
	}
	if len(candidates) < 2 {
		// Nothing to compare; fall back to merge.
		if len(candidates) == 1 {
			return candidates[0].Body, nil
		}
		return "", nil
	}
	byLabel := make(map[string]string, len(candidates))
	var b strings.Builder
	fmt.Fprintf(&b, "Parent skill: %s\n\n", parentSkill)
	for _, c := range candidates {
		label := strings.TrimSpace(c.Label)
		if label == "" {
			continue
		}
		byLabel[label] = c.Body
		fmt.Fprintf(&b, "<%s>\n%s\n</%s>\n\n", label, c.Body, label)
	}
	b.WriteString("Pick the winner. Output ONLY the label.")

	raw, err := e.drafter.Draft(ctx, evaluatorModel, evaluatorSystem, b.String(), 50)
	if err != nil {
		return "", err
	}
	pick := normalizeLabel(raw)
	if pick == "" {
		return "", nil
	}
	body, ok := byLabel[pick]
	if !ok {
		return "", nil
	}
	if e.logger != nil {
		e.logger.Info("skill merge evaluator pick", "parent_skill", parentSkill, "winner", pick)
	}
	return body, nil
}

func normalizeLabel(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.Trim(s, "\"'`.")
	if i := strings.IndexAny(s, " \n\t"); i > 0 {
		s = s[:i]
	}
	switch s {
	case "existing", "proposed", "merged":
		return s
	}
	return ""
}
