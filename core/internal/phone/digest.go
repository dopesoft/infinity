// digest.go — what a finished call yields, split into its parts.
//
// The summarizer answers one prompt and tags each part of its answer. Go parses
// the tags and ALWAYS acts on each one: the outcome leads the call card, the
// message becomes the boss's mail, the read becomes Jarvis's own take on whoever
// was on the line, the urgency sets how loudly it lands, the contact goes into
// the phone book, the follow-up becomes a task.
//
// The split is the Rule #1b split. Judgment (is there a message, what did she
// actually say, did she sound frantic or delighted, is this urgent) is the only
// thing the model is asked for. Delivery (persist it, surface it, push it, save
// the contact) is code, so it cannot be forgotten on a run where the model is
// having an off day.
package phone

import "strings"

// digestTags are the labels the summarizer is told to emit. Order matters only
// for splitting: everything before the FIRST tag is the outcome.
var digestTags = []string{"MESSAGE:", "READ:", "URGENCY:", "CONTACT:", "FOLLOWUP:"}

// callDigest is one finished call, parsed.
type callDigest struct {
	Outcome  string // the 1-2 sentence result, leads the call card
	Message  string // what they asked Jarvis to pass on, in their words
	Read     string // Jarvis's read on them: mood, manner, whether it sounded urgent
	Urgency  string // low | normal | high
	Contact  string // "Name | +19293100906" for someone worth remembering
	Followup string // an action the boss now owes
}

// parseDigest splits a tagged summary into its parts. Tolerant by design: a
// missing tag is simply an empty field, never an error, because a call that
// produced no message and no follow-up is the normal case.
func parseDigest(s string) callDigest {
	var d callDigest
	// Where each tag starts (-1 = absent).
	at := map[string]int{}
	first := len(s)
	for _, tag := range digestTags {
		i := strings.Index(s, tag)
		at[tag] = i
		if i >= 0 && i < first {
			first = i
		}
	}
	d.Outcome = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s[:first]), "\n -"))

	// Each tag's value runs until the next tag that appears AFTER it.
	value := func(tag string) string {
		i := at[tag]
		if i < 0 {
			return ""
		}
		start := i + len(tag)
		end := len(s)
		for _, other := range digestTags {
			if j := at[other]; j > i && j < end {
				end = j
			}
		}
		return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s[start:end]), "\n -"))
	}

	d.Message = value("MESSAGE:")
	d.Read = value("READ:")
	d.Urgency = strings.ToLower(value("URGENCY:"))
	d.Contact = value("CONTACT:")
	d.Followup = value("FOLLOWUP:")
	return d
}

// importanceFor maps the model's read of urgency onto how loudly the message
// lands on the dashboard. Deterministic: the model says "high", Go decides what
// high MEANS, so urgency can never be argued into or out of a push.
func importanceFor(urgency string) int {
	switch strings.ToLower(strings.TrimSpace(urgency)) {
	case "high", "urgent", "emergency":
		return 95
	case "low":
		return 75
	default:
		return 85
	}
}

// isUrgent reports whether a message should announce itself as urgent.
func isUrgent(urgency string) bool {
	switch strings.ToLower(strings.TrimSpace(urgency)) {
	case "high", "urgent", "emergency":
		return true
	}
	return false
}

// parseContact splits the summarizer's "Name | number" contact line. Returns
// empty strings when it is not usable, and the caller then saves nothing:
// half a contact is worse than none, because it looks like a real entry.
func parseContact(line string) (name, number string) {
	parts := strings.SplitN(line, "|", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// speakerLines pulls one side of the conversation out of the transcript, in
// order, with the "Speaker: " prefix stripped.
//
// This is how the boss gets his message VERBATIM. The summarizer's rendition is
// a rendition, however careful the prompt is, and he asked not to lose details.
// The transcript is what was actually said, and it is right here in memory, so
// there is no reason to make him trust a paraphrase.
func speakerLines(lines []string, speaker string) []string {
	prefix := speaker + ": "
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			if t := strings.TrimSpace(strings.TrimPrefix(l, prefix)); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}
