package agent

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Generic tool-result trimming.
//
// An agent turn re-reads its entire transcript on every LLM call (~10-20x per
// turn). A single fat tool result - a 5,000-line `go build`, a full file dump,
// a verbose API response - therefore gets re-ingested by the brain on every
// subsequent call in the turn, which is what actually burns the rate-limit
// token budget (prompt caching discounts the COST of re-reads but not the
// COUNT, so this is the lever that reduces quota consumption).
//
// trimToolResult collapses the middle of an over-budget result down to its
// head, tail, and any error/failure lines, leaving a marker. It is generic
// over ALL tools (no per-vendor branch) and is applied ONLY to the copy that
// enters the message transcript - the FULL output is still streamed to the UI
// and captured to mem_observations via the PostToolUse hook, so nothing is
// lost from the record. The model can re-fetch a narrower slice if it needs
// the elided detail.

const (
	defaultToolResultMaxTokens = 3000 // ~12k chars; tune via env
	trimHeadLines              = 60
	trimTailLines              = 60
	trimMaxErrorLines          = 40
)

// errLineRe flags lines worth preserving even from the elided middle: build
// failures, panics, stack traces, permission problems. Case-insensitive.
var errLineRe = regexp.MustCompile(`(?i)(\berror\b|\bpanic\b|\bfatal\b|\bfail(ed|ure)?\b|\bexception\b|traceback|stderr|denied|refused|undefined|not found|cannot |unable to |\bFAIL\b|✗|❌|^\s*at )`)

// toolResultMaxChars returns the char budget above which a result is trimmed.
// INFINITY_TOOL_RESULT_MAX_TOKENS sets the token budget (×4 for chars); 0 or a
// negative value disables trimming entirely.
func toolResultMaxChars() int {
	maxTokens := defaultToolResultMaxTokens
	if v := strings.TrimSpace(os.Getenv("INFINITY_TOOL_RESULT_MAX_TOKENS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			maxTokens = n
		}
	}
	if maxTokens <= 0 {
		return 0 // disabled
	}
	return maxTokens * 4
}

// trimToolResult returns output unchanged when it is within budget (the common
// case), otherwise a head + tail + error-lines digest with an elision marker.
func trimToolResult(name, output string) string {
	max := toolResultMaxChars()
	if max <= 0 || len(output) <= max {
		return output
	}

	lines := strings.Split(output, "\n")

	// Few but very long lines (e.g. a single minified blob): fall back to a
	// straight character window so we still cut the size.
	if len(lines) <= trimHeadLines+trimTailLines {
		half := max / 2
		elided := len(output) - 2*half
		return output[:half] +
			fmt.Sprintf("\n\n[… %d chars elided; full output captured in memory …]\n\n", elided) +
			output[len(output)-half:]
	}

	keep := make([]bool, len(lines))
	for i := 0; i < trimHeadLines; i++ {
		keep[i] = true
	}
	for i := len(lines) - trimTailLines; i < len(lines); i++ {
		keep[i] = true
	}
	// Preserve error/failure lines from the middle so a buried build error is
	// never dropped. Capped so a log that is mostly errors can't defeat the
	// trim.
	errKept := 0
	for i := trimHeadLines; i < len(lines)-trimTailLines && errKept < trimMaxErrorLines; i++ {
		if errLineRe.MatchString(lines[i]) {
			keep[i] = true
			errKept++
		}
	}

	var b strings.Builder
	b.Grow(max + 256)
	droppedLines, droppedChars := 0, 0
	flush := func() {
		if droppedLines > 0 {
			fmt.Fprintf(&b, "[… %d lines elided (%d chars); full output captured in memory …]\n", droppedLines, droppedChars)
			droppedLines, droppedChars = 0, 0
		}
	}
	for i, l := range lines {
		if keep[i] {
			flush()
			b.WriteString(l)
			if i < len(lines)-1 {
				b.WriteByte('\n')
			}
			continue
		}
		droppedLines++
		droppedChars += len(l) + 1
	}
	flush()
	return b.String()
}
