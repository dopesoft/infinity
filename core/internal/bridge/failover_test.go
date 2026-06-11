package bridge

import (
	"context"
	"errors"
	"testing"
)

// fakeBridge is a scriptable Bridge for exercising Router.Call's failover. Each
// call returns the next scripted (status, ok); health is fixed.
type fakeBridge struct {
	name    Kind
	healthy bool
	status  int
	ok      bool
	body    []byte // optional scripted response body; defaults to the name
	calls   int
}

func (f *fakeBridge) Name() Kind                { return f.name }
func (f *fakeBridge) BaseURL() string           { return string(f.name) }
func (f *fakeBridge) Health(context.Context) bool { return f.healthy }
func (f *fakeBridge) respond() ([]byte, int, bool) {
	f.calls++
	if f.body != nil {
		return f.body, f.status, f.ok
	}
	return []byte(string(f.name)), f.status, f.ok
}
func (f *fakeBridge) Get(context.Context, string) ([]byte, int, bool) {
	return f.respond()
}
func (f *fakeBridge) Post(context.Context, string, any) ([]byte, int, bool) {
	return f.respond()
}

func call(r *Router) (Bridge, int, bool, error) {
	served, _, status, failedOver, err := r.Call(context.Background(), PrefAuto, func(b Bridge) ([]byte, int, bool) {
		return b.Get(context.Background(), "/x")
	})
	return served, status, failedOver, err
}

// A Mac 5xx must fail over to the healthy cloud — the night-after-night bug.
func TestCall_MacFailsOverToCloud(t *testing.T) {
	mac := &fakeBridge{name: KindMac, healthy: true, status: 500, ok: true}
	cloud := &fakeBridge{name: KindCloud, healthy: true, status: 200, ok: true}
	r := NewRouter(mac, cloud)

	served, status, failedOver, err := call(r)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if served.Name() != KindCloud {
		t.Fatalf("expected cloud to serve after Mac 5xx, got %s", served.Name())
	}
	if !failedOver {
		t.Fatal("expected failedOver=true")
	}
	if status != 200 {
		t.Fatalf("expected status 200 from cloud, got %d", status)
	}
}

// A 4xx is a command/param result, not a bridge outage — no failover, no retry.
func TestCall_4xxDoesNotFailOver(t *testing.T) {
	mac := &fakeBridge{name: KindMac, healthy: true, status: 400, ok: true}
	cloud := &fakeBridge{name: KindCloud, healthy: true, status: 200, ok: true}
	r := NewRouter(mac, cloud)

	served, status, failedOver, err := call(r)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if served.Name() != KindMac || status != 400 || failedOver {
		t.Fatalf("expected mac 400 no-failover, got served=%s status=%d failedOver=%v", served.Name(), status, failedOver)
	}
	if cloud.calls != 0 {
		t.Fatalf("cloud must not be called on a 4xx, got %d calls", cloud.calls)
	}
}

// Both bridges down at the bridge level → ErrBothBridgesDown.
func TestCall_BothDown(t *testing.T) {
	mac := &fakeBridge{name: KindMac, healthy: true, status: 502, ok: true}
	cloud := &fakeBridge{name: KindCloud, healthy: true, status: 0, ok: false} // transport fail
	r := NewRouter(mac, cloud)

	_, _, _, err := call(r)
	if !errors.Is(err, ErrBothBridgesDown) {
		t.Fatalf("expected ErrBothBridgesDown, got %v", err)
	}
}

// A contract-less 404 (a tunnel/proxy that doesn't serve the route — e.g. the
// Mac tunnel routing /sse to the MCP proxy but never wiring /bash) is a
// bridge-level miss: the alternate must serve. This was the bash_run 404 that
// stalled the nightly self-improve run instead of failing over to cloud.
func TestCall_RouteMiss404FailsOver(t *testing.T) {
	mac := &fakeBridge{name: KindMac, healthy: true, status: 404, ok: true, body: []byte("404 page not found")}
	cloud := &fakeBridge{name: KindCloud, healthy: true, status: 200, ok: true}
	r := NewRouter(mac, cloud)

	served, status, failedOver, err := call(r)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if served.Name() != KindCloud || !failedOver || status != 200 {
		t.Fatalf("expected cloud to serve after contract-less Mac 404, got served=%s status=%d failedOver=%v",
			served.Name(), status, failedOver)
	}
}

// A 404 carrying the bridge's {"error":...} JSON contract is a real command
// result (e.g. the cloud workspace's fs/read missing-file 404) — never retried
// on the other bridge, or a missing file would silently read off the wrong box.
func TestCall_Json404DoesNotFailOver(t *testing.T) {
	mac := &fakeBridge{name: KindMac, healthy: true, status: 404, ok: true, body: []byte(`{"error":"not found"}`)}
	cloud := &fakeBridge{name: KindCloud, healthy: true, status: 200, ok: true}
	r := NewRouter(mac, cloud)

	served, status, failedOver, err := call(r)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if served.Name() != KindMac || status != 404 || failedOver {
		t.Fatalf("expected mac JSON-404 no-failover, got served=%s status=%d failedOver=%v", served.Name(), status, failedOver)
	}
	if cloud.calls != 0 {
		t.Fatalf("cloud must not be called on a contract 404, got %d calls", cloud.calls)
	}
}

// NormalizePath rewrites well-known path shapes for the bridge that actually
// serves the call and passes unknown shapes through untouched.
func TestNormalizePath(t *testing.T) {
	mac := &fakeBridge{name: KindMac}
	cloud := &fakeBridge{name: KindCloud}
	cases := []struct {
		b    Bridge
		in   string
		want string
	}{
		// Mac → cloud (the failover that produced /workspace/~/Dev/infinity)
		{cloud, "~/Dev/infinity", "/workspace/infinity"},
		{cloud, "~/Dev/infinity/core/internal", "/workspace/infinity/core/internal"},
		{cloud, "~/Dev/buttercut", "/workspace/projects/buttercut"},
		{cloud, "~", "/workspace"},
		{cloud, "~/Dev", "/workspace"},
		{cloud, "~/notes.md", "/workspace/notes.md"},
		{cloud, "/Users/n0m4d/Dev/infinity", "/workspace/infinity"},
		{cloud, "/workspace/infinity", "/workspace/infinity"}, // already native
		{cloud, "relative/path", "relative/path"},
		{cloud, "", ""},
		// Cloud → Mac
		{mac, "/workspace/infinity", "~/Dev/infinity"},
		{mac, "/workspace/infinity/studio", "~/Dev/infinity/studio"},
		{mac, "/workspace/projects/buttercut", "~/Dev/buttercut"},
		{mac, "/workspace", "~"},
		{mac, "/workspace/notes.md", "~/notes.md"},
		{mac, "~/Dev/infinity", "~/Dev/infinity"}, // already native
		{mac, "relative/path", "relative/path"},
	}
	for _, c := range cases {
		if got := NormalizePath(c.b, c.in); got != c.want {
			t.Errorf("NormalizePath(%s, %q) = %q, want %q", c.b.Name(), c.in, got, c.want)
		}
	}
}

// Healthy primary → no failover.
func TestCall_HealthyPrimary(t *testing.T) {
	mac := &fakeBridge{name: KindMac, healthy: true, status: 200, ok: true}
	cloud := &fakeBridge{name: KindCloud, healthy: true, status: 200, ok: true}
	r := NewRouter(mac, cloud)

	served, _, failedOver, err := call(r)
	if err != nil || served.Name() != KindMac || failedOver {
		t.Fatalf("expected mac served no-failover, got served=%v failedOver=%v err=%v", served, failedOver, err)
	}
	if cloud.calls != 0 {
		t.Fatalf("cloud must not be called when Mac is healthy, got %d", cloud.calls)
	}
}
