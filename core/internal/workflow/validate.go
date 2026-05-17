package workflow

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var stepRefRe = regexp.MustCompile(`\{\{steps\.(\d+)\.output\}\}`)

// ValidateSteps statically checks a workflow's step list before it runs:
// kinds are valid, each spec carries the field its kind needs, and every
// {{steps.N.output}} reference points at an EARLIER step. Returns a list
// of human-readable problems - empty means the assembly is well-formed.
//
// This is the cheap "verify the assembly before you commit it" check -
// it catches the structural mistakes (a typo'd kind, a forward reference)
// without running anything. Whether a referenced tool/skill exists is
// caught at run time by the engine's retry/fail path.
func ValidateSteps(steps []StepDef) []string {
	if len(steps) == 0 {
		return []string{"workflow has no steps"}
	}
	var problems []string
	for i, st := range steps {
		if !st.Kind.Valid() {
			problems = append(problems, fmt.Sprintf("step %d: invalid kind %q (want tool|skill|agent|checkpoint)", i, st.Kind))
			continue
		}
		switch st.Kind {
		case KindTool:
			if s, ok := st.Spec["tool"].(string); !ok || s == "" {
				problems = append(problems, fmt.Sprintf("step %d (tool): spec.tool is required", i))
			}
		case KindSkill:
			if s, ok := st.Spec["skill"].(string); !ok || s == "" {
				problems = append(problems, fmt.Sprintf("step %d (skill): spec.skill is required", i))
			}
		case KindAgent:
			if s, ok := st.Spec["prompt"].(string); !ok || s == "" {
				problems = append(problems, fmt.Sprintf("step %d (agent): spec.prompt is required", i))
			}
		}
		for _, ref := range collectStepRefs(st.Spec) {
			n, _ := strconv.Atoi(ref)
			if n >= i {
				problems = append(problems, fmt.Sprintf(
					"step %d references {{steps.%d.output}} - a step can only reference an EARLIER step's output", i, n))
			}
		}
	}
	return problems
}

type Rehearsal struct {
	Steps            int              `json:"steps"`
	EstimatedCostUSD float64          `json:"estimated_cost_usd"`
	RiskLevel        string           `json:"risk_level"`
	SideEffects      []string         `json:"side_effects,omitempty"`
	Permissions      []string         `json:"permissions,omitempty"`
	CheckpointAdvice []string         `json:"checkpoint_advice,omitempty"`
	ExpectedFlow     []map[string]any `json:"expected_flow"`
}

// RehearseSteps is a no-side-effect dry run. It does not call tools; it gives
// the agent a pre-flight map of likely risk, permissions, checkpoint placement,
// and rough cost so long workflows do not launch blind.
func RehearseSteps(steps []StepDef) Rehearsal {
	out := Rehearsal{Steps: len(steps), RiskLevel: "low"}
	risk := 0
	hasCheckpoint := false
	seen := map[string]bool{}
	add := func(dst *[]string, v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		*dst = append(*dst, v)
	}
	for i, st := range steps {
		item := map[string]any{"index": i, "kind": string(st.Kind), "name": st.Name}
		switch st.Kind {
		case KindCheckpoint:
			hasCheckpoint = true
			item["expected"] = "pause for boss approval"
		case KindAgent:
			out.EstimatedCostUSD += 0.02
			item["expected"] = "run an agent turn"
		case KindSkill:
			skill, _ := st.Spec["skill"].(string)
			out.EstimatedCostUSD += 0.01
			item["skill"] = skill
			item["expected"] = "invoke skill recipe"
			if looksSideEffectful(skill) {
				risk += 2
				add(&out.SideEffects, "skill:"+skill)
			}
		case KindTool:
			tool, _ := st.Spec["tool"].(string)
			item["tool"] = tool
			item["expected"] = "invoke tool"
			switch {
			case strings.Contains(tool, "search") || strings.Contains(tool, "fetch") || strings.Contains(tool, "list") || strings.Contains(tool, "get"):
				out.EstimatedCostUSD += 0.001
			default:
				out.EstimatedCostUSD += 0.005
			}
			if looksSideEffectful(tool) {
				risk += 2
				add(&out.SideEffects, "tool:"+tool)
			}
			if strings.Contains(tool, "github") || strings.Contains(tool, "gmail") || strings.Contains(tool, "composio") || strings.Contains(tool, "connector") {
				add(&out.Permissions, tool)
			}
		}
		out.ExpectedFlow = append(out.ExpectedFlow, item)
	}
	switch {
	case risk >= 4:
		out.RiskLevel = "high"
	case risk >= 2:
		out.RiskLevel = "medium"
	}
	if risk > 0 && !hasCheckpoint {
		out.CheckpointAdvice = append(out.CheckpointAdvice, "Add a checkpoint before the first side-effectful step.")
	}
	if len(out.ExpectedFlow) > 6 {
		out.CheckpointAdvice = append(out.CheckpointAdvice, "Long workflow: add a midpoint checkpoint or progress surface.")
	}
	out.EstimatedCostUSD = float64(int(out.EstimatedCostUSD*1000+0.5)) / 1000
	return out
}

func looksSideEffectful(name string) bool {
	n := strings.ToLower(name)
	for _, word := range []string{"send", "post", "write", "edit", "delete", "commit", "merge", "push", "purchase", "update", "create", "remove", "approve"} {
		if strings.Contains(n, word) {
			return true
		}
	}
	return false
}

func collectStepRefs(v any) []string {
	var refs []string
	switch x := v.(type) {
	case string:
		for _, m := range stepRefRe.FindAllStringSubmatch(x, -1) {
			refs = append(refs, m[1])
		}
	case map[string]any:
		for _, item := range x {
			refs = append(refs, collectStepRefs(item)...)
		}
	case []any:
		for _, item := range x {
			refs = append(refs, collectStepRefs(item)...)
		}
	}
	return refs
}
