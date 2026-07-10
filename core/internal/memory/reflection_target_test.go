package memory

import "testing"

// looksLikeCodeTarget is the deterministic floor that stops a failed-session
// self-critique from becoming a boss-facing "code issue" card unless it names
// an addressable file. Each rejected case below is a real false-alarm shape
// the boss saw in his inbox.
func TestLooksLikeCodeTarget(t *testing.T) {
	accept := []string{
		"core/internal/memory/reflection.go",
		"openai_oauth.go",
		"core/db/migrations/158_verify_revert_deterministic_only.sql",
		"studio/components/dashboard/SurfaceCard.tsx",
		"docker/workspace/env.sh",
	}
	for _, h := range accept {
		if !looksLikeCodeTarget(h) {
			t.Errorf("expected %q to be a valid code target", h)
		}
	}

	reject := []string{
		"",
		"media_job",                                  // a tool name, not a file — the 2026-07-10 noise card
		"classifyOutcome",                            // a bare symbol is too ambiguous to route a fix to
		"bridge routing / bash_run failover",         // "A / B" phrase, not a path
		"delegate model routing / Codex auth compat", // phrase
		"the post-deploy-verify flow",                // behavior description
	}
	for _, h := range reject {
		if looksLikeCodeTarget(h) {
			t.Errorf("expected %q to be rejected as a non-target", h)
		}
	}
}
