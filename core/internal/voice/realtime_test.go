package voice

import (
	"strings"
	"testing"
)

func TestBuildVoiceInstructionsIncludesIdentityAccessBlock(t *testing.T) {
	instructions, err := buildVoiceInstructions("You are Jarvis.\n\nAbout the boss: Kai builds Infinity.")
	if err != nil {
		t.Fatalf("buildVoiceInstructions returned error: %v", err)
	}

	mustContain := []string{
		"Your name is Jarvis.",
		"Your boss is Kai.",
		"Infinity is the platform and codebase you run inside.",
		"You are Kai's developer and operator for Infinity.",
		"You can inspect, change, test, commit, deploy, or kick off code work through the available tools.",
		"never deflect with 'ask the developers', 'contact support', or 'I cannot modify the system'",
	}
	for _, want := range mustContain {
		if !strings.Contains(instructions, want) {
			t.Fatalf("instructions missing %q\n\n%s", want, instructions)
		}
	}
}

func TestBuildVoiceInstructionsPreservesIdentityAccessBlockWhenOverBudget(t *testing.T) {
	oversizedSections := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		oversizedSections = append(oversizedSections, strings.Repeat("tool catalog filler ", 120))
	}
	systemPrompt := strings.Join(oversizedSections, "\n\n")

	instructions, err := buildVoiceInstructions(systemPrompt)
	if err != nil {
		t.Fatalf("buildVoiceInstructions returned error: %v", err)
	}

	if !strings.Contains(instructions, voiceIdentityAccessBlock) {
		t.Fatalf("identity access block was trimmed from over-budget instructions")
	}
	if got := estimateTokens(instructions); got > realtimeInstructionHardLimit {
		t.Fatalf("instructions exceed hard limit: got %d, limit %d", got, realtimeInstructionHardLimit)
	}
}
