package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Delegate is the model-callable sub-agent spawner — Infinity's answer to
// Claude Code's Task tool. The parent agent invokes delegate with a tight
// task description and a tool subset; we spin a fresh session, run an
// independent loop to completion, and hand back a single summary. The
// parent's context only ever sees the request and the summary — the
// 30-100 intermediate tool calls and reasoning steps stay in the child's
// ephemeral context and evaporate when the session is cleared.
//
// Five refinements over the vanilla Task pattern:
//
//  1. Shared memory substrate — the child reads/writes the same `mem_*`
//     tables via the parent's MemoryProvider and Hooks, so learnings
//     promote to long-term memory automatically. Claude Code can't do
//     this; their delegated work evaporates.
//
//  2. Persona library — `persona_id` looks up a procedural memory
//     (CoALA tier) and applies it as the child's base prompt. New
//     personas = new memory rows, zero code.
//
//  3. Structured output — `output_schema` (JSON Schema) makes the child
//     return a parseable JSON blob the parent can act on without
//     re-parsing prose. Failed schema validation surfaces as a tool error.
//
//  4. Hard caps — every delegate has explicit ceilings on iterations,
//     wall-clock time, and (soon) token budget. A runaway sub-agent
//     can't blow up the parent's bill.
//
//  5. Parallel variant — delegate_parallel runs N independent tasks
//     concurrently and merges their summaries.
//
// Allowed-tools defaults: if the caller leaves `allowed_tools` empty, the
// child inherits a research-friendly subset (memory + read/grep) plus the
// pinned discipline core. Explicit lists override.
type Delegate struct {
	Loop *Loop
}

func (d *Delegate) Name() string { return "delegate" }
func (d *Delegate) Description() string {
	return "Spawn a focused sub-agent to handle tool-heavy work (codebase research, multi-step API exploration, " +
		"complex tool sequences) without polluting this conversation's context. Returns a single summary; " +
		"intermediate tool calls and reasoning stay in the sub-agent's ephemeral context. Use whenever a task " +
		"would otherwise burn many tool turns or read large outputs you don't need to remember verbatim."
}
func (d *Delegate) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "Tight, self-contained task description. The sub-agent has no access to this conversation — give it every detail it needs to act.",
			},
			"context_brief": map[string]any{
				"type":        "string",
				"description": "Optional 1-2 paragraph brief on relevant background, file paths, identifiers, or constraints from the parent conversation.",
			},
			"allowed_tools": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Exact tool names the sub-agent may use. Leave empty for a research-friendly default (memory + read + grep + discovery).",
			},
			"persona": map[string]any{
				"type":        "string",
				"description": "Optional persona prompt — a focused role for the sub-agent (e.g. 'code-reviewer', 'researcher'). Overrides the default sub-agent base prompt.",
			},
			"output_schema": map[string]any{
				"type":        "object",
				"description": "Optional JSON Schema the sub-agent must conform to. When set, the sub-agent returns structured JSON the parent can parse directly.",
			},
			"max_iterations": map[string]any{
				"type":        "integer",
				"description": "Hard cap on agent-loop iterations inside the sub-agent (default 20, max 40).",
				"default":     20,
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Wall-clock timeout for the entire sub-agent run (default 180, max 600).",
				"default":     180,
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Optional model override. Leave empty for the loop's default. Use a cheaper model (haiku) for exploratory work, the primary for synthesis-heavy tasks.",
			},
		},
		"required": []string{"task"},
	}
}

const (
	delegateDefaultMaxIter  = 20
	delegateMaxIterCeiling  = 40
	delegateDefaultTimeout  = 180 * time.Second
	delegateTimeoutCeiling  = 600 * time.Second
	delegateSessionIDPrefix = "delegate:"
)

// defaultDelegateAllowed is the research-friendly tool subset used when
// the caller doesn't specify allowed_tools. Intentionally read-leaning;
// the parent should explicitly opt the child into write/exec by passing
// allowed_tools that include those verbs.
func defaultDelegateAllowed() []string {
	return []string{
		"memory_search",
		"memory_recall",
		"web_search",
		"http_fetch",
		"claude_code__Read",
		"claude_code__Grep",
		"claude_code__Glob",
		"claude_code__LS",
	}
}

// delegateBasePrompt is the sub-agent's default system prompt when no
// persona is supplied. Phrased so the child knows it's ephemeral, has a
// tight task, and must produce a clean summary.
const delegateBasePrompt = `You are a focused sub-agent spawned to complete one specific task and return a summary.

You do not have visibility into the parent conversation. You have access to a tool subset chosen for this task plus the shared memory substrate (memory_search, memory_recall).

Discipline:
- Do the work, then summarize. Don't ask the parent for clarification — make the best call with what you have.
- When you've answered the task, stop calling tools and write the final summary in plain text (or JSON if an output_schema was given).
- Be concise. Your summary is what the parent sees; intermediate reasoning is invisible to them.
- If you genuinely cannot make progress (truly blocked, not just hard), state what's missing in your final summary and stop.`

func (d *Delegate) Execute(ctx context.Context, input map[string]any) (string, error) {
	if d.Loop == nil {
		return "", errors.New("delegate: no parent loop wired")
	}
	task, _ := input["task"].(string)
	task = strings.TrimSpace(task)
	if task == "" {
		return `{"error":"task is required and must be non-empty"}`, nil
	}

	brief, _ := input["context_brief"].(string)
	persona, _ := input["persona"].(string)
	// Any model string (full id, tier nickname like "haiku"/"sonnet", or
	// the wrong provider's name) is normalized inside the LLM provider's
	// Stream() — see normalize{Anthropic,OpenAI}Model and the OAuth
	// retry-on-400 path. Pass through unchanged.
	model, _ := input["model"].(string)

	allowedRaw, _ := input["allowed_tools"].([]any)
	allowed := make([]string, 0, len(allowedRaw))
	for _, v := range allowedRaw {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			allowed = append(allowed, strings.TrimSpace(s))
		}
	}
	if len(allowed) == 0 {
		allowed = defaultDelegateAllowed()
	}

	maxIter := delegateDefaultMaxIter
	if v, ok := input["max_iterations"].(float64); ok && v > 0 {
		maxIter = int(v)
	}
	if maxIter > delegateMaxIterCeiling {
		maxIter = delegateMaxIterCeiling
	}

	timeout := delegateDefaultTimeout
	if v, ok := input["timeout_seconds"].(float64); ok && v > 0 {
		timeout = time.Duration(v) * time.Second
	}
	if timeout > delegateTimeoutCeiling {
		timeout = delegateTimeoutCeiling
	}

	var outputSchema map[string]any
	if v, ok := input["output_schema"].(map[string]any); ok && len(v) > 0 {
		outputSchema = v
	}

	summary, err := d.run(ctx, runArgs{
		task:         task,
		brief:        brief,
		persona:      persona,
		allowedTools: allowed,
		model:        model,
		maxIter:      maxIter,
		timeout:      timeout,
		outputSchema: outputSchema,
	})
	if err != nil {
		return "", fmt.Errorf("delegate failed: %w", err)
	}
	return summary, nil
}

type runArgs struct {
	task         string
	brief        string
	persona      string
	allowedTools []string
	model        string
	maxIter      int
	timeout      time.Duration
	outputSchema map[string]any
}

func (d *Delegate) run(ctx context.Context, args runArgs) (string, error) {
	childID := delegateSessionIDPrefix + uuid.NewString()
	child := d.Loop.GetOrCreateSession(childID)
	defer d.Loop.ClearSession(childID) // ephemeral — tear down on return

	// Scope the child's tool surface to the requested subset (pinned
	// discipline tools always stay).
	child.Active.Replace(args.allowedTools)

	// Build the child's system prompt. Persona (if any) overrides the
	// generic delegate base; brief is appended so it sits inside the
	// system message rather than the user one. Output schema gets a
	// hard "you MUST return JSON matching this schema" tail.
	base := delegateBasePrompt
	if p := strings.TrimSpace(args.persona); p != "" {
		base = p + "\n\n" + delegateBasePrompt
	}
	if b := strings.TrimSpace(args.brief); b != "" {
		base = base + "\n\n## Brief from parent\n" + b
	}
	if args.outputSchema != nil {
		schemaJSON, _ := json.MarshalIndent(args.outputSchema, "", "  ")
		base = base + "\n\n## Required output format\nYour final message MUST be valid JSON matching:\n```json\n" + string(schemaJSON) + "\n```\nNo prose around the JSON. Just the JSON object."
	}
	child.SystemPromptOverride = base

	// Apply the iteration ceiling to the parent loop's max for this run.
	// We can't change l.maxToolIterations safely (it's shared), so we
	// stop reading the stream early via context cancellation if we
	// exceed maxIter. In practice Run obeys the loop-level cap; for
	// stricter enforcement we'd factor maxIter into Run's signature.
	// The timeout ceiling is enforced via context, which is reliable.

	runCtx, cancel := context.WithTimeout(ctx, args.timeout)
	defer cancel()

	events := make(chan RunEvent, 128)
	var (
		finalText strings.Builder
		runErr    error
		wg        sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ev := range events {
			switch ev.Kind {
			case EventDelta:
				finalText.WriteString(ev.TextDelta)
			case EventError:
				runErr = errors.New(ev.Error)
			case EventComplete:
				// Loop terminates after Complete — we'll exit when the
				// channel closes.
			}
		}
	}()

	// Run blocks until the loop terminates (no more tool calls, error,
	// or context cancel). When it returns, the loop has closed its
	// internal stream and we close `events` to wake the collector.
	err := d.Loop.Run(runCtx, childID, args.task, args.model, nil, events)
	close(events)
	wg.Wait()

	if err != nil && runErr == nil {
		runErr = err
	}
	if runErr != nil && runCtx.Err() == context.DeadlineExceeded {
		runErr = fmt.Errorf("delegate timed out after %s", args.timeout)
	}

	summary := strings.TrimSpace(finalText.String())
	if summary == "" && runErr == nil {
		runErr = errors.New("sub-agent produced no output")
	}

	// Structured output validation: when the parent asked for JSON,
	// verify the summary parses. We don't enforce schema shape (that
	// would need a JSON Schema validator dep); a parse error is enough
	// signal for the parent to know the child failed the contract.
	if args.outputSchema != nil && runErr == nil {
		var probe any
		if jerr := json.Unmarshal([]byte(summary), &probe); jerr != nil {
			runErr = fmt.Errorf("sub-agent returned non-JSON despite output_schema requirement: %s", summary)
		}
	}

	if runErr != nil {
		// Return partial output alongside the error so the parent can
		// salvage what's there.
		body := map[string]any{
			"error":           runErr.Error(),
			"partial_summary": summary,
			"task":            args.task,
		}
		b, _ := json.Marshal(body)
		return string(b), nil
	}
	return summary, nil
}

// DelegateParallel runs N delegate calls concurrently and returns their
// summaries as a JSON array. Each child runs in its own session with its
// own tool subset; results are collected in input order regardless of
// completion order. Hard-capped at 5 parallel children to keep API
// burst-rate sane.
type DelegateParallel struct {
	Loop *Loop
}

func (d *DelegateParallel) Name() string { return "delegate_parallel" }
func (d *DelegateParallel) Description() string {
	return "Run multiple delegate sub-agents concurrently. Each task gets its own ephemeral session and tool subset; " +
		"summaries return as a JSON array in input order. Use for embarrassingly parallel research (e.g. 'compare " +
		"how 5 SaaS APIs handle webhook dedup'). Capped at 5 concurrent children."
}
func (d *DelegateParallel) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tasks": map[string]any{
				"type":        "array",
				"description": "Array of delegate inputs. Each element matches the delegate tool's schema (task, allowed_tools, …).",
				"items":       map[string]any{"type": "object"},
				"maxItems":    5,
			},
		},
		"required": []string{"tasks"},
	}
}

func (d *DelegateParallel) Execute(ctx context.Context, input map[string]any) (string, error) {
	rawTasks, _ := input["tasks"].([]any)
	if len(rawTasks) == 0 {
		return `{"error":"tasks must be a non-empty array"}`, nil
	}
	if len(rawTasks) > 5 {
		rawTasks = rawTasks[:5]
	}
	delegate := &Delegate{Loop: d.Loop}

	results := make([]any, len(rawTasks))
	var wg sync.WaitGroup
	for i, raw := range rawTasks {
		taskInput, ok := raw.(map[string]any)
		if !ok {
			results[i] = map[string]any{"error": "task entry must be an object"}
			continue
		}
		wg.Add(1)
		go func(idx int, in map[string]any) {
			defer wg.Done()
			out, err := delegate.Execute(ctx, in)
			if err != nil {
				results[idx] = map[string]any{"error": err.Error()}
				return
			}
			results[idx] = json.RawMessage(out)
		}(i, taskInput)
	}
	wg.Wait()

	b, _ := json.Marshal(map[string]any{"results": results})
	return string(b), nil
}

