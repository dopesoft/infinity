package phone

import "testing"

func TestParseDigest(t *testing.T) {
	// The shape a real summary comes back in: outcome first, then tagged parts.
	raw := `Ariana called back about the voicemail and left a message for Mr. Kai.
MESSAGE: She thanks you for the voicemail and says she was moved by your words. She loves you.
READ: She sounded genuinely moved, close to tears at one point, and very warm. Nothing is wrong; this was purely affectionate.
URGENCY: low
CONTACT: Ariana Malaby | 929-310-0906
FOLLOWUP: Call Ariana back this evening.`

	d := parseDigest(raw)

	if d.Outcome != "Ariana called back about the voicemail and left a message for Mr. Kai." {
		t.Fatalf("outcome = %q", d.Outcome)
	}
	if !contains(d.Message, "moved by your words") || contains(d.Message, "READ:") {
		t.Fatalf("message bled into the next tag or lost content: %q", d.Message)
	}
	if !contains(d.Read, "close to tears") {
		t.Fatalf("read = %q", d.Read)
	}
	if d.Urgency != "low" {
		t.Fatalf("urgency = %q", d.Urgency)
	}
	if d.Contact != "Ariana Malaby | 929-310-0906" {
		t.Fatalf("contact = %q", d.Contact)
	}
	if d.Followup != "Call Ariana back this evening." {
		t.Fatalf("followup = %q", d.Followup)
	}
}

// The common case by far: a call with no message, no contact, no follow-up. It
// must parse to a clean outcome and nothing else, or every ordinary call would
// spam the boss's inbox with empty mail.
func TestParseDigestPlainCall(t *testing.T) {
	d := parseDigest("Your pizza will be ready in 20 minutes, at 2:50pm.")
	if d.Outcome != "Your pizza will be ready in 20 minutes, at 2:50pm." {
		t.Fatalf("outcome = %q", d.Outcome)
	}
	if d.Message != "" || d.Read != "" || d.Contact != "" || d.Followup != "" {
		t.Fatalf("a plain call must yield no message, read, contact or follow-up: %+v", d)
	}
}

// Tags can arrive in any order, and the model will not always give all of them.
func TestParseDigestOutOfOrderAndPartial(t *testing.T) {
	d := parseDigest("She rang.\nURGENCY: high\nMESSAGE: Get to the hospital, the baby is coming.")
	if !contains(d.Message, "baby is coming") {
		t.Fatalf("message = %q", d.Message)
	}
	if d.Urgency != "high" {
		t.Fatalf("urgency = %q", d.Urgency)
	}
	if !isUrgent(d.Urgency) || importanceFor(d.Urgency) != 95 {
		t.Fatal("a high-urgency message must outrank ordinary mail and announce itself")
	}
}

func TestImportanceFor(t *testing.T) {
	if importanceFor("") != 85 {
		t.Fatal("an unlabelled message is still the boss's mail, not an FYI")
	}
	if importanceFor("low") >= importanceFor("normal") || importanceFor("normal") >= importanceFor("high") {
		t.Fatal("urgency must order strictly")
	}
}

func TestParseContact(t *testing.T) {
	name, num := parseContact("Ariana Malaby | 929-310-0906")
	if name != "Ariana Malaby" || num != "929-310-0906" {
		t.Fatalf("parseContact = %q, %q", name, num)
	}
	// Half a contact is worse than none: it looks like a real entry and is not.
	if n, num := parseContact("Ariana Malaby"); n != "" || num != "" {
		t.Fatal("a contact line with no number must save nothing")
	}
}

// The boss asked not to lose any detail, so his mail carries what the caller
// ACTUALLY said, lifted from the transcript rather than trusted to a paraphrase.
func TestSpeakerLinesGivesVerbatim(t *testing.T) {
	lines := []string{
		"Jarvis: Good afternoon, Mr. Kai's office.",
		"Caller: I would like to leave a message.",
		"Jarvis: Certainly.",
		"Caller: Tell him I was moved by his words.",
	}
	got := speakerLines(lines, "Caller")
	if len(got) != 2 || got[0] != "I would like to leave a message." || got[1] != "Tell him I was moved by his words." {
		t.Fatalf("verbatim caller lines = %#v", got)
	}
	if len(speakerLines(lines, "Callee")) != 0 {
		t.Fatal("an inbound call has no callee lines")
	}
}
