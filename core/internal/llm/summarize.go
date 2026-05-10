package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// CompressedFacts mirrors memory.CompressedFacts but lives here so the llm
// package has no dependency on memory. The Anthropic Summarizer adapter
// converts between the two.
type CompressedFacts struct {
	Type     string         `json:"type"`
	Title    string         `json:"title"`
	Summary  string         `json:"summary"`
	Concepts []string       `json:"concepts"`
	Entities []FactEntity   `json:"entities"`
	Files    []string       `json:"files"`
}

type FactEntity struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// AnthropicSummarizer uses Claude Haiku for cheap, fast structured extraction.
// One Haiku turn per observation. Schema-validated; one retry with stricter
// prompt on validation failure.
type AnthropicSummarizer struct {
	a     *Anthropic
	model string
}

func NewAnthropicSummarizer(a *Anthropic, model string) *AnthropicSummarizer {
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}
	return &AnthropicSummarizer{a: a, model: model}
}

const summarizeSystem = `You are an extractor that converts agent observations into structured memory facts.

Return ONLY valid JSON in this exact shape (no commentary, no code fences):

{
  "type": "decision|fact|error|preference|event",
  "title": "<=80 chars, sentence case",
  "summary": "1-3 sentences capturing what's worth remembering",
  "concepts": ["concept1", "concept2"],
  "entities": [{"type": "person|project|file|concept|decision|error|skill", "name": "..."}],
  "files": ["path/one.go", "path/two.tsx"]
}

If the observation is empty, boilerplate, or low-value, return:
{"type":"event","title":"","summary":""}

Never invent facts. If unsure, leave fields empty.`

func (s *AnthropicSummarizer) Summarize(ctx context.Context, hookName, rawText string) (CompressedFacts, error) {
	prompt := fmt.Sprintf("Hook: %s\n\nObservation:\n%s", hookName, truncate(rawText, 4000))

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(s.model),
		MaxTokens: 1024,
		System:    []anthropic.TextBlockParam{{Text: summarizeSystem}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(prompt))},
	}

	msg, err := s.a.client.Messages.New(ctx, params)
	if err != nil {
		return CompressedFacts{}, err
	}

	raw := collectText(msg)
	facts, err := parseFacts(raw)
	if err == nil {
		return facts, nil
	}

	// Retry once with a stricter prompt
	retry := params
	retry.Messages = append(retry.Messages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(raw)))
	retry.Messages = append(retry.Messages, anthropic.NewUserMessage(
		anthropic.NewTextBlock("That response was not valid JSON. Return ONLY the JSON object, nothing else."),
	))
	msg2, err := s.a.client.Messages.New(ctx, retry)
	if err != nil {
		return CompressedFacts{}, err
	}
	return parseFacts(collectText(msg2))
}

func parseFacts(raw string) (CompressedFacts, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return CompressedFacts{}, errors.New("empty response")
	}
	var out CompressedFacts
	if err := json.Unmarshal([]byte(cleaned), &out); err != nil {
		return CompressedFacts{}, err
	}
	return out, nil
}

func collectText(msg *anthropic.Message) string {
	var b strings.Builder
	for _, c := range msg.Content {
		if t, ok := c.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
