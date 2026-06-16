package agent

import (
	"context"
	"testing"

	"github.com/dopesoft/infinity/core/internal/bridge"
)

func TestBackgroundLabelUsesBridgeWorkerPrefix(t *testing.T) {
	cloud := backgroundLabel("Fix the worker label", string(bridge.KindCloud))
	if want := "Cloud agent: Fix the worker label"; cloud != want {
		t.Fatalf("cloud label = %q, want %q", cloud, want)
	}

	mac := backgroundLabel("Fix the worker label", string(bridge.KindMac))
	if want := "Mac agent: Fix the worker label"; mac != want {
		t.Fatalf("mac label = %q, want %q", mac, want)
	}
}

func TestBackgroundLabelFallsBackForEmptyTask(t *testing.T) {
	cloud := backgroundLabel("", string(bridge.KindCloud))
	if want := "Cloud agent: background build"; cloud != want {
		t.Fatalf("cloud empty label = %q, want %q", cloud, want)
	}

	unknown := backgroundLabel("", "")
	if want := "background build"; unknown != want {
		t.Fatalf("unknown empty label = %q, want %q", unknown, want)
	}
}

func TestBackgroundWorkerAndBackendLabels(t *testing.T) {
	if got := backgroundWorkerLabel(string(bridge.KindCloud)); got != "Cloud agent" {
		t.Fatalf("cloud worker label = %q", got)
	}
	if got := backgroundBackendLabel(string(bridge.KindCloud)); got != "settings model" {
		t.Fatalf("cloud backend label = %q", got)
	}
	if got := backgroundWorkerLabel(string(bridge.KindMac)); got != "Mac agent" {
		t.Fatalf("mac worker label = %q", got)
	}
	if got := backgroundBackendLabel(string(bridge.KindMac)); got != "settings model" {
		t.Fatalf("mac backend label = %q", got)
	}
}

func TestBackgroundAgentActiveBridgeKind(t *testing.T) {
	a := &BackgroundAgent{Bridge: func(context.Context, string) bridge.Preference { return bridge.PrefCloud }}
	if got := a.activeBridgeKind(context.Background(), "chat-1"); got != string(bridge.KindCloud) {
		t.Fatalf("cloud preference picked %q", got)
	}

	a.Bridge = func(context.Context, string) bridge.Preference { return bridge.PrefMac }
	if got := a.activeBridgeKind(context.Background(), "chat-1"); got != string(bridge.KindMac) {
		t.Fatalf("mac preference picked %q", got)
	}

	a.Bridge = nil
	if got := a.activeBridgeKind(context.Background(), "chat-1"); got != "" {
		t.Fatalf("nil bridge picked %q", got)
	}
}
