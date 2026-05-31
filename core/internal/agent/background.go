package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/runs"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/google/uuid"
)

// BackgroundAgent is the model-callable "kick off a real build and walk
// away" tool. It exists to keep expensive, metered surfaces (the GPT
// Realtime voice session) OUT of long engineering work: instead of the
// realtime model orchestrating a build turn-by-turn (every tool result
// fed back through metered realtime tokens), it calls background_build
// once, gets an immediate "started" ack, and the actual work runs on the
// boss's SETTINGS model (gpt-5.4 over the OAuth subscription) in a
// detached goroutine. When it finishes, OnDone fires - the server turns
// that into a live chat bubble + a push notification, so the boss can
// literally hang up the voice call and get pinged when it's done.
//
// Unlike delegate (synchronous, returns the summary inline, capped at a
// few minutes because the caller blocks), background_build:
//
//   - returns immediately with a run_id; the caller never waits
//   - runs on context.Background() so it survives the triggering voice /
//     chat session ending
//   - books a mem_runs row (status running → ok/error) so Studio shows a
//     persistent spinner + the final summary, surviving navigation /
//     refresh / second device
//   - notifies on completion via OnDone (chat + push)
//
// It runs the FULL default tool loadout by default (read/write/exec) so
// it can actually build - the read-only delegate default would be useless
// for "build me X". Pass allowed_tools to narrow it.
type BackgroundAgent struct {
	Loop *Loop
	// OnDone is invoked once, after the background run finishes (success
	// or failure). Wired in serve.go to broadcast a chat bubble + send a
	// push. nil-safe: when unset, the run still completes + persists to
	// mem_runs, it just doesn't actively notify.
	OnDone func(ctx context.Context, r BackgroundResult)
	// OnProgress receives best-effort stage updates for the live parent
	// chat session while the detached run is still executing. Wired in
	// serve.go to broadcast proactive chat bubbles that share the run id,
	// so Studio can render an updating progress card tied to mem_runs.
	OnProgress func(ctx context.Context, p BackgroundProgress)
}

// BackgroundResult is the completion payload handed to OnDone.
type BackgroundResult struct {
	RunID         string
	ParentSession string
	Task          string
	Summary       string
	Err           string
	DurationMS    int64
}

// BackgroundProgress is a best-effort live status update emitted while a
// background build is still running. Stage progress is coarse by design:
// the loop does not expose an exact task DAG, so we surface the current
// tool/stage label plus optional 0..1 progress when available.
type BackgroundProgress struct {
	RunID         string
	ParentSession string
	Task          string
	Label         string
	Status        string
	Progress      *float32
}

const (
	backgroundDefaultTimeout  = 30 * time.Minute
	backgroundTimeoutCeiling  = 60 * time.Minute
	backgroundSessionIDPrefix = "background:"
)

func (b *BackgroundAgent) Name() string { return "background_build" }

func (b *BackgroundAgent) Description() string {
	return "Run a heavy, multi-step engineering task (writing or editing code, building a feature, " +
		"refactoring, a build/test cycle, large multi-file work) autonomously IN THE BACKGROUND on the " +
		"boss's main settings model. Returns immediately with a run_id - it does NOT block. The work runs " +
		"to completion detached from this conversation and the boss is notified (chat + push) when done, so " +
		"he can walk away or hang up a voice call. Use this for anything that would otherwise take many tool " +
		"turns. Do NOT use it for quick one-shot actions (a single file read, a calendar lookup, sending one " +
		"message) - just do those inline."
}

func (b *BackgroundAgent) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type": "string",
				"description": "Complete, self-contained description of what to build. The background agent does NOT " +
					"see this conversation - include every detail it needs: what to build, where (repo / path / project), " +
					"constraints, and what 'done' looks like.",
			},
			"context_brief": map[string]any{
				"type":        "string",
				"description": "Optional extra background: relevant file paths, identifiers, prior decisions, or links the agent should know.",
			},
			"allowed_tools": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional exact tool names to restrict the background agent to. Leave empty for the full default loadout (read/write/exec) - usually correct for a build.",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Wall-clock ceiling for the whole background run (default 1800, max 3600).",
				"default":     1800,
			},
		},
		"required": []string{"task"},
	}
}

func (b *BackgroundAgent) Execute(ctx context.Context, input map[string]any) (string, error) {
	if b.Loop == nil {
		return "", errors.New("background_build: no loop wired")
	}
	task, _ := input["task"].(string)
	task = strings.TrimSpace(task)
	if task == "" {
		return `{"error":"task is required and must be non-empty"}`, nil
	}
	brief, _ := input["context_brief"].(string)

	allowedRaw, _ := input["allowed_tools"].([]any)
	allowed := make([]string, 0, len(allowedRaw))
	for _, v := range allowedRaw {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			allowed = append(allowed, strings.TrimSpace(s))
		}
	}

	timeout := backgroundDefaultTimeout
	if v, ok := input["timeout_seconds"].(float64); ok && v > 0 {
		timeout = time.Duration(v) * time.Second
	}
	if timeout > backgroundTimeoutCeiling {
		timeout = backgroundTimeoutCeiling
	}

	// Capture the parent (calling) session NOW, while the request context
	// still carries it. The detached goroutine runs on context.Background()
	// and would otherwise lose it.
	parentSession := tools.SessionIDFromContext(ctx)

	runID := uuid.NewString()
	label := backgroundLabel(task)

	go b.runDetached(parentSession, runID, label, task, brief, allowed, timeout)

	resp := map[string]any{
		"status":  "started",
		"run_id":  runID,
		"message": "Build started in the background on the main model. It'll keep running even if this session ends - the boss gets a chat message and a push when it's done.",
	}
	out, _ := json.Marshal(resp)
	return string(out), nil
}

// runDetached owns the whole background lifecycle on a fresh, detached
// context so it survives the triggering session closing.
func (b *BackgroundAgent) runDetached(parentSession, runID, label, task, brief string, allowed []string, timeout time.Duration) {
	// Cap the absolute lifetime so a wedged build can't leak a goroutine
	// forever. context.Background() (not the request ctx) is the point:
	// hanging up the voice call must NOT cancel the build.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	var summary string

	// mem_runs row: status=running now, ok/error + summary on finish.
	// Studio's useRuns() shows this live and it survives refresh / device
	// switch. The summary is stored on the row so the result is durable
	// even if the boss never sees the live chat bubble.
	runErr := runs.Track(ctx, runs.KindBackgroundBuild, runID, label, runs.SourceAgent, func(ctx context.Context) error {
		if b.OnProgress != nil {
			phase := float32(0.05)
			b.OnProgress(context.Background(), BackgroundProgress{
				RunID:         runID,
				ParentSession: parentSession,
				Task:          task,
				Label:         "queued background worker",
				Status:        "running",
				Progress:      &phase,
			})
		}
		s, e := b.runToCompletion(ctx, parentSession, runID, task, brief, allowed)
		summary = s
		return e
	})

	res := BackgroundResult{
		RunID:         runID,
		ParentSession: parentSession,
		Task:          task,
		Summary:       summary,
		DurationMS:    time.Since(start).Milliseconds(),
	}
	if runErr != nil {
		res.Err = runErr.Error()
	}
	if b.OnDone != nil {
		// Detached notify ctx - the run ctx may already be cancelled by
		// the timeout/defer above by the time we get here.
		b.OnDone(context.Background(), res)
	}
}

// runToCompletion spins an ephemeral session, runs the agent loop on the
// DEFAULT (settings) model to completion, and returns the final text.
// Mirrors delegate.run, minus the synchronous-return contract.
func (b *BackgroundAgent) runToCompletion(ctx context.Context, parentSession, runID, task, brief string, allowed []string) (string, error) {
	childID := backgroundSessionIDPrefix + uuid.NewString()
	child := b.Loop.GetOrCreateSession(childID)
	defer b.Loop.ClearSession(childID)

	// Full default loadout unless the caller narrowed it - a build needs
	// write/exec, so we do NOT fall back to the read-only delegate set.
	if len(allowed) > 0 {
		child.Active.Replace(allowed)
	}

	base := backgroundBasePrompt
	if trimmed := strings.TrimSpace(brief); trimmed != "" {
		base = base + "\n\n## Brief\n" + trimmed
	}
	child.SystemPromptOverride = base

	events := make(chan RunEvent, 256)
	var (
		finalText     strings.Builder
		runErr        error
		wg            sync.WaitGroup
		toolCallCount int
		lastProgress  string
	)
	progressForToolCalls := func(count int) float32 {
		if count <= 0 {
			return 0.15
		}
		// Stage-based, asymptotic progress: initial setup = 0.05, each tool
		// call advances the bar, but leave room for wrap-up and final summary.
		progress := float32(0.15 + 0.1*float32(count))
		if progress > 0.9 {
			progress = 0.9
		}
		return progress
	}
	publishProgress := func(label string, progress *float32) {
		trimmed := strings.TrimSpace(label)
		if trimmed == "" || b.OnProgress == nil {
			return
		}
		if trimmed == lastProgress {
			return
		}
		lastProgress = trimmed
		b.OnProgress(context.Background(), BackgroundProgress{
			RunID:         runID,
			ParentSession: parentSession,
			Task:          task,
			Label:         trimmed,
			Status:        "running",
			Progress:      progress,
		})
	}
	publishProgress("starting background build", func() *float32 { v := float32(0.1); return &v }())
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ev := range events {
			switch ev.Kind {
			case EventDelta:
				finalText.WriteString(ev.TextDelta)
				if strings.TrimSpace(ev.TextDelta) != "" {
					phase := float32(0.95)
					publishProgress("writing final summary", &phase)
				}
			case EventToolCall:
				if ev.ToolCall != nil {
					toolCallCount++
					phase := progressForToolCalls(toolCallCount)
					publishProgress(backgroundProgressLabel(ev.ToolCall.Name, toolCallCount), &phase)
				}
			case EventThinking:
				if text := strings.TrimSpace(ev.ThinkingDelta); text != "" {
					phase := progressForToolCalls(toolCallCount)
					publishProgress(text, &phase)
				}
			case EventError:
				runErr = errors.New(ev.Error)
			}
		}
	}()

	// Empty model string → the loop's default provider = settings model
	// (gpt-5.4 over the OAuth subscription). The realtime API is never
	// touched here.
	err := b.Loop.Run(ctx, childID, task, "", nil, events)
	close(events)
	wg.Wait()

	if err != nil && runErr == nil {
		runErr = err
	}
	summary := strings.TrimSpace(finalText.String())
	if summary == "" && runErr == nil {
		summary = "Background build completed (no summary text produced)."
	}
	return summary, runErr
}

// backgroundLabel produces the short human string Studio shows next to the
// run spinner. First line of the task, clipped.
func backgroundLabel(task string) string {
	line := task
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	const max = 80
	if len(line) > max {
		line = line[:max] + "…"
	}
	if line == "" {
		return "background build"
	}
	return line
}

func backgroundProgressLabel(toolName string, toolCallCount int) string {
	name := strings.TrimSpace(toolName)
	if name == "" {
		if toolCallCount <= 0 {
			return "working"
		}
		return fmt.Sprintf("running step %d", toolCallCount)
	}
	name = strings.TrimPrefix(name, "functions.")
	name = strings.TrimPrefix(name, "claude_code__")
	name = strings.TrimPrefix(name, "composio__")
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		if toolCallCount <= 0 {
			return "working"
		}
		return fmt.Sprintf("running step %d", toolCallCount)
	}
	if toolCallCount <= 0 {
		return name
	}
	return fmt.Sprintf("step %d: %s", toolCallCount, name)
}

const backgroundBasePrompt = `You are the full autonomous coding agent, running in the BACKGROUND on a detached session. The boss kicked off this task by voice or chat and has moved on - he is NOT watching. You have the complete tool loadout (read, write, edit, exec, memory, bridges).

Your job:
1. Complete the task end to end. Actually write the code / make the changes / run the builds - do not just plan.
2. Use the same care and conventions you would in a live session. Read before you write; verify your work.
3. When finished, your FINAL message must be a concise plain-language summary of what you did: what you built or changed, which files, and anything the boss must know (decisions, follow-ups, anything that needs his approval). This summary is what gets sent to his phone - make it scannable, not a wall of text.

There is no one to ask mid-task, so make sensible decisions and note them in the summary rather than stopping. If a step genuinely cannot proceed (it needs a Trust-gated approval, a missing credential, or a decision only the boss can make), do as much as you safely can, then clearly state what is blocked and why in the summary.`
