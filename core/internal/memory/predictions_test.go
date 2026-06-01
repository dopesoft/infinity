package memory

import (
	"strings"
	"testing"
)

// TestClassifyActual_EnvelopeAnchoring encodes WHY signal scanning is anchored
// to the result envelope: a SUCCESSFUL data fetch whose body merely CONTAINS
// words like "error", "unauthorized", or "not found" must classify OutcomeOK,
// not OutcomeError. The deep-content substring scan misclassified compact_context
// summaries and composio Gmail fetches as failures (surprise 0.9), producing the
// un-dismissable high-surprise curiosity spam. A genuine error wrapper at the
// envelope must still classify OutcomeError.
func TestClassifyActual_EnvelopeAnchoring(t *testing.T) {
	bigBody := func(mid string) string {
		// 1KB of benign content with the signal buried in the middle, well
		// past the 200-char envelope on both ends.
		pad := strings.Repeat("the quarterly report looks fine and on track. ", 12)
		return `{"id":"abc","subject":"weekly digest","body":"` + pad + mid + pad + `"}`
	}
	cases := []struct {
		name   string
		actual string
		want   OutcomeClass
	}{
		{"email body containing 'error' is OK", bigBody("we hit an error last week but resolved it"), OutcomeOK},
		{"summary containing 'unauthorized' is OK", bigBody("the unauthorized access attempt was blocked"), OutcomeOK},
		{"recall result containing 'not found' is OK", bigBody("the file was not found earlier"), OutcomeOK},
		{"genuine error prefix is Error", "Error: tool failed to connect", OutcomeError},
		{"json error envelope is Error", `{"error":"unauthorized","status":401}`, OutcomeError},
		{"empty json is Empty", "{}", OutcomeEmpty},
	}
	for _, c := range cases {
		if got := ClassifyActual(c.actual); got != c.want {
			t.Errorf("%s: ClassifyActual = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestSurpriseFor_OutcomeClass encodes WHY surprise exists: it must score a
// successful tool call as low-surprise and a genuine failure as high-surprise.
// The previous Jaccard-on-text implementation scored ~1.0 surprise on every
// successful JSON-returning call, which silently poisoned Gym training-example
// mining and the curiosity high_surprise detector. These cases would all have
// failed under that implementation - that's the regression they guard.
func TestSurpriseFor_OutcomeClass(t *testing.T) {
	cases := []struct {
		name        string
		expected    string
		actual      string
		wantMatched bool
		maxSurprise float64 // surprise must be <= this
		minSurprise float64 // surprise must be >= this
	}{
		{
			name:        "success predicted, JSON success returned -> low surprise",
			expected:    "expect surface_item to return a usable result",
			actual:      `{"id":"528827b0-742b-4948-8161-14fac3f69abc","ok":true}`,
			wantMatched: true,
			maxSurprise: 0.2,
		},
		{
			name:        "success predicted, notify success -> low surprise",
			expected:    "expect notify to return a usable result",
			actual:      `{"channel":"surface","id":"97f78b24-0d95-4f00-bb11-0001"}`,
			wantMatched: true,
			maxSurprise: 0.2,
		},
		{
			name:        "success predicted, hard error -> high surprise",
			expected:    "expect composio__GMAIL_FETCH_EMAILS to return 2xx response with non-empty body",
			actual:      "ERROR: composio execute GMAIL_FETCH_EMAILS 401 invalid api key",
			wantMatched: false,
			minSurprise: 0.8,
		},
		{
			name:        "success predicted, empty result -> mid surprise",
			expected:    "expect mem_list to return a usable result",
			actual:      `{"count":0,"items":[]}`,
			wantMatched: false,
			minSurprise: 0.4,
			maxSurprise: 0.6,
		},
		{
			name:        "error predicted, error happened -> low surprise",
			expected:    "expect this to fail with an error",
			actual:      "error: permission denied",
			wantMatched: true,
			maxSurprise: 0.3,
		},
		{
			name:        "error predicted, success happened -> notable surprise",
			expected:    "expect this to fail",
			actual:      `{"ok":true,"result":"done"}`,
			wantMatched: false,
			minSurprise: 0.6,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matched, surprise := SurpriseFor(tc.expected, tc.actual)
			if matched != tc.wantMatched {
				t.Errorf("matched = %v, want %v", matched, tc.wantMatched)
			}
			if tc.maxSurprise > 0 && surprise > tc.maxSurprise {
				t.Errorf("surprise = %.2f, want <= %.2f", surprise, tc.maxSurprise)
			}
			if tc.minSurprise > 0 && surprise < tc.minSurprise {
				t.Errorf("surprise = %.2f, want >= %.2f", surprise, tc.minSurprise)
			}
		})
	}
}
