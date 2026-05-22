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
