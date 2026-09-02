package server

import (
	"testing"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// llm.ContextWindow backs the chat context meter's denominator. A wrong value
// makes the meter read the wrong % on the boss's actual model - this caught a
// real bug where every gpt-5.x returned 400K when gpt-5.4 ships 1.05M. The
// numbers below are OpenAI/Anthropic/DeepSeek model-card values; update this
// test in lock step when a card changes.
func TestContextWindowFor(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		// OpenAI gpt-5.x - window differs by minor version AND tier.
		{"gpt-5.4", 1_050_000},
		{"openai_oauth:gpt-5.4", 1_050_000}, // vendor-prefixed form must still resolve
		{"gpt-5.4-pro", 1_050_000},
		{"gpt-5.4-mini", 400_000}, // mini/nano stay 400K even on a 1.05M minor
		{"gpt-5.4-nano", 400_000},
		{"gpt-5.5", 1_050_000},
		{"gpt-5.5-pro", 1_050_000},
		{"gpt-5.6", 1_050_000},
		{"gpt-5.6-sol", 1_050_000},
		{"gpt-5.6-terra", 1_050_000},
		{"gpt-5.6-luna", 1_050_000},
		{"gpt-5.2", 400_000},
		{"gpt-5.2-pro", 400_000},
		{"gpt-5.1", 400_000},
		{"gpt-5", 400_000},
		{"gpt-5-mini", 400_000},
		{"gpt-5-nano", 400_000},
		// OpenAI o-series + gpt-4.x.
		{"o4-mini", 200_000},
		{"o3", 200_000},
		{"o3-pro", 200_000},
		{"o1", 200_000},
		{"o1-mini", 128_000}, // the 128K exception
		{"gpt-4.1", 1_000_000},
		{"gpt-4.1-mini", 1_000_000},
		{"gpt-4o", 128_000},
		{"gpt-4o-mini", 128_000},
		// Anthropic - effective 200K unless the id carries the 1m marker.
		{"claude-opus-4-7", 200_000},
		{"claude-opus-4-6", 200_000},
		{"claude-sonnet-4-6", 200_000},
		{"claude-haiku-4-5-20251001", 200_000},
		{"claude-opus-4-8[1m]", 1_000_000},
		// Google Gemini - 3 Pro/2.5 Pro are 1M (NOT 2M); 3 Flash is 200K.
		{"gemini-3-pro", 1_000_000},
		{"gemini-3-flash", 200_000},
		{"gemini-2.5-pro", 1_000_000},
		{"gemini-2.5-flash", 1_000_000},
		{"gemini-2.5-flash-lite", 1_000_000},
		{"gemini-2.0-flash", 1_000_000},
		// DeepSeek V4 - 1M (forward-looking; not in catalog yet).
		{"deepseek-v4-flash", 1_000_000},
		{"deepseek-v4-pro", 1_000_000},
		// Unknown - conservative default.
		{"some-unknown-model", 200_000},
	}
	// Claude Code tier aliases: what Settings stores when the plan brain is
	// picked, and what the running turns log (model="opus[1m]"). These fell
	// through to the 200K default and under-reported his window by 5x.
	cases = append(cases,
		struct {
			model string
			want  int
		}{"opus[1m]", 1_000_000},
		struct {
			model string
			want  int
		}{"opus", 1_000_000},
		struct {
			model string
			want  int
		}{"sonnet", 1_000_000},
		struct {
			model string
			want  int
		}{"haiku", 200_000},
		// Fable 5.1, shipped 2026-09-01. A new model in the family must not
		// fall through to the 200K default and under-report his window.
		struct {
			model string
			want  int
		}{"claude-fable-5-1", 1_000_000},
		struct {
			model string
			want  int
		}{"fable", 1_000_000},
	)
	for _, c := range cases {
		if got := llm.ContextWindow(c.model); got != c.want {
			t.Errorf("llm.ContextWindow(%q) = %d, want %d", c.model, got, c.want)
		}
	}
}

// Why: a fill is a measurement of one prompt sent to one model, and the boss
// switches models inside a live conversation. A 900K reading taken on the
// previous brain, divided by the new brain's window, is what showed him a
// full red dial on a 1M window he had barely touched.
func TestFillOnlyCountsWhenThisBrainMeasuredIt(t *testing.T) {
	cases := []struct {
		name     string
		measured string
		active   string
		want     bool
	}{
		{"same brain, same id", "claude-opus-5", "claude-opus-5", true},
		{"same brain, alias vs full id", "opus[1m]", "opus", true},
		{"same brain, dated suffix", "claude-opus-5", "claude-opus-5-20260101", true},
		{"different brain", "gpt-5.6-sol", "opus[1m]", false},
		{"different brain, both claude-ish", "claude-haiku-4-5", "claude-opus-5", false},
		{"unknown measurer is trusted", "", "opus[1m]", false},
	}
	for _, c := range cases {
		got := c.measured != "" && sameBrain(c.measured, c.active)
		if got != c.want {
			t.Errorf("%s: sameBrain(%q, %q) = %v, want %v", c.name, c.measured, c.active, got, c.want)
		}
	}
}
