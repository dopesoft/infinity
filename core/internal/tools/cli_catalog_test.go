package tools

import (
	"context"
	"strings"
	"testing"
)

type fakeCatalog struct{ bins []string }

func (f fakeCatalog) CloudCLIBinaries(context.Context) []string { return f.bins }
func (f fakeCatalog) CloudEnvPrelude() string {
	return "source /workspace/.jarvis/env.sh 2>/dev/null && "
}

func withCatalog(t *testing.T, c CLICatalog) {
	t.Helper()
	AttachCLICatalog(c)
	t.Cleanup(func() { AttachCLICatalog(nil) })
}

// The whole point: a bare `yt-dlp <url>` — exactly what a recipe writes, with
// no `source`, no bridge flag — must be recognised as cloud-resident.
func TestCloudCLICommandRecognisesBareInvocation(t *testing.T) {
	withCatalog(t, fakeCatalog{bins: []string{"yt-dlp", "ffmpeg"}})
	pre, ok := CloudCLICommand(context.Background(), "yt-dlp --skip-download --write-auto-subs https://youtu.be/abc")
	if !ok {
		t.Fatal("bare yt-dlp invocation must be recognised as a cloud CLI")
	}
	if !strings.Contains(pre, "env.sh") {
		t.Fatalf("prelude must source the workspace env, got %q", pre)
	}
}

func TestCloudCLICommandCases(t *testing.T) {
	withCatalog(t, fakeCatalog{bins: []string{"yt-dlp", "ffmpeg"}})
	ctx := context.Background()

	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		{"binary after a source prefix", "source /workspace/.jarvis/env.sh && yt-dlp --version", true},
		{"binary later in a pipeline", "echo hi | ffmpeg -i - out.mp4", true},
		{"absolute path to the binary", "/workspace/.jarvis/.local/bin/yt-dlp --version", true},
		{"env assignment before it", "FOO=bar yt-dlp --version", true},
		{"sudo before it", "sudo ffmpeg -version", true},
		// The command word is what counts. A binary named inside a string is
		// not an invocation — otherwise `git commit -m "fix yt-dlp"` would get
		// force-routed to the cloud and never touch the boss's repo.
		{"named only inside an argument", `echo "install yt-dlp first"`, false},
		{"named in a commit message", `git commit -m "fix yt-dlp path"`, false},
		{"unrelated command", "ls -la", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := CloudCLICommand(ctx, tc.cmd)
			if ok != tc.want {
				t.Fatalf("CloudCLICommand(%q) = %v, want %v", tc.cmd, ok, tc.want)
			}
		})
	}
}

// Sourcing twice would clobber PATH ordering; a command that already sources
// the env is left alone.
func TestCloudCLICommandIsIdempotent(t *testing.T) {
	withCatalog(t, fakeCatalog{bins: []string{"yt-dlp"}})
	pre, ok := CloudCLICommand(context.Background(), "source /workspace/.jarvis/env.sh 2>/dev/null && yt-dlp --version")
	if !ok {
		t.Fatal("still a cloud CLI command")
	}
	if pre != "" {
		t.Fatalf("already-sourced command must not be re-prefixed, got %q", pre)
	}
}

// With no catalog attached, nothing changes — every existing bash_run keeps its
// old routing. This is the safety property that lets the seam ship dark.
func TestCloudCLICommandInertWithoutCatalog(t *testing.T) {
	AttachCLICatalog(nil)
	if _, ok := CloudCLICommand(context.Background(), "yt-dlp --version"); ok {
		t.Fatal("no catalog attached must mean no rerouting")
	}
}
