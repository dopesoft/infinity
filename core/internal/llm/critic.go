package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// ReflectionResult mirrors memory.ReflectionResult - kept in the llm package
// so the memory package doesn't import this one (matches the
// CompressedFacts/Summarizer pattern).
type ReflectionResult struct {
	Critique     string          `json:"critique"`
	QualityScore float64         `json:"quality_score"`
	Lessons      []CriticLesson  `json:"lessons"`
	Kind         string          `json:"kind"`
}

type CriticLesson struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

// AnthropicCritic implements the metacognition step: it reads a session
// transcript and emits a structured critique + lessons. Pattern: Multi-Agent
// Reflexion (MAR, arXiv 2512.20845) - separate persona, fresh model call so
// the actor doesn't get to grade its own homework. We use Haiku to keep this
// cheap; quality is high enough for the extraction shape.
type AnthropicCritic struct {
	a     *Anthropic
	model string
}

func NewAnthropicCritic(a *Anthropic, model string) *AnthropicCritic {
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}
	return &AnthropicCritic{a: a, model: model}
}

const critiqueSystem = `You are an honest, terse critic reviewing one of your own past agent sessions.

You will receive a transcript of a session - user prompts, your replies, the tools you called, errors, etc. Your job: judge how well the session went and extract durable lessons.

Be strict. If the session was sloppy, say so. If it was excellent, say so. Don't pad.

Return ONLY a JSON object in this exact shape - no commentary, no code fences:

{
  "kind": "session_critique" | "error_postmortem" | "self_consistency",
  "critique": "2-4 sentences. What went well, what went badly, what should have happened differently. Concrete, not generic.",
  "quality_score": 0.0,
  "lessons": [
    { "text": "imperative sentence, ≤140 chars", "confidence": 0.0 }
  ]
}

Rules:
- quality_score is 0..1 (0 = disaster, 1 = exemplary).
- Lessons are sentences the FUTURE you should follow: "When the user asks X, first do Y". Not summaries of what happened.
- Lesson confidence is 0..1; reserve >0.7 for lessons backed by clear evidence in the transcript.
- 0-5 lessons. Quality over quantity. If there's nothing durable, return an empty array.
- If the transcript is too short or boilerplate to judge, return critique="" and lessons=[]. Don't fabricate.`

func (c *AnthropicCritic) CritiqueSession(ctx context.Context, transcript string) (ReflectionResult, error) {
	if c == nil || c.a == nil {
		return ReflectionResult{}, errors.New("critic not configured")
	}
	prompt := fmt.Sprintf("Session transcript:\n\n%s", truncate(transcript, 12000))

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: 1200,
		System:    []anthropic.TextBlockParam{{Text: critiqueSystem}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(prompt))},
	}

	msg, err := c.a.client.Messages.New(ctx, params)
	if err != nil {
		return ReflectionResult{}, err
	}

	raw := strings.TrimSpace(collectText(msg))
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ReflectionResult{}, nil
	}

	var out ReflectionResult
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return ReflectionResult{}, fmt.Errorf("parse critique: %w", err)
	}
	if out.Kind == "" {
		out.Kind = "session_critique"
	}
	return out, nil
}
