package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// Refusing the ten-requirement monolith.
//
// 2026-08-29. One `code_agent` call carried an entire feature redesign as ten
// numbered requirements, including "run formatting/lint/typecheck/tests and
// production build" as requirement ten. It ran for 47 minutes with no
// checkpoint, nothing surfaced and nothing verifiable until the very end. The
// boss watched a spinner and could not tell whether to trust any of it.
//
// The skill now teaches bounded passes — one verifiable piece of work per call,
// checked before the next — but a sentence in a skill is a sentence the runtime
// model drops, and this one is a MECHANIC (Rule #1b): there is exactly one
// correct behaviour and no judgment about whether ten requirements is one pass.
// So it is enforced here, where it cannot be forgotten.
//
// It is a REDIRECT, not a wall. Nothing has launched and nothing is billed when
// it fires, the reply says exactly what to send instead, and the very next call
// goes through. The cost of being wrong is one round trip; the cost of NOT
// having it is another 47-minute black box.
//
// Deliberately scoped to `code_agent` and NOT to `background_build`: walking
// away from a whole job is precisely what background builds are for, and
// checkpointing something nobody is watching buys nothing.

// briefRequirementLimit is where a brief stops being one pass. Six is
// generous — a bounded pass is normally one or two things plus its acceptance
// command — so tripping it means the call really is a plan, not a step.
const briefRequirementLimit = 6

// numberedItemRe matches a top-level enumerated requirement: "1. ", "2) ",
// with at most a little leading indent so a nested sub-list inside one
// requirement is not counted as its own.
var numberedItemRe = regexp.MustCompile(`(?m)^ {0,3}(\d{1,2})[.)]\s+\S`)

// countBriefRequirements returns how many distinct numbered requirements a
// brief carries.
//
// Numbering only, on purpose. Bullets in a brief are overwhelmingly
// constraints, acceptance criteria and file lists — the parts that make ONE
// pass well-specified — and counting them would refuse exactly the good briefs
// this is meant to encourage. A numbered list is the shape of "and then, and
// then, and then", which is the shape that goes wrong.
func countBriefRequirements(task string) int {
	seen := map[string]bool{}
	for _, m := range numberedItemRe.FindAllStringSubmatch(task, -1) {
		seen[m[1]] = true
	}
	return len(seen)
}

// splitBriefGuidance returns the redirect for a brief that is a plan rather
// than a pass. ok=false means the brief is fine and the launch proceeds.
func splitBriefGuidance(task string) (string, bool) {
	n := countBriefRequirements(task)
	if n < briefRequirementLimit {
		return "", false
	}
	return fmt.Sprintf("NOT LAUNCHED — that brief is a plan, not a pass. Nothing was started and nothing was billed.\n\n"+
		"It carries %d numbered requirements. One `code_agent` call is ONE piece of work you can check when it comes back; "+
		"%d of them in a single run means a wrong turn in the first five minutes stays invisible for the next forty, which is "+
		"exactly what happened to the boss on the last long build.\n\n"+
		"Do this instead:\n\n"+
		"1. Call `code_agent` again with just requirement 1 (two if they are genuinely one change), and give it the command "+
		"that proves that pass worked.\n"+
		"2. When it returns, VERIFY it yourself — run the build or the test with `bash_run`, look at the diff — and tell the "+
		"boss in one line what landed.\n"+
		"3. Continue with `resume_session` (the run's `meta.claude_session_id`) and the NEXT requirement as the task. That "+
		"reloads everything Claude already read and wrote, so nothing is redone.\n\n"+
		"If a plan is open, one requirement is one step: settle each with `plan_update` as you go.\n\n"+
		"If the boss has genuinely stepped away and wants the whole thing done unattended, that is what `background_build` is "+
		"for — use it deliberately, not as a way around this.", n, n), true
}

// briefSummary is the one-line note recorded on the run when a brief was
// accepted but is on the large side, so a build that later goes wrong can be
// read back against the size of what it was asked to do.
func briefSummary(task string) string {
	n := countBriefRequirements(task)
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d numbered requirement(s), %d chars", n, len(strings.TrimSpace(task)))
}
