package tools

import (
	"strings"
	"testing"
)

func TestTrimToolDescriptionKeepsFirstParagraph(t *testing.T) {
	desc := "Executes a bash command in a persistent shell.\n\nIMPORTANT: before committing, run git status...\n\nMore house rules."
	got := trimToolDescription(desc)
	if got != "Executes a bash command in a persistent shell." {
		t.Fatalf("got %q", got)
	}
}

func TestTrimToolDescriptionCapsLongParagraph(t *testing.T) {
	desc := strings.Repeat("word ", 400) // one paragraph, 2000 chars
	got := trimToolDescription(desc)
	if len(got) > mcpDescriptionMaxChars+3 {
		t.Fatalf("cap breached: %d chars", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected an ellipsis, got %q", got[len(got)-10:])
	}
	if strings.Contains(got, " …") || strings.HasSuffix(strings.TrimSuffix(got, "…"), " ") {
		t.Fatalf("should cut on a word boundary without a dangling space: %q", got[len(got)-12:])
	}
}

func TestTrimToolDescriptionShortUnchanged(t *testing.T) {
	if got := trimToolDescription("  Read a file.  "); got != "Read a file." {
		t.Fatalf("got %q", got)
	}
	if got := trimToolDescription(""); got != "" {
		t.Fatalf("got %q", got)
	}
}
