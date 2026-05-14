package workflow

import (
	"fmt"
	"regexp"
	"strconv"
)

var stepRefRe = regexp.MustCompile(`\{\{steps\.(\d+)\.output\}\}`)

// ValidateSteps statically checks a workflow's step list before it runs:
// kinds are valid, each spec carries the field its kind needs, and every
// {{steps.N.output}} reference points at an EARLIER step. Returns a list
// of human-readable problems — empty means the assembly is well-formed.
//
// This is the cheap "verify the assembly before you commit it" check —
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
					"step %d references {{steps.%d.output}} — a step can only reference an EARLIER step's output", i, n))
			}
		}
	}
	return problems
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
