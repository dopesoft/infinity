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
	calls   int
}

func (f *fakeBridge) Name() Kind                { return f.name }
func (f *fakeBridge) BaseURL() string           { return string(f.name) }
func (f *fakeBridge) Health(context.Context) bool { return f.healthy }
func (f *fakeBridge) Get(context.Context, string) ([]byte, int, bool) {
	f.calls++
	return []byte(string(f.name)), f.status, f.ok
}
func (f *fakeBridge) Post(context.Context, string, any) ([]byte, int, bool) {
	f.calls++
	return []byte(string(f.name)), f.status, f.ok
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
