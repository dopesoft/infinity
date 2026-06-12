package main

import (
	"context"
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/skills"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/dopesoft/infinity/core/internal/workflow"
)

type stubTool struct {
	name string
	out  string
	err  error
}

func (s stubTool) Name() string        { return s.name }
func (s stubTool) Description() string { return "stub" }
func (s stubTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (s stubTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	return s.out, s.err
}

func TestWorkflowRunToolRejectsRecipeOnlySkillInvokeOutput(t *testing.T) {
	reg := tools.NewRegistry()
	recipe := skills.FormatLLMPrompt(&skills.Skill{Name: "nightly-self-improve", Version: "1.0", Body: "Do the thing"}, nil)
	reg.Register(stubTool{name: "skills_invoke", out: "[SKILL_RECIPE_DELIVERY]\n" + recipe})

	exec := &workflowExecutor{registry: reg}
	step := workflow.Step{
		Kind: workflow.KindTool,
		Spec: map[string]any{
			"tool": "skills_invoke",
			"args": map[string]any{"name": "nightly-self-improve"},
		},
	}

	out, err := exec.runTool(context.Background(), step)
	if err == nil {
		t.Fatal("expected recipe-only skills_invoke output to raise an error")
	}
	if out != "" {
		t.Fatalf("expected empty output on rejected recipe delivery, got %q", out)
	}
	if !strings.Contains(err.Error(), "returned a recipe") {
		t.Fatalf("expected recipe-delivery guidance in error, got %v", err)
	}
}
