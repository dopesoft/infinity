package server

import "testing"

// The 2026-08-25 report: a document Jarvis generated on the cloud workspace
// (/workspace/artifacts/…) was opened from a MAC session. Routing by session
// preference sent the read to the Mac, which has no /workspace, so Studio
// showed "mac bridge unreachable?" while the Mac sat there awake and healthy.
//
// The invariant this encodes: a path decides its own host. /workspace exists
// ONLY on the cloud bridge, so it must never be handed to the Mac path
// regardless of which bridge the session prefers.
func TestIsCloudWorkspacePath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/workspace/artifacts/weekly-ai-agent-advancement-brief-2026-08-17.docx", true},
		{"/workspace", true},
		{"/workspace/", true},
		{"/Users/n0m4d/Dev/infinity/core/internal/server/canvas_api.go", false},
		{"", false},
		// Must not match a Mac path that merely CONTAINS the word, or a
		// sibling directory that shares the prefix — both live on the Mac.
		{"/Users/n0m4d/workspace/notes.md", false},
		{"/workspace-backup/old.docx", false},
	}
	for _, c := range cases {
		if got := isCloudWorkspacePath(c.path); got != c.want {
			t.Errorf("isCloudWorkspacePath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
