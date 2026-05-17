package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/runs"
)

// Runner executes skills. It dispatches by risk_level to a sandbox tier:
//
//   - low / medium → in-process jail (rlimits, env filter)
//   - high          → container (TODO: container runtime not yet wired)
//   - critical      → container + Trust Contract gate (gated by Trust Contract)
//
// We ship the in-process tier and hard-fail on high/critical until
// the Docker client lands. The frontend surfaces this clearly.
type Runner struct {
	registry *Registry
	store    *Store
}

func NewRunner(reg *Registry, store *Store) *Runner {
	return &Runner{registry: reg, store: store}
}

// Invoke executes a skill by name with the given args. The trigger source is
// recorded on the run row so the Skills tab can show "manual / conversation /
// cron / heartbeat / sentinel" filters.
func (r *Runner) Invoke(ctx context.Context, sessionID, name string, args map[string]any, triggerSource string) (Result, *Run, error) {
	skill, ok := r.registry.Get(name)
	if !ok {
		return Result{}, nil, fmt.Errorf("unknown skill: %s", name)
	}
	if skill.Status != StatusActive {
		return Result{}, nil, fmt.Errorf("skill %s is %s; only active skills can be invoked", name, skill.Status)
	}

	if err := validateInputs(skill, args); err != nil {
		return Result{}, nil, err
	}

	// runs.Track surfaces "this skill is invoking" to every Studio tab
	// + device via mem_runs realtime. Wraps the dispatch below so both
	// in-process and container tiers (when wired) get the same visible
	// spinner. See CLAUDE.md → "Server-tracked progress".
	source := mapTriggerSource(triggerSource)
	var (
		res    Result
		runRow *Run
		invErr error
	)
	_ = runs.Track(ctx, runs.KindSkill, skill.Name, skill.Name, source, func(ctx context.Context) error {
		switch skill.RiskLevel {
		case RiskLow, RiskMedium:
			res, runRow, invErr = r.runInProcess(ctx, skill, args, sessionID, triggerSource)
		case RiskHigh, RiskCritical:
			res = Result{Stderr: "container sandbox not yet wired"}
			invErr = errors.New("high/critical-risk skills require the container sandbox (container sandbox)")
		default:
			invErr = fmt.Errorf("invalid risk_level: %s", skill.RiskLevel)
		}
		return invErr
	})
	return res, runRow, invErr
}

// mapTriggerSource bridges the Skills triggerSource vocabulary to the
// runs.Source vocabulary. They overlap but the runs side is intentionally
// smaller (manual / agent / scheduled / heartbeat / sentinel).
func mapTriggerSource(s string) runs.Source {
	switch s {
	case "conversation", "agent":
		return runs.SourceAgent
	case "cron", "scheduled":
		return runs.SourceScheduled
	case "heartbeat":
		return runs.SourceHeartbeat
	case "sentinel":
		return runs.SourceSentinel
	default:
		return runs.SourceManual
	}
}

func (r *Runner) runInProcess(ctx context.Context, skill *Skill, args map[string]any, sessionID, triggerSource string) (Result, *Run, error) {
	cmd, err := buildCommand(skill, args)
	if err != nil {
		return Result{}, nil, err
	}

	opts := SandboxOpts{
		Timeout:    timeoutFor(skill),
		MemoryMB:   512,
		CPULimit:   1.0,
		AllowedEnv: defaultAllowedEnv(skill),
		WorkDir:    skill.Path,
	}
	if len(skill.NetworkEgress) > 0 {
		opts.NetworkAllow = skill.NetworkEgress
	}

	res, runErr := RunInProcessJail(ctx, cmd, opts)

	endedAt := time.Now().UTC()
	run := &Run{
		SkillName:     skill.Name,
		Version:       skill.Version,
		SessionID:     sessionID,
		TriggerSource: triggerSource,
		Input:         args,
		Output:        res.Stdout,
		Success:       res.Success && runErr == nil,
		DurationMS:    res.DurationMS,
		StartedAt:     endedAt.Add(-time.Duration(res.DurationMS) * time.Millisecond),
		EndedAt:       &endedAt,
	}

	if r.store != nil {
		if id, err := r.store.RecordRun(ctx, run); err == nil {
			run.ID = id
		}
	}
	return res, run, runErr
}

// validateInputs checks required-input presence. Type coercion is best-effort;
// skills can re-validate inside their implementation.
func validateInputs(skill *Skill, args map[string]any) error {
	for _, in := range skill.Inputs {
		if !in.Required {
			continue
		}
		if _, ok := args[in.Name]; !ok {
			return fmt.Errorf("missing required input %q", in.Name)
		}
	}
	return nil
}

// buildCommand resolves how to execute the skill: implementation file if
// present, otherwise the body Markdown is treated as a prompt for an LLM
// sub-call (handled by registry_tools.go via the parent agent - when there is
// no implementation file, runInProcess will return an error and registry_tools
// surfaces the body to the model).
func buildCommand(skill *Skill, args map[string]any) ([]string, error) {
	if skill.ImplPath == "" {
		return nil, errors.New("skill has no executable implementation; LLM-only skills must be invoked via the body prompt path")
	}
	switch skill.ImplLanguage {
	case "python":
		return []string{"python3", skill.ImplPath, encodeArgs(args)}, nil
	case "bash":
		return []string{"bash", skill.ImplPath, encodeArgs(args)}, nil
	case "javascript", "typescript":
		return []string{"node", skill.ImplPath, encodeArgs(args)}, nil
	}
	if path, err := exec.LookPath(skill.ImplPath); err == nil {
		return []string{path, encodeArgs(args)}, nil
	}
	return nil, fmt.Errorf("unsupported implementation language: %s", skill.ImplLanguage)
}

func encodeArgs(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(args)
	return string(b)
}

func timeoutFor(skill *Skill) time.Duration {
	switch skill.RiskLevel {
	case RiskCritical:
		return 5 * time.Minute
	case RiskHigh:
		return 2 * time.Minute
	case RiskMedium:
		return 60 * time.Second
	}
	return 30 * time.Second
}

// defaultAllowedEnv returns the env-var names a skill is permitted to inherit.
// Skills declare extras via documentation; the v1 default exposes nothing -
// skills are expected to take inputs via argv or stdin.
func defaultAllowedEnv(skill *Skill) []string {
	out := []string{"PATH", "HOME", "TMPDIR"}
	_ = skill
	return out
}

// FormatLLMPrompt returns the body markdown wrapped as a system-prompt block
// suitable for "LLM-only" skills (no implementation file). The agent loop
// uses this when registry_tools.skills.invoke is called against a skill that
// has no implementation: it composes a sub-call to the LLM with the body as
// the instruction.
func FormatLLMPrompt(skill *Skill, args map[string]any) string {
	var b strings.Builder
	b.WriteString("# Skill: ")
	b.WriteString(skill.Name)
	b.WriteString(" (v")
	b.WriteString(skill.Version)
	b.WriteString(")\n\n")
	if skill.Description != "" {
		b.WriteString(skill.Description)
		b.WriteString("\n\n")
	}
	b.WriteString("## Instructions\n")
	b.WriteString(strings.TrimSpace(skill.Body))
	b.WriteString("\n\n## Inputs\n")
	for k, v := range args {
		fmt.Fprintf(&b, "- %s: %v\n", k, v)
	}
	return b.String()
}
