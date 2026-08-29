package tools

// "Still spinning is not a status. It is an unhandled failure state."
//
// Jarvis's own diagnosis of the 8m08s coding run the boss watched: the worker
// only reported when it FINISHED, so the interface showed a spinner with no
// intermediate proof, and the only way to learn anything was to interrupt it.
//
// The progress was never missing - `code_agent` has written a heartbeat every
// 15s since it shipped, naming the exact tool and file Claude Code is on. Two
// things stopped it reaching the boss:
//
//  1. It reported a progress fraction of literally 0 on every beat, so the
//     bar could not move even where something drew it.
//  2. The one live surface for a running job - the pinned dock under the chat
//     - watches only `background.build` runs, so a `code_agent` run did not
//     appear in it at all.
//
// Both are fixed at the run row, which is the right home: mem_runs is what
// survives navigation, refresh and a second device (CLAUDE.md, "Server-tracked
// progress"), and Studio reads it live over realtime. Deliberately NOT a new
// WebSocket frame - Studio drops progress frames from the transcript on
// purpose, so the dock stays the single place a job's status lives and the
// boss never scrolls the chat hunting for it.

// ProgressForSteps maps a running step count onto a progress bar that always
// moves forward and never reaches 1.0 while work is still going - the bar is
// honest about being an estimate, and a job that finishes fast doesn't jump
// backwards. The background agent uses the same curve, so the two engines
// draw the same bar for the same amount of work rather than making one job
// look like a different amount of progress depending on which one ran it.
func ProgressForSteps(count int) float32 {
	if count <= 0 {
		return 0.15
	}
	progress := float32(0.15 + 0.1*float32(count))
	if progress > 0.9 {
		progress = 0.9
	}
	return progress
}
