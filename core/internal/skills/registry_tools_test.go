package skills

import (
	"context"
	"strings"
	"testing"
)

// TestInvokeToolLLMOnlySkillReturnsSentinel verifies that skills_invoke on a
// recipe-only (no executable) skill:
//   - returns output prefixed with recipeDeliverySentinel so callers can
//     detect it is a recipe delivery, not execution output
//   - still includes the imperative "EXECUTE THIS NOW" framing so the LLM
//     knows it must carry out the recipe
func TestInvokeToolLLMOnlySkillReturnsSentinel(t *testing.T) {
	reg := NewRegistry("")
	reg.mu.Lock()
	reg.skills["test-recipe"] = &Skill{
		Name:    "test-recipe",
		Version: "1.0.0",
		Body:    "## Step 1\nDo the thing.",
		Status:  StatusActive,
	}
	reg.mu.Unlock()

	tool := &invokeTool{r: reg}
	out, err := tool.Execute(context.Background(), map[string]any{"name": "test-recipe"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !IsRecipeOutput(out) {
		t.Errorf("expected recipe sentinel prefix %q, got: %.120q", recipeDeliverySentinel, out)
	}
	if !strings.Contains(out, "EXECUTE THIS NOW") {
		t.Errorf("imperative recipe framing missing from output; LLM would not know to execute")
	}
}

// TestIsRecipeOutput verifies the detector against representative strings.
func TestIsRecipeOutput(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"", false},
		{"normal execution output", false},
		{"EXECUTE THIS NOW", false},         // framing phrase alone, no sentinel
		{"skills_invoke ran nothing", false}, // phrase without sentinel
		{recipeDeliverySentinel, true},
		{recipeDeliverySentinel + "\n# Skill: foo (v1.0)", true},
		{"  " + recipeDeliverySentinel + "\nleading whitespace ok", true},
	}
	for _, tt := range tests {
		if got := IsRecipeOutput(tt.s); got != tt.want {
			t.Errorf("IsRecipeOutput(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
}
