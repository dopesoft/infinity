// errand.go — what happens after the boss hangs up.
//
// He rings from the car, gives an instruction, and hangs up. The full agent then
// runs it: his memory, his skills, every tool. That part worked. What did NOT
// work was the report back.
//
// Delivery used to be a SENTENCE in the prompt ("when done, notify him with the
// outcome"), and this model drops sentences. Worse, it asked for a push, and no
// tool can send one: there is no push verb in the registry, so the instruction
// was not merely droppable, it was impossible. The result: the work happened, an
// artifact might land in Saved, and nothing told him any of it. He would have to
// go looking for a report he commissioned by voice.
//
// So the report is a MECHANIC now. Whatever the errand did or failed to do, code
// puts it in his inbox, pushes his phone, and links to the conversation Jarvis
// had with himself while doing it. Rule #1b: judgment (how to approach the ask)
// is data; delivery is code, and cannot be forgotten.
package phone

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/errs"
	"github.com/dopesoft/infinity/core/internal/push"
	"github.com/dopesoft/infinity/core/internal/runs"
	"github.com/dopesoft/infinity/core/internal/surface"
	"github.com/google/uuid"
)

// errandTimeout caps one spoken errand. Long enough to research, write, and
// deliver a real piece of work; short enough that a wedged run still reports.
const errandTimeout = 10 * time.Minute

// ErrandRunner executes the boss's spoken instruction as a full agent turn and
// returns what Jarvis finally SAID: his own words, which become the report.
//
// The seam takes and returns plain strings so this package never depends on the
// agent loop. serve.go supplies it.
type ErrandRunner func(ctx context.Context, sessionID, prompt string) (finalText string, err error)

// SetErrandRunner late-binds the executor (serve.go).
func (m *Manager) SetErrandRunner(fn ErrandRunner) {
	if m != nil {
		m.errand = fn
	}
}

// runErrand is the drive-home loop: a verified boss's spoken asks, executed the
// moment the line drops, and REPORTED, always.
func (m *Manager) runErrand(transcript string) {
	if m == nil || m.errand == nil {
		// Loud. A boss who gave an instruction on the phone and got silence is
		// the exact failure this system is not allowed to have.
		log.Printf("phone: the boss gave an errand on the phone but no executor is wired; it was DROPPED: %s", clip(transcript, 200))
		return
	}
	ctx := context.Background()

	sessionID := uuid.NewString()
	if m.pool != nil {
		// A real session row: the report links to it, so he can read exactly
		// what Jarvis did rather than trusting a summary.
		if _, err := m.pool.Exec(ctx, `
			INSERT INTO mem_sessions (id, kind, origin_ref, started_at)
			VALUES ($1::uuid, 'user', '{"kind":"phone_boss_ask"}'::jsonb, NOW())
			ON CONFLICT (id) DO NOTHING`, sessionID); err != nil {
			log.Printf("phone: opening the session for a phone errand failed: %v", err)
		}
	}

	// How to approach an errand shouted from a moving car is JUDGMENT, so it is
	// data (phone:persona:errand). If it is missing we still run the errand on
	// his raw words: dropping his instruction because a prompt row is absent
	// would be far worse than running it unframed.
	framing, err := m.loadPersona(ctx, "errand")
	if err != nil {
		log.Printf("phone: the errand framing is missing (run migration 181), running on his words alone: %v", err)
	}
	prompt := strings.TrimSpace(framing + "\n\nHis instruction, as he said it on the phone:\n\n" + transcript)

	infoLog.Printf("phone: running the boss's spoken errand (session %s)", sessionID)

	// Booked as a run so the errand is a first-class AGENT TASK: it shows on the
	// Kanban (Running, then Done), it can be stopped, and its narrative is the
	// report itself rather than a bare "ok". session_id in meta is what lets the
	// board deep-link into the work and the Stop button reach the live turn.
	h := runs.BeginGlobal(ctx, runs.KindPhoneAsk, sessionID, "Errand from your call", runs.SourceAgent)
	h.SetMeta(ctx, map[string]any{"session_id": sessionID, "via": "phone"})

	runCtx, cancel := context.WithTimeout(ctx, errandTimeout)
	final, runErr := m.errand(runCtx, sessionID, prompt)
	cancel()

	report := m.deliverErrandReport(sessionID, transcript, final, runErr)
	h.Finish(ctx, runErr, report)

	// The call-back loop: if he ASKED to be rung back, he gets rung back. This
	// is deterministic, not a hope: it was a sentence in the prompt, and a
	// sentence is exactly what this model drops. He asked, so code dials.
	if callbackRequested(transcript) {
		m.callBackWithResult(transcript, report, runErr)
	}
}

// deliverErrandReport is the guaranteed report back.
//
// It lands on surface="runs" as a run_outcome, exactly like a cron's outcome
// card, so it renders in "Surfaced by Jarvis" through the same row with no new
// widget (and never on the calls surface, which the dashboard routes to the
// Phone card and filters OUT of the inbox). It pushes his phone, because he
// asked for this by voice and is probably still driving. And it carries the
// session id, so one tap shows the whole conversation Jarvis had doing it.
// Returns the narrative it reported, which also becomes the run's summary on
// the Kanban card and the words Jarvis reads out if he rings back.
func (m *Manager) deliverErrandReport(sessionID, transcript, final string, runErr error) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ask := clip(strings.Join(strings.Fields(transcript), " "), 70)
	final = strings.TrimSpace(final)

	title := "Done: " + ask
	body := final
	reason := firstSentence(final)
	imp := 78

	if runErr != nil {
		// Never dress a failure as a result. He asked for something and did not
		// get it, and he needs to know that in plain words, with the next step.
		h := errs.Humanize(runErr)
		title = "I could not finish what you asked"
		reason = h.Summary
		body = strings.TrimSpace(h.Summary + "\n\n" + h.Impact + "\n\n" + h.Action)
		if final != "" {
			body = strings.TrimSpace(body + "\n\nHow far I got:\n" + final)
		}
		imp = 92
	} else if body == "" {
		// The run went green but said nothing. That is not a success worth
		// reporting as one: say so honestly rather than showing an empty card.
		title = "I ran what you asked, but I have nothing to show for it"
		reason = "The errand finished without producing a result. Worth a look."
		body = "You asked: " + ask + "\n\nThe run completed but Jarvis returned nothing, which usually means it stopped short. Open the conversation to see where it got to."
		imp = 88
	}

	if m.surface != nil {
		if _, err := m.surface.Upsert(ctx, &surface.Item{
			Surface:          "runs",
			Kind:             "run_outcome",
			Source:           "phone",
			ExternalID:       "phone-errand:" + sessionID,
			Title:            title,
			Body:             body,
			Importance:       &imp,
			ImportanceReason: reason,
			Metadata: map[string]any{
				"session_id": sessionID,
				"outcome":    outcomeWord(runErr),
				"asked":      ask,
				"via":        "phone",
			},
			Status: surface.StatusOpen,
		}); err != nil {
			log.Printf("phone: surfacing the errand report for session %s failed: %v", sessionID, err)
		}
	}

	// Always push: he commissioned this by voice, from a car, and the whole
	// point is that it is waiting for him when he arrives.
	if m.push != nil {
		m.push.Notify(ctx, push.Notification{
			Title: title,
			Body:  clip(reason, 200),
			Kind:  "phone_call",
			URL:   "/",
			Tag:   "phone-errand-" + sessionID,
		})
	}
	infoLog.Printf("phone: reported the errand from session %s (%s)", sessionID, outcomeWord(runErr))
	return body
}

func outcomeWord(err error) string {
	if err != nil {
		return "failed"
	}
	return "did_work"
}

// firstSentence is the row's one-line "why": Jarvis's own opening words, so the
// boss knows what happened without opening anything.
func firstSentence(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return ""
	}
	if i := strings.Index(s, ". "); i > 0 && i < 180 {
		return s[:i+1]
	}
	return clip(s, 180)
}

// callbackPhrases are the ways the boss asks to be rung back. Matched in code,
// deterministically, because "call me back when it's done" is a PROMISE and a
// promise kept only when the model remembers is not a promise.
var callbackPhrases = []string{
	"call me back", "call me when", "ring me back", "ring me when",
	"give me a call", "call me once", "call back when", "let me know by phone",
	"call me and tell me", "call me and let me know", "call my phone",
}

// callbackRequested reports whether he asked to be rung back with the result.
func callbackRequested(transcript string) bool {
	t := strings.ToLower(transcript)
	for _, p := range callbackPhrases {
		if strings.Contains(t, p) {
			return true
		}
	}
	return false
}

// callBackWithResult rings the boss on his own mobile and tells him how the
// errand went, in his assistant's voice.
//
// This is the whole promise of a phone assistant: he asks for something at 60mph,
// carries on driving, and Jarvis calls him back when it is done. The report is
// carried IN the brief, so the call agent (who knows nothing else) can actually
// deliver it.
func (m *Manager) callBackWithResult(transcript, report string, runErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cell := m.vaultBossCell(ctx)
	if cell == "" {
		// Loud: he asked to be called back and he will not be. Better a red line
		// in the log and a card he can see than a promise silently broken.
		log.Printf("phone: the boss asked to be called back but no cell is stored (Settings, Vault, Personal info); the report is on his dashboard only")
		return
	}

	outcome := "It went well."
	if runErr != nil {
		outcome = "It did NOT go to plan, and he needs to hear that plainly, not have it dressed up."
	}
	goal := "Call Mr. Kai back on his own mobile. He asked you to ring him with the result of an errand he " +
		"gave you by phone a short while ago, and he is very likely still driving.\n\n" +
		"What he asked for: " + clip(strings.Join(strings.Fields(transcript), " "), 400) + "\n\n" +
		"How it went: " + outcome + "\n\n" +
		"The result, which is what you are ringing to tell him:\n" + clip(report, 2000) + "\n\n" +
		"Greet him, tell him you are calling back about what he asked for, then give him the result " +
		"in plain speech, briefly, leading with what it means for him. He is driving, so do not read " +
		"him a document: give him the answer. If he asks for detail, give it. If he gives you a new " +
		"instruction, take it, confirm it back, and tell him it will be handled."

	brief := &Brief{
		Topic:     "calling you back",
		Kind:      "person",
		Name:      "Kai",
		To:        cell,
		Goal:      goal,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	briefID := uuid.NewString()
	if err := m.storeBrief(ctx, briefID, brief); err != nil {
		log.Printf("phone: could not store the call-back brief: %v", err)
		return
	}
	if _, err := m.createTwilioCall(ctx, cell, briefID); err != nil {
		log.Printf("phone: calling the boss back FAILED, he asked for it and will not get it: %v", err)
		return
	}
	infoLog.Printf("phone: calling the boss back with the result of his errand")
}
