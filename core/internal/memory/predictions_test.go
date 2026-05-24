package memory

import "testing"

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
