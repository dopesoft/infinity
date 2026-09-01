package llm

import "strings"

// Making a failed request safe to send again.
//
// Every brain in here rejects some requests outright, and the reasons have
// nothing to do with what the boss asked: a file block shaped the way one
// vendor wants and another does not, a tool call left in the history with no
// result under it after a crash or an interrupt, a continuation handle for a
// session that no longer exists. The vendor answers 400, the turn dies, and he
// reads an error about `file_id` when what he said was "here is my resume".
//
// The fix is not to learn each vendor's sentences. It is to be able to send
// the SAME question stripped back to what every brain accepts, so a request
// that cannot be served is retried in a form that can. That is what this does,
// and the loop calls it once before it ever shows him a failure.
//
// Nothing about the conversation is lost. An attachment folds into the
// labelled text rendering the providers already fall back to (name, type,
// size, extracted text, where the file is on disk), so the brain can still
// read it and can still open the file with its own tools.

// MakeSafe returns the messages reduced to what every provider accepts, and
// reports whether anything actually changed. A false return means the request
// was already minimal, so retrying it would be repeating it.
func MakeSafe(messages []Message) ([]Message, bool) {
	out, dropped := dropOrphanToolCalls(messages)
	out, folded := foldAttachments(out)
	return out, dropped || folded
}

// foldAttachments replaces native file blocks with their text rendering.
//
// This is the PDF that never reached the brain: a document block one vendor
// accepts and the next rejects. The text rendering carries the same content
// plus the path, and every Chat Completions vendor takes it.
func foldAttachments(messages []Message) ([]Message, bool) {
	changed := false
	out := make([]Message, 0, len(messages))
	for _, m := range messages {
		if len(m.Attachments) == 0 {
			out = append(out, m)
			continue
		}
		var b strings.Builder
		for _, a := range m.Attachments {
			if block := strings.TrimSpace(a.TextBlock()); block != "" {
				b.WriteString(block)
				b.WriteString("\n\n")
			}
		}
		m.Content = strings.TrimSpace(b.String() + m.Content)
		m.Attachments = nil
		changed = true
		out = append(out, m)
	}
	return out, changed
}

// dropOrphanToolCalls removes tool calls with no result and results with no
// call.
//
// A turn that is interrupted, or whose process dies mid-flight, leaves the
// history with a call and no output under it. Every vendor refuses the whole
// request from then on ("No tool output found for function call …"), so the
// conversation is poisoned permanently: not just that turn, every turn after
// it, until somebody notices. Pairing them off makes the history sendable
// again, and the turn it belonged to is over either way.
func dropOrphanToolCalls(messages []Message) ([]Message, bool) {
	answered := map[string]bool{}
	called := map[string]bool{}
	for _, m := range messages {
		if m.Role == RoleTool && m.ToolCallID != "" {
			answered[m.ToolCallID] = true
		}
		for _, c := range m.ToolCalls {
			called[c.ID] = true
		}
	}

	changed := false
	out := make([]Message, 0, len(messages))
	for _, m := range messages {
		// A result whose call is gone.
		if m.Role == RoleTool && m.ToolCallID != "" && !called[m.ToolCallID] {
			changed = true
			continue
		}
		if len(m.ToolCalls) > 0 {
			kept := m.ToolCalls[:0:0]
			for _, c := range m.ToolCalls {
				if answered[c.ID] {
					kept = append(kept, c)
				}
			}
			if len(kept) != len(m.ToolCalls) {
				changed = true
				m.ToolCalls = kept
				// An assistant message that was ONLY the unanswered call has
				// nothing left to say; dropping it keeps the transcript
				// readable instead of leaving a blank turn in the middle.
				if len(kept) == 0 && strings.TrimSpace(m.Content) == "" {
					continue
				}
			}
		}
		out = append(out, m)
	}
	return out, changed
}
