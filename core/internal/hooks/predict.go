package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/memory"
)

// PredictionRecorder is a hook-side helper that binds the prediction store
// to the agent's PreToolUse / PostToolUse events. Pattern: JEPA-style
// predict-then-act, without a generative world model - we capture a
// heuristic prediction at PreToolUse and resolve it on PostToolUse so the
// delta becomes a curriculum signal (high surprise → Voyager curriculum).
//
// The recorder runs entirely async on the pipeline's goroutine. It never
// blocks the agent loop. Failures are logged + dropped.
type PredictionRecorder struct {
	store   *memory.PredictionStore
	drafter PredictionDrafter
	model   string
}

type PredictionDrafter interface {
	Draft(ctx context.Context, model, system, userPrompt string, maxTokens int64) (string, error)
}

func NewPredictionRecorderWithDrafter(store *memory.PredictionStore, drafter PredictionDrafter, model string) *PredictionRecorder {
	return &PredictionRecorder{store: store, drafter: drafter, model: model}
}

// Register wires both ends. Call from RegisterDefaults or serve.go after the
// pipeline + store are built.
func (p *PredictionRecorder) Register(pipe *Pipeline) {
	if p == nil || p.store == nil || pipe == nil {
		return
	}
	pipe.RegisterFunc("predict.record", p.handlePre, PreToolUse)
	pipe.RegisterFunc("predict.resolve", p.handlePost, PostToolUse, PostToolUseFailure)
}

// handlePre extracts the tool call id + name from the PreToolUse payload,
// builds a heuristic prediction sentence, and records it. The prediction is
// intentionally generic - the value here is the post-hoc surprise score, not
// the prediction text itself. (When a model wants more fidelity, the agent
// loop can override Predict with a real Haiku call.)
func (p *PredictionRecorder) handlePre(ctx context.Context, ev Event) error {
	if p.store == nil {
		return nil
	}
	toolName, callID, input := extractToolMeta(ev.Payload)
	if toolName == "" || callID == "" {
		return nil
	}
	expected := heuristicPrediction(toolName, input)
	if p.drafter != nil && shouldDraftPrediction(toolName) {
		if drafted, err := p.draftPrediction(ctx, toolName, input); err == nil && strings.TrimSpace(drafted) != "" {
			expected = strings.TrimSpace(drafted)
		}
	}
	if expected == "" {
		return nil
	}
	turnID, _ := ev.Payload["turn_id"].(string)
	if _, err := p.store.RecordWithTurn(ctx, ev.SessionID, turnID, callID, toolName, expected, input); err != nil {
		return fmt.Errorf("predict.record %s: %w", toolName, err)
	}
	return nil
}

// handlePost resolves the prediction with the actual output, computes surprise
// via memory.SurpriseFor, and persists. Failure hooks also land here - when
// PostToolUseFailure fires, the actual is treated as an error string.
func (p *PredictionRecorder) handlePost(ctx context.Context, ev Event) error {
	if p.store == nil {
		return nil
	}
	toolName, callID, _ := extractToolMeta(ev.Payload)
	if callID == "" {
		// Try to recover the call id from event text - when the loop emits
		// the post hook with just the tool name + result, fall back to a
		// composite key. This is a best-effort path.
		return nil
	}
	actual, _ := ev.Payload["output"].(string)
	if actual == "" {
		actual = ev.Text
	}
	// When the failure hook fires, guarantee the actual classifies as an error
	// even if the loop didn't prefix it - so SurpriseFor sees an error outcome.
	if ev.Name == PostToolUseFailure {
		la := strings.ToLower(strings.TrimSpace(actual))
		if !strings.HasPrefix(la, "error:") && !strings.HasPrefix(la, "blocked:") {
			actual = "error: " + actual
		}
	}
	// Pull the expected prediction we wrote at PreToolUse so surprise is scored
	// against what we actually predicted (success vs error vs empty), not the
	// raw tool name + input. One indexed lookup; no LLM call. SurpriseFor then
	// compares outcome classes, so a successful JSON result scores low surprise
	// and a genuine error scores high.
	expected, _ := p.store.ExpectedFor(ctx, callID)
	if expected == "" {
		// No stored prediction - fall back to a success-flavored expectation so
		// the outcome class still drives the score.
		expected = "expect " + toolName + " to return a usable result"
	}
	matched, surprise := memory.SurpriseFor(expected, actual)
	if err := p.store.Resolve(ctx, callID, actual, matched, surprise); err != nil {
		return fmt.Errorf("predict.resolve %s: %w", toolName, err)
	}
	return nil
}

// extractToolMeta pulls (name, call_id, input_map) from a PreToolUse /
// PostToolUse payload. The agent.Loop emits these with `name`, `input`,
// and includes `tool_call_id` when present.
func extractToolMeta(payload map[string]any) (string, string, map[string]any) {
	if payload == nil {
		return "", "", nil
	}
	name, _ := payload["name"].(string)
	callID, _ := payload["tool_call_id"].(string)
	if callID == "" {
		callID, _ = payload["id"].(string)
	}
	input, _ := payload["input"].(map[string]any)
	return name, callID, input
}

// heuristicPrediction returns a one-sentence expectation for a tool call.
// Kept deliberately small and rule-based so it costs zero LLM tokens. The
// score is what matters - even a generic "expect success" prediction will
// score low surprise on success and high on failure, which is the signal
// the curriculum cares about.
func heuristicPrediction(toolName string, input map[string]any) string {
	short := jsonShort(input)
	lower := strings.ToLower(toolName)
	switch {
	case strings.HasPrefix(lower, "memory_") || strings.Contains(lower, "search"):
		return fmt.Sprintf("expect %s to return matching rows; non-empty result", toolName)
	case strings.Contains(lower, "write") || strings.Contains(lower, "edit"):
		return fmt.Sprintf("expect %s to succeed writing %s", toolName, short)
	case strings.Contains(lower, "bash") || strings.Contains(lower, "shell") || strings.Contains(lower, "exec"):
		return fmt.Sprintf("expect %s to exit 0 with relevant output for %s", toolName, short)
	case strings.HasPrefix(lower, "claude_code__"):
		return fmt.Sprintf("expect claude_code to perform %s successfully on %s", toolName, short)
	case strings.Contains(lower, "fetch") || strings.Contains(lower, "http"):
		return fmt.Sprintf("expect %s to return 2xx response with non-empty body", toolName)
	default:
		return fmt.Sprintf("expect %s to return a usable result for %s", toolName, short)
	}
}

func shouldDraftPrediction(toolName string) bool {
	lower := strings.ToLower(toolName)
	return strings.HasPrefix(lower, "bridge_") ||
		strings.HasPrefix(lower, "claude_code__") ||
		strings.HasPrefix(lower, "github__") ||
		strings.Contains(lower, "workflow") ||
		strings.Contains(lower, "skills_invoke") ||
		strings.Contains(lower, "write") ||
		strings.Contains(lower, "edit") ||
		strings.Contains(lower, "bash") ||
		strings.Contains(lower, "exec")
}

const predictionSystem = `You predict the likely result of one high-risk tool call.

Return one short sentence only. State the concrete expected success condition.
Do not include markdown or caveats.`

func (p *PredictionRecorder) draftPrediction(ctx context.Context, toolName string, input map[string]any) (string, error) {
	// p.model is usually "" → the active-model drafter resolves the boss's
	// set model. An explicit INFINITY_PREDICTION_MODEL still passes through.
	prompt := fmt.Sprintf("Tool: %s\nInput: %s\nExpected result:", toolName, jsonShort(input))
	return p.drafter.Draft(ctx, p.model, predictionSystem, prompt, 120)
}

func jsonShort(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	s := string(b)
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}
