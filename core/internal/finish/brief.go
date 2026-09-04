package finish

import (
	"fmt"
	"strings"
	"time"
)

// buildBrief writes what Jarvis is woken up with.
//
// It carries FACTS and one decision. Everything that could be got wrong by
// being forgotten — noticing the job stopped, reading the repo, finding the
// session id to resume from, counting the passes — has already happened in Go
// by the time this is written. What is left is the part a model is actually
// for: whether this work should be continued, replanned, or handed back.
//
// It is addressed to Jarvis, not to the boss. The boss sees whatever Jarvis
// decides to say, in Jarvis's own voice, which is the point: a machine-worded
// "run 4f2a stopped_reason=still_working" is not a companion telling you your
// build stalled.
func buildBrief(s stranded, r Report, maxPasses int) string {
	var b strings.Builder

	b.WriteString("[Automatic check, no one asked for this] A coding job you started stopped without finishing, and nobody has picked it back up.\n\n")
	b.WriteString("You are in a side session opened for this one check. The boss is not reading it: anything that needs him " +
		"goes to him through `surface_item`, in your own words, not as a reply here.\n\n")

	fmt.Fprintf(&b, "**The job:** %s\n", firstNonEmpty(strings.TrimSpace(s.label), "a Claude Code run"))
	fmt.Fprintf(&b, "**Repo:** %s\n", s.repo)
	fmt.Fprintf(&b, "**Started from:** conversation `%s`\n", s.sessionID)
	if s.planID != "" {
		fmt.Fprintf(&b, "**Its plan:** `%s` — `plan_get` with that `plan_id` reads it, `plan_resume` with it picks the next step back up.\n", s.planID)
	} else {
		b.WriteString("**Its plan:** that conversation has no active plan on record, so there is no step to settle.\n")
	}
	fmt.Fprintf(&b, "**How it ended:** %s, after %s.\n", reasonLine(s.reason), humanDuration(s.endedAt.Sub(s.startedAt)))
	if f := strings.TrimSpace(s.lastFile); f != "" {
		fmt.Fprintf(&b, "**Last thing it touched:** %s\n", f)
	}
	if sum := clip(strings.TrimSpace(s.summary), 900); sum != "" {
		fmt.Fprintf(&b, "\n**What it reported before it stopped:**\n%s\n", sum)
	}

	b.WriteString("\n**What the repo looks like right now")
	if r.Gathered() {
		b.WriteString(" (I just checked):**\n")
		if r.Branch != "" || r.Head != "" {
			fmt.Fprintf(&b, "- On %s at %s\n", firstNonEmpty(r.Branch, "an unnamed branch"), firstNonEmpty(r.Head, "an unknown commit"))
		}
		switch n := len(r.Dirty); {
		case n == 0:
			b.WriteString("- Working tree is CLEAN — nothing uncommitted. Either its work was already committed, or it never landed anything.\n")
		default:
			fmt.Fprintf(&b, "- %d file(s) with uncommitted changes: %s\n", n, strings.Join(clipList(r.Dirty, 12), ", "))
		}
		if ds := strings.TrimSpace(r.DiffStat); ds != "" {
			fmt.Fprintf(&b, "- Diffstat:\n```\n%s\n```\n", clip(ds, 700))
		}
	} else {
		// Never let a failed look read as an empty repo.
		fmt.Fprintf(&b, ":** I could not check — %s. Treat the state of that repo as UNKNOWN and look yourself before you act on it.\n", r.Err)
	}

	if s.claudeSes != "" {
		fmt.Fprintf(&b, "\n**To continue it cheaply:** call `code_agent` with `resume_session: \"%s\"` and `repo: \"%s\"`. "+
			"That reloads everything Claude already read, wrote and tried in that session, so `task` should be ONLY what is left to do — "+
			"not the original brief again, which would make it redo work that is already on disk.\n", s.claudeSes, s.repo)
	} else {
		b.WriteString("\n**Note:** that run never recorded a Claude session id, so it can't be resumed — a fresh `code_agent` call would start cold. " +
			"Scope it to what the evidence above shows is still missing.\n")
	}

	fmt.Fprintf(&b, "\n**Your call** (attempt %d of %d — after that I stop offering): continue it, replan it, or tell the boss it needs him. "+
		"Pick one and do it now. If you continue it, keep this pass SMALL and finish it: the reason it stopped the first time is that it was too big for one window. "+
		"If the evidence says the work is actually complete, say so and close the plan step instead of running anything.\n", s.pass, maxPasses)

	return b.String()
}

// buildCompletedNotice is the card for a job that ACTUALLY FINISHED and was
// never reported: a title and a body in plain English, addressed to the boss.
//
// This is the other half of the 2026-08-29 failure. The build succeeded, wrote
// a full report, and the boss was told it had failed. The mechanics (noticing,
// reading the transcript, correcting the run row) have already happened in Go
// by the time this is written, and nothing that remains is a decision, so it
// goes to his inbox as a notice rather than waking a model to say the same
// thing at the price of a full turn over his chat.
func buildCompletedNotice(c candidate, v Verdict, planID string) (title, body string) {
	name := jobName(c.label)
	if v.IsError {
		title = "Build finished with errors: " + name
	} else {
		title = "Build finished: " + name
	}
	title = clip(title, 140)

	var b strings.Builder
	if v.IsError {
		b.WriteString("This one ran to the end and reported a failure. What follows is its own account of what went wrong.\n\n")
	} else {
		fmt.Fprintf(&b, "This one finished after %s. You were last told it had stalled or failed; it had not.\n\n",
			humanDuration(time.Since(c.startedAt)))
	}
	if report := strings.TrimSpace(v.Report); report != "" {
		fmt.Fprintf(&b, "%s\n", clip(report, 4000))
	} else {
		b.WriteString("It wrote no closing summary of its own. Check the repo before trusting what landed.\n")
	}
	if len(v.Files) > 0 {
		fmt.Fprintf(&b, "\nFiles it touched: %s\n", strings.Join(clipList(v.Files, 10), ", "))
	}
	if planID != "" {
		b.WriteString("\nA plan step in that conversation may still be waiting on this. Open the chat and I will close it out.\n")
	}
	return title, strings.TrimRight(b.String(), "\n")
}

// reasonLine turns a stopped_reason into something a person would say.
func reasonLine(reason string) string {
	switch strings.TrimSpace(reason) {
	case "still_working":
		return "it was STILL WORKING when we stopped following it — it was never stopped and never failed, we just ran out of window"
	case "interrupted":
		return "it was interrupted before it reached a verdict"
	case "":
		return "it closed without a verdict"
	default:
		return "it closed without a verdict (" + reason + ")"
	}
}

func humanDuration(d time.Duration) string {
	if d <= 0 {
		return "an unknown time"
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func clipList(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	out := append([]string{}, items[:n]...)
	return append(out, fmt.Sprintf("…and %d more", len(items)-n))
}
