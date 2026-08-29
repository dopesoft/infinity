package server

import "testing"

// The rule these lock in: a summary states what the numbers SAY. It never
// guesses at intent, and when the facts do not support a sentence it stays
// empty so the UI renders nothing rather than something plausible and wrong.
func TestParseNumstat(t *testing.T) {
	out := "12\t0\tcore/internal/server/search_api.go\n" +
		"34\t0\tcore/internal/server/search_api_test.go\n" +
		"2\t1\tcore/internal/server/server.go\n" +
		"0\t40\tcore/internal/old/dead.go\n" +
		"-\t-\tdocs/logo.png\n"

	files := parseNumstat(out)
	if len(files) != 5 {
		t.Fatalf("want 5 files, got %d", len(files))
	}

	if got := files[0].Summary; got != "New lines only, nothing removed." {
		t.Errorf("additions-only: got %q", got)
	}
	if !files[1].IsTest || files[1].Summary != "A test was added here." {
		t.Errorf("test file: isTest=%v summary=%q", files[1].IsTest, files[1].Summary)
	}
	// 2 added / 1 removed is a small mixed edit: the counts are already on the
	// row, so a vague sentence beside them would be furniture.
	if got := files[2].Summary; got != "" {
		t.Errorf("small mixed edit should say nothing, got %q", got)
	}
	if got := files[3].Summary; got != "Lines removed, nothing added." {
		t.Errorf("deletions-only: got %q", got)
	}
	// A binary file has no meaningful counts; saying "+0 -0" would be a lie.
	if got := files[4].Summary; got != "A binary file changed." {
		t.Errorf("binary: got %q", got)
	}
}

func TestParseNumstatIgnoresJunk(t *testing.T) {
	if got := parseNumstat("\n\nnot a numstat line\n"); len(got) != 0 {
		t.Errorf("want no files from junk, got %d", len(got))
	}
}

func TestLooksLikeTest(t *testing.T) {
	for _, p := range []string{"a/b_test.go", "x/foo.test.ts", "x/foo.spec.tsx", "t/test_thing.py"} {
		if !looksLikeTest(p) {
			t.Errorf("%s should read as a test", p)
		}
	}
	if looksLikeTest("core/internal/latest/main.go") {
		t.Error("a path containing 'test' inside a word is not a test file")
	}
}
