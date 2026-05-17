package skills

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestContainerCommandRewritesImplPathInsideWorkspace(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "tmp", "skill")
	cmd := []string{"python3", filepath.Join(root, "impl.py"), "{}"}
	got := containerCommand(cmd, root)
	want := []string{"python3", "/workspace/impl.py", "{}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("containerCommand = %#v, want %#v", got, want)
	}
}

func TestNetworkModeDefaultsToNone(t *testing.T) {
	if got := networkMode(SandboxOpts{}); got != "none" {
		t.Fatalf("networkMode empty = %q", got)
	}
	if got := networkMode(SandboxOpts{NetworkAllow: []string{"api.example.com"}}); got != "bridge" {
		t.Fatalf("networkMode allowlist = %q", got)
	}
}
