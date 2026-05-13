package memory

import (
	"context"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// llmCriticAdapter bridges llm.AnthropicCritic to memory.Critic so the memory
// package doesn't import llm types directly. Mirrors llmSummarizerAdapter.
type llmCriticAdapter struct {
	inner interface {
		CritiqueSession(ctx context.Context, transcript string) (llm.ReflectionResult, error)
	}
}

// NewCritic wraps an llm.AnthropicCritic (or anything with the same shape) so
// the Reflector can call it.
func NewCritic(c interface {
	CritiqueSession(ctx context.Context, transcript string) (llm.ReflectionResult, error)
}) Critic {
	return &llmCriticAdapter{inner: c}
}

func (a *llmCriticAdapter) CritiqueSession(ctx context.Context, transcript string) (ReflectionResult, error) {
	res, err := a.inner.CritiqueSession(ctx, transcript)
	if err != nil {
		return ReflectionResult{}, err
	}
	out := ReflectionResult{
		Critique:     res.Critique,
		QualityScore: res.QualityScore,
		Kind:         res.Kind,
	}
	for _, l := range res.Lessons {
		out.Lessons = append(out.Lessons, Lesson{Text: l.Text, Confidence: l.Confidence})
	}
	return out, nil
}
