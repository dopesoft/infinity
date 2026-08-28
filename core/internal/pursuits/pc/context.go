package pc

import (
	"fmt"
	"strings"
	"time"
)

// FormatChatContext renders the cockpit as the turn-1 context block for a
// seeded "Discuss with Jarvis" session.
//
// This is the mechanic behind "the conversation starts with the pursuit
// loaded" (Rule #1b): the agent receives the identity, objective, programme
// position, today's proof state, recent evidence, success memories, patterns,
// and the coach's current phase without having to call a single tool. The
// judgment about what to SAY given that context lives in the seeded skill.
//
// Kept plain-text and bounded: this rides in every turn-1 prompt for the
// session, so it must stay small enough not to crowd the conversation.
func FormatChatContext(c Cockpit) string {
	var b strings.Builder

	b.WriteString("Psycho-Cybernetics pursuit: ")
	b.WriteString(c.Pursuit.Title)
	b.WriteString("\n")
	fmt.Fprintf(&b, "Programme position: day %d of %d, cycle %d.\n",
		c.State.CurrentDay, c.State.CycleLengthDays, c.State.CycleNumber)
	if c.State.MissedDaysCount > 0 {
		fmt.Fprintf(&b, "Days missed this cycle: %d. Treat these as data, never as a failing grade.\n",
			c.State.MissedDaysCount)
	}

	// ── The two things the whole programme turns on ──
	b.WriteString("\nOperating identity (the boss's own words):\n")
	b.WriteString(valueOrPlaceholder(c.State.CurrentIdentity, "not set yet - onboarding has not run"))
	b.WriteString("\n\nAbundance objective:\n")
	b.WriteString(valueOrPlaceholder(c.State.CurrentObjective, "not set yet - onboarding has not run"))
	if p := strings.TrimSpace(c.State.CurrentLimitingPattern); p != "" {
		b.WriteString("\n\nLimiting pattern he is working against:\n")
		b.WriteString(p)
	}

	if pt := c.State.PressureTest; strings.TrimSpace(pt.Fear+pt.Doubt+pt.Alternate) != "" {
		b.WriteString("\n\nIdentity pressure test:\n")
		writeLabelled(&b, "Where it might crack under fear", pt.Fear)
		writeLabelled(&b, "The doubt he already has about it", pt.Doubt)
		writeLabelled(&b, "The alternative framing he considered", pt.Alternate)
	}

	// ── Today ──
	b.WriteString("\n\nToday's proof actions:\n")
	if len(c.TodayProofs) == 0 {
		b.WriteString("  none pledged yet today\n")
	}
	for _, p := range c.TodayProofs {
		status := "not yet taken"
		if p.Taken {
			status = "taken"
		}
		fmt.Fprintf(&b, "  - %s (%s)\n", p.Label, status)
		if n := strings.TrimSpace(p.Note); n != "" {
			fmt.Fprintf(&b, "    note: %s\n", n)
		}
	}

	b.WriteString("\nToday's captures:\n")
	if len(c.TodayEvidence) == 0 {
		b.WriteString("  nothing captured yet today\n")
	}
	for _, e := range c.TodayEvidence {
		fmt.Fprintf(&b, "  - [%s] %s\n", e.Kind, e.Body)
	}

	// ── Recent history the coach should reason over ──
	writeEvidenceDigest(&b, c.RecentEvidence, c.TodayEvidence)
	writeProofDigest(&b, c.RecentProofs)

	if c.RehearsalMemory != nil {
		b.WriteString("\nToday's rehearsal memory (the one the cockpit is showing him):\n")
		fmt.Fprintf(&b, "  %s", c.RehearsalMemory.Title)
		if body := strings.TrimSpace(c.RehearsalMemory.Body); body != "" {
			fmt.Fprintf(&b, ": %s", truncate(body, 300))
		}
		b.WriteString("\n")
	}

	if len(c.Memories) > 0 {
		b.WriteString("\nSuccess memories he has banked (Maltz's winning feeling material):\n")
		for i, m := range c.Memories {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "  - %s", m.Title)
			if body := strings.TrimSpace(m.Body); body != "" {
				fmt.Fprintf(&b, ": %s", truncate(body, 220))
			}
			b.WriteString("\n")
		}
	}

	if len(c.Patterns) > 0 {
		b.WriteString("\nPatterns logged over time:\n")
		for i, p := range c.Patterns {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "  - [%s] %s\n", p.Kind, truncate(p.Body, 220))
		}
	}
	if len(c.Corrections) > 0 {
		b.WriteString("\nCorrections he has committed to:\n")
		for i, p := range c.Corrections {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "  - %s\n", truncate(p.Body, 220))
		}
	}

	if len(c.RecentSessions) > 0 {
		b.WriteString("\nRecent coaching sessions:\n")
		for i, s := range c.RecentSessions {
			if i >= 6 {
				break
			}
			fmt.Fprintf(&b, "  - %s on day %d of cycle %d (%s)\n",
				s.Kind, s.DayInCycle, s.CycleNumber, s.OccurredAt.Format(time.RFC3339))
			for _, key := range sessionAnswerOrder {
				if v := answerString(s.Answers, key); v != "" {
					fmt.Fprintf(&b, "      %s: %s\n", key, truncate(v, 240))
				}
			}
		}
	}

	if len(c.CycleReviews) > 0 {
		b.WriteString("\nPrevious cycle reviews:\n")
		for i, rv := range c.CycleReviews {
			if i >= 3 {
				break
			}
			fmt.Fprintf(&b, "  - cycle %d wins: %s\n", rv.CycleNumber, truncate(rv.Wins, 200))
			if m := strings.TrimSpace(rv.Misses); m != "" {
				fmt.Fprintf(&b, "    misses: %s\n", truncate(m, 200))
			}
		}
	}

	// ── The coach's current read ──
	b.WriteString("\nWhere the coach says he is right now:\n")
	fmt.Fprintf(&b, "  Phase: %s\n", c.Guidance.Phase)
	fmt.Fprintf(&b, "  Next step: %s\n", c.Guidance.Headline)
	if q := strings.TrimSpace(c.Guidance.Prompt); q != "" {
		fmt.Fprintf(&b, "  Open question: %s\n", q)
	}
	if c.Adjustment != nil {
		fmt.Fprintf(&b, "  Adaptive note: %s. %s\n",
			c.Adjustment.Headline, strings.TrimSpace(c.Adjustment.Body))
	}

	b.WriteString("\nHow to hold this conversation. You are his coach for this " +
		"programme, in Maxwell Maltz's framing. Nothing here is clinical and you " +
		"do not diagnose or treat anything; this is reflective self experimentation " +
		"he opted into. Do not restate the data above at him, he can see it. Pick " +
		"up where the phase says he is. When he decides something concrete in this " +
		"conversation, write it back with the pursuit_pc_write tool (log the " +
		"session, pledge or complete the proof, capture evidence or resistance, " +
		"bank a success memory, edit the identity or objective, or close the cycle) " +
		"so the cockpit and this chat never disagree. Never invent evidence, " +
		"memories, or proof actions on his behalf; those are his to write.\n")

	return b.String()
}

// sessionAnswerOrder is the stable order session answers are surfaced in, so
// the context block reads the same way every turn.
var sessionAnswerOrder = []string{
	"rehearsal", "proof_pledge", "fact", "interpretation", "lesson",
	"correction", "resistance", "smallest_next_step", "wins", "misses",
}

func writeLabelled(b *strings.Builder, label, value string) {
	if v := strings.TrimSpace(value); v != "" {
		fmt.Fprintf(b, "  %s: %s\n", label, v)
	}
}

// writeEvidenceDigest summarises evidence from previous days, skipping the
// rows already printed under "today".
func writeEvidenceDigest(b *strings.Builder, recent, today []Evidence) {
	todayIDs := make(map[string]bool, len(today))
	for _, e := range today {
		todayIDs[e.ID] = true
	}
	var lines []string
	for _, e := range recent {
		if todayIDs[e.ID] {
			continue
		}
		lines = append(lines, fmt.Sprintf("  - [%s] day %d: %s", e.Kind, e.DayInCycle, truncate(e.Body, 200)))
		if len(lines) >= 8 {
			break
		}
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString("\nRecent evidence and resistance from earlier days:\n")
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n")
}

// writeProofDigest reports how the last stretch of proof actions actually went.
// The taken/pledged ratio is the single most useful signal for whether the
// proof actions are sized right.
func writeProofDigest(b *strings.Builder, proofs []Proof) {
	if len(proofs) == 0 {
		return
	}
	taken := 0
	for _, p := range proofs {
		if p.Taken {
			taken++
		}
	}
	fmt.Fprintf(b, "\nProof actions overall: %d taken of %d pledged.\n", taken, len(proofs))
	shown := 0
	for _, p := range proofs {
		if p.Taken {
			continue
		}
		if shown == 0 {
			b.WriteString("Pledged but not taken:\n")
		}
		fmt.Fprintf(b, "  - day %d: %s\n", p.DayInCycle, truncate(p.Label, 200))
		shown++
		if shown >= 5 {
			break
		}
	}
}

func valueOrPlaceholder(s, placeholder string) string {
	if v := strings.TrimSpace(s); v != "" {
		return v
	}
	return placeholder
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
