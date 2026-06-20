package agent

import (
	"strings"
	"testing"
)

func TestTrimToolResult_SmallPassesThrough(t *testing.T) {
	in := "line1\nline2\nline3"
	if got := trimToolResult("bash", in); got != in {
		t.Fatalf("small output should pass through unchanged, got %q", got)
	}
}

func TestTrimToolResult_CollapsesMiddleKeepsHeadTail(t *testing.T) {
	t.Setenv("INFINITY_TOOL_RESULT_MAX_TOKENS", "200") // ~800 chars budget
	var b strings.Builder
	b.WriteString("HEAD_MARKER\n")
	for i := 0; i < 4000; i++ {
		b.WriteString("noise line of filler text to blow the budget\n")
	}
	b.WriteString("TAIL_MARKER")
	got := trimToolResult("bash", b.String())

	if len(got) >= len(b.String()) {
		t.Fatalf("expected trimmed output to be smaller; got %d >= %d", len(got), len(b.String()))
	}
	if !strings.Contains(got, "HEAD_MARKER") || !strings.Contains(got, "TAIL_MARKER") {
		t.Fatalf("head/tail must survive trimming: %q", firstN(got, 200))
	}
	if !strings.Contains(got, "elided") {
		t.Fatalf("expected an elision marker, got %q", firstN(got, 200))
	}
}

func TestTrimToolResult_PreservesErrorLines(t *testing.T) {
	t.Setenv("INFINITY_TOOL_RESULT_MAX_TOKENS", "200")
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("ok progress line\n")
	}
	b.WriteString("./main.go:42:13: undefined: somethingBroken\n") // buried error
	for i := 0; i < 200; i++ {
		b.WriteString("ok progress line\n")
	}
	got := trimToolResult("claude_code__bash", b.String())
	if !strings.Contains(got, "undefined: somethingBroken") {
		t.Fatalf("buried error line must be preserved through trimming")
	}
}

func TestTrimToolResult_DisabledByZero(t *testing.T) {
	t.Setenv("INFINITY_TOOL_RESULT_MAX_TOKENS", "0")
	in := strings.Repeat("x\n", 100000)
	if got := trimToolResult("bash", in); got != in {
		t.Fatalf("trimming disabled (0) must pass output through unchanged")
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
