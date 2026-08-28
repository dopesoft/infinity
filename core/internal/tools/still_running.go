package tools

import "errors"

// The "still working" sentinel, exported for the layers above code_agent.
//
// stillRunningError (code_agent.go) means the inline WAIT WINDOW closed, not
// the job: `claude -p` is nohup-reparented on the Mac and keeps editing files.
// Its own doc says it plainly — "The job was NOT killed."
//
// Everything above this package has to be able to tell that apart from a real
// failure, because getting it wrong is what put a red ❌ and a "Build failed"
// push in front of the boss for work that had actually landed on disk. The
// concrete type is unexported, so these two functions are the seam: TYPE-based
// (errors.As, wrapping-safe), never a substring match on the message. Matching
// on wording is how the bug survived — serve.go's isRecoverableErr scanned for
// "timeout"/"eof" and this error's wording simply isn't one of them.

// IsStillRunning reports whether err (or anything it wraps) is the
// "wait window closed, the worker is STILL GOING" sentinel.
//
// A true result means: do NOT settle a plan step failed, do NOT push "Build
// failed", do NOT close the run row as an error. The work is continuing.
func IsStillRunning(err error) bool {
	var s *stillRunningError
	return errors.As(err, &s)
}

// StillRunningMessage returns the boss-facing "it's still going" line for a
// still-running error, or "" for anything else — so a caller can use the empty
// string as the "this was a real error, handle it normally" branch.
func StillRunningMessage(err error) string {
	var s *stillRunningError
	if errors.As(err, &s) {
		return s.inlineMessage()
	}
	return ""
}
