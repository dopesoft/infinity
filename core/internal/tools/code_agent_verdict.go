package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/bridge"
)

// Reading a detached coding job's own files AFTER nobody is watching it.
//
// THE FAILURE. On 2026-08-29 a 47-minute Claude Code build finished
// successfully — `{"type":"result","subtype":"success","is_error":false,
// "duration_ms":2844766,"num_turns":170,"result":"Done. Here is the report…"}`
// — and Infinity told the boss it had failed. The report was written, sat on
// his Mac, and nothing ever read it. The run row said "still working", the
// plan step stayed `blocked`, and the work looked lost when it was not.
//
// Everything in the poll loop reads that transcript WHILE somebody is
// watching. This reads it afterwards, from the continuation poller, so a job
// that crossed the line with nobody there still gets its verdict.
//
// It answers three questions in one read-only round trip: are the job's files
// still there, is its process still alive, and did Claude write its terminal
// result. Those three are exactly what separates "it finished and nobody
// noticed", "it is still going, leave it alone", and "this row is dead and is
// blocking everything behind it".

// ClaudeJobVerdict is what a detached job's own files say about it.
type ClaudeJobVerdict struct {
	// Found: the job's transcript is still on the bridge. False means either
	// it was cleaned up after settling, or this is a different machine.
	Found bool
	// Alive: its process is still resident. `claude -p` does not exit while
	// its MCP servers are attached, so Alive and Done are BOTH commonly true —
	// which is the whole reason the status file could never be the signal.
	Alive bool
	// Done: Claude wrote its terminal result. The job is finished.
	Done    bool
	IsError bool
	Report  string
	// Files are the paths the visible part of the transcript shows it editing.
	Files []string
	// Err is why the read failed. Populated instead of returning a silently
	// empty verdict, because "I could not look" and "there was nothing there"
	// lead to opposite decisions (CLAUDE.md → self-healing).
	Err string
}

// Looked reports whether the read actually happened.
func (v ClaudeJobVerdict) Looked() bool { return v.Err == "" }

// claudeVerdictScript is read-only: an existence test, a liveness test, and
// two reads of the transcript. The two reads are budgeted to sit inside the
// bridge's 64KB reply cap with the completion signal FIRST, so a truncated
// reply can only ever cost evidence, never the verdict.
func claudeVerdictScript(dir, jobID string) string {
	f := newClaudeJobFilesIn(dir, jobID)
	return fmt.Sprintf(`if [ -f %s ]; then echo "FOUND:yes"; else echo "FOUND:no"; fi
P=$(cat %s 2>/dev/null | tr -d ' \t\n')
if [ -n "$P" ] && kill -0 "$P" 2>/dev/null; then echo "ALIVE:yes"; else echo "ALIVE:no"; fi
if tail -n 1 %s 2>/dev/null | head -c 64 | grep -q '^{"type":"result"'; then echo "RESULT:yes"; else echo "RESULT:no"; fi
echo "===LAST==="
tail -n 1 %s 2>/dev/null | head -c %d
echo ""
echo "===TAIL==="
tail -c %d %s 2>/dev/null
exit 0`,
		f.out, f.pid, f.out,
		f.out, claudeLastLineBytes,
		claudeTailBytes, f.out)
}

// newClaudeJobFilesIn is newClaudeJobFiles against an explicit directory, so a
// reader outside the runner does not depend on the package-level var the tests
// swap out from under it.
func newClaudeJobFilesIn(dir, jobID string) claudeJobFiles {
	return claudeJobFiles{
		out:      fmt.Sprintf("%s/%s.out", dir, jobID),
		err:      fmt.Sprintf("%s/%s.err", dir, jobID),
		status:   fmt.Sprintf("%s/%s.status", dir, jobID),
		pid:      fmt.Sprintf("%s/%s.pid", dir, jobID),
		pgid:     fmt.Sprintf("%s/%s.pgid", dir, jobID),
		settings: fmt.Sprintf("%s/settings.json", dir),
	}
}

// ReadClaudeJobVerdict reads job jobID's own files off b and reports what they
// say. Read-only: nothing is killed, nothing is deleted, nothing is written.
func ReadClaudeJobVerdict(ctx context.Context, b bridge.Bridge, repo, jobID string) ClaudeJobVerdict {
	var v ClaudeJobVerdict
	if b == nil {
		v.Err = "no bridge answered"
		return v
	}
	if strings.TrimSpace(jobID) == "" {
		v.Err = "that run has no job id to look up"
		return v
	}
	body, code, ok := b.Post(ctx, "/bash", map[string]any{
		"cmd":         claudeVerdictScript(codeAgentTmpDir, jobID),
		"cwd":         bridge.NormalizePath(b, repo),
		"timeout_sec": 20,
	})
	if !ok {
		v.Err = "the " + string(b.Name()) + " bridge could not be reached"
		return v
	}
	if code >= 300 {
		v.Err = fmt.Sprintf("the %s bridge answered %d", b.Name(), code)
		return v
	}
	out, _ := bridge.BashOutput(body)
	return parseClaudeVerdict(out)
}

// parseClaudeVerdict decodes claudeVerdictScript's output.
func parseClaudeVerdict(out string) ClaudeJobVerdict {
	var v ClaudeJobVerdict
	head, rest := splitMarker(out, "", "===LAST===")
	last, tail := splitMarker(rest, "", "===TAIL===")

	v.Found = strings.Contains(head, "FOUND:yes")
	v.Alive = strings.Contains(head, "ALIVE:yes")
	if !v.Found && !strings.Contains(head, "FOUND:no") {
		// Neither answer came back: the script did not run. Saying "no
		// transcript" here would read as "it produced nothing".
		v.Err = "the probe ran but answered nothing, so the job's files could not be checked"
		return v
	}
	if res, done := claudeTerminalResult(last); done {
		v.Done = true
		v.IsError = res.IsError
		v.Report = res.Result
	} else if strings.Contains(head, "RESULT:yes") {
		// The terminal line is there but too long to decode inside the budget.
		// It still means DONE — that is the fact the row turns on — and the
		// report is recovered from the tail rather than invented.
		v.Done = true
		if res := parseClaudeStreamResult(tail); res.parsed {
			v.IsError = res.IsError
			v.Report = res.Result
		}
	}
	v.Files = claudeTouchedFiles(tail)
	return v
}
