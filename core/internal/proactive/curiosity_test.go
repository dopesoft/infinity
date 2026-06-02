package proactive

import "testing"

func TestLowConfidenceQuestionSubjectSuppressesBossProfile(t *testing.T) {
	subject, ok := lowConfidenceQuestionSubject("Name", "Khaya", "_self")
	if ok {
		t.Fatalf("boss-profile semantic memory should not become a curiosity question, got %q", subject)
	}
}

func TestLowConfidenceQuestionSubjectUsesContentForProfileLabels(t *testing.T) {
	subject, ok := lowConfidenceQuestionSubject("Working hours", "Mon-Fri 9am-7pm", "")
	if !ok {
		t.Fatal("non-profile memory with content should still be askable")
	}
	if subject != "Mon-Fri 9am-7pm" {
		t.Fatalf("generic field label should not be the question subject, got %q", subject)
	}
}

func TestLowConfidenceQuestionSubjectKeepsSpecificTitle(t *testing.T) {
	subject, ok := lowConfidenceQuestionSubject("Stripe partner intro", "Follow up next week", "")
	if !ok {
		t.Fatal("specific semantic memory should still be askable")
	}
	if subject != "Stripe partner intro" {
		t.Fatalf("specific title should be preserved, got %q", subject)
	}
}

// The graph cleanup left self/internal entity nodes with no backing memory, so
// the uncovered_mention detector asked "what's important about the boss / about
// the mem_curiosity_questions table / about the inbox-triage skill?" — pure
// noise. These must be suppressed; a genuine external entity must not be.
func TestShouldSuppressUncoveredMention(t *testing.T) {
	suppress := []struct{ kind, name string }{
		{"concept", "mem_curiosity_questions"}, // table name leaked into graph
		{"person", "boss"},                     // the boss himself
		{"person", "user"},                     // generic placeholder
		{"person", "kai@dopesoft.io"},          // his own email
		{"skill", "inbox-triage"},              // a skill name
		{"skill", "self-improve-from-finding"}, // a skill name
		{"concept", "malabie industries account"}, // his own account
		{"concept", "mr khaya account"},           // his own account
	}
	for _, c := range suppress {
		if !shouldSuppressUncoveredMention(c.kind, c.name) {
			t.Errorf("expected %s %q to be suppressed (noise), but it was kept", c.kind, c.name)
		}
	}
	// Genuine external entities the boss references SHOULD still be askable.
	keep := []struct{ kind, name string }{
		{"person", "Sarah Chen"},
		{"organization", "Acme Robotics"},
		{"concept", "Series A term sheet"},
	}
	for _, c := range keep {
		if shouldSuppressUncoveredMention(c.kind, c.name) {
			t.Errorf("expected genuine external %s %q to be kept, but it was suppressed", c.kind, c.name)
		}
	}
}
