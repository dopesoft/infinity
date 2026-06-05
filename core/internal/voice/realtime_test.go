package voice

import (
	"strings"
	"testing"
)

// The realtime model is demoted to a transcriber now - identity, tools, and
// dispatch discipline live in the agent loop, not in a frozen voice prompt, so
// the old buildVoiceInstructions trim/identity tests are gone. What's worth
// testing here is the SentenceChunker that decides when to speak.

func TestSentenceChunkerEmitsCompleteSentences(t *testing.T) {
	var c SentenceChunker

	// A partial clause should stay buffered, not emit.
	if got := c.Push("Right, checking your inbox"); len(got) != 0 {
		t.Fatalf("partial clause should not emit, got %v", got)
	}
	// Completing the sentence emits it.
	got := c.Push(" now. Pulling the thread")
	if len(got) != 1 || strings.TrimSpace(got[0]) != "Right, checking your inbox now." {
		t.Fatalf("expected one complete sentence, got %v", got)
	}
	// Flush returns the trailing partial.
	tail := c.Flush()
	if len(tail) != 1 || !strings.Contains(tail[0], "Pulling the thread") {
		t.Fatalf("flush should return trailing partial, got %v", tail)
	}
}

func TestSentenceChunkerKeepsShortAbbreviations(t *testing.T) {
	var c SentenceChunker
	// "Mr." is under the min length and shouldn't be spoken as its own clip
	// while more text is still coming.
	got := c.Push("Mr. Khaya is on the call")
	if len(got) != 0 {
		t.Fatalf("short abbreviation should not split into its own clip, got %v", got)
	}
	final := c.Push(".")
	if len(final) != 1 || !strings.Contains(final[0], "Mr. Khaya is on the call") {
		t.Fatalf("expected the full sentence after the real terminator, got %v", final)
	}
}

func TestSentenceChunkerSplitsOnNewline(t *testing.T) {
	var c SentenceChunker
	got := c.Push("First the short version\nthen the detail")
	if len(got) != 1 || !strings.Contains(got[0], "First the short version") {
		t.Fatalf("newline should break a spoken chunk, got %v", got)
	}
}
