package memory

import (
	"context"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// llmSummarizerAdapter bridges llm.AnthropicSummarizer to the memory.Summarizer
// interface so the memory package doesn't import llm types directly.
type llmSummarizerAdapter struct {
	inner interface {
		Summarize(ctx context.Context, hookName, rawText string) (llm.CompressedFacts, error)
	}
}

// NewSummarizer wraps an llm.AnthropicSummarizer (or anything with the same
// Summarize signature) so the compressor can call it.
func NewSummarizer(s interface {
	Summarize(ctx context.Context, hookName, rawText string) (llm.CompressedFacts, error)
}) Summarizer {
	return &llmSummarizerAdapter{inner: s}
}

func (a *llmSummarizerAdapter) SummarizeObservation(ctx context.Context, hookName, rawText string) (CompressedFacts, error) {
	facts, err := a.inner.Summarize(ctx, hookName, rawText)
	if err != nil {
		return CompressedFacts{}, err
	}
	out := CompressedFacts{
		Type:     facts.Type,
		Title:    facts.Title,
		Summary:  facts.Summary,
		Concepts: facts.Concepts,
		Files:    facts.Files,
	}
	for _, e := range facts.Entities {
		out.Entities = append(out.Entities, Entity{Type: e.Type, Name: e.Name})
	}
	for _, r := range facts.Relations {
		out.Relations = append(out.Relations, RelationFact{
			FromType: r.FromType,
			FromName: r.FromName,
			ToType:   r.ToType,
			ToName:   r.ToName,
			Type:     r.Type,
		})
	}
	return out, nil
}
