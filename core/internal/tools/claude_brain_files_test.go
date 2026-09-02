package tools

import (
	"strings"
	"testing"
)

// Why: this path is how one of the boss's files gets a name on the box, and a
// name is the only handle the model has on it. A collision would hand it
// somebody else's file; a shell metacharacter in a filename he chose would end
// up inside a command.
func TestPlacedFilePath_IsSafeAndStable(t *testing.T) {
	// The same attachment always lands in the same place, so a resumed
	// session's earlier reference still resolves and the bytes cross the wire
	// once per box rather than once per turn.
	a := placedFilePath("6677ff03-b235-427d", "KhayaMalabie-2026-Resume.pdf")
	if a != placedFilePath("6677ff03-b235-427d", "KhayaMalabie-2026-Resume.pdf") {
		t.Fatal("the path for one file must be stable")
	}
	if !strings.HasPrefix(a, "/tmp/inf-attach/") {
		t.Fatalf("attachments live under the shared directory: %s", a)
	}
	if !strings.HasSuffix(a, "-KhayaMalabie-2026-Resume.pdf") {
		t.Fatalf("he should recognise the file by name: %s", a)
	}

	// Two different files with the SAME name must not collide, or the second
	// silently overwrites the first and he is answered about the wrong one.
	if placedFilePath("aaaa", "resume.pdf") == placedFilePath("bbbb", "resume.pdf") {
		t.Fatal("two different files must never share a path")
	}
}

// Why: he names files himself. Anything that could break out of the path or
// out of the shell command has to be flattened here, at the one place a name
// becomes a path.
func TestPlacedFilePath_DefusesAHostileName(t *testing.T) {
	nasty := map[string]string{
		"traversal":    "../../../etc/passwd",
		"command":      "a; rm -rf ~/Dev #.png",
		"quotes":       `he said "hi".png`,
		"only dots":    "..",
		"empty":        "   ",
		"subdirectory": "nested/deep/file.png",
	}
	for what, name := range nasty {
		got := placedFilePath("id1", name)
		rest := strings.TrimPrefix(got, "/tmp/inf-attach/")
		if strings.ContainsAny(rest, "/;\"'`$&|<>* \t\n") {
			t.Errorf("%s: %q produced an unsafe path segment %q", what, name, rest)
		}
		if strings.Contains(got, "..") {
			t.Errorf("%s: %q escaped the directory: %s", what, name, got)
		}
	}
}

// Why: the size is reported back to the boss when a file is too big to carry,
// so it has to read like a person said it, not like a byte count.
func TestHumanBytes_ReadsLikeAPerson(t *testing.T) {
	for in, want := range map[int64]string{
		512:                "512B",
		2048:               "2KB",
		5 << 20:            "5.0MB",
		maxPlacedFileBytes: "20.0MB",
	} {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
