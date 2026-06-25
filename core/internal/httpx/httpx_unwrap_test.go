package httpx

import (
	"net/http"
	"testing"
)

type nopRecorder struct{}

func (nopRecorder) Record(Failure) {}

// TestUnwrapAfterInstallDefault reproduces the boot panic that crash-looped core
// when the instrumentation first shipped: once InstallDefault wraps
// http.DefaultTransport, a bare `http.DefaultTransport.(*http.Transport)` panics
// because the value is now *httpx.roundTripper. Unwrap must see through the
// wrapper and hand back the real *http.Transport so callers can Clone() it.
func TestUnwrapAfterInstallDefault(t *testing.T) {
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })

	InstallDefault(nopRecorder{})

	// After wrapping, the default transport is NOT an *http.Transport — the bare
	// assertion mcp.go used to do would panic here.
	if _, ok := http.DefaultTransport.(*http.Transport); ok {
		t.Fatal("expected DefaultTransport to be wrapped (not *http.Transport) after InstallDefault")
	}

	// Unwrap recovers the underlying *http.Transport so it can be cloned.
	base := Unwrap(http.DefaultTransport)
	tr, ok := base.(*http.Transport)
	if !ok {
		t.Fatalf("Unwrap should return *http.Transport, got %T", base)
	}
	if clone := tr.Clone(); clone == nil { // the exact op mcp.go performs
		t.Fatal("Clone() of unwrapped transport returned nil")
	}
}

// TestUnwrapPassthrough: a plain transport (or any non-wrapper) comes back as-is.
func TestUnwrapPassthrough(t *testing.T) {
	tr := &http.Transport{}
	if got := Unwrap(tr); got != tr {
		t.Fatalf("Unwrap of a bare transport should return it unchanged, got %T", got)
	}
}
