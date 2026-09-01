package httpx

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheckTargetRefusesTheInsides(t *testing.T) {
	// Each of these is somewhere a link handed to Jarvis must never reach. The
	// spellings matter as much as the addresses: every one of the alternate
	// forms below is a documented way past a guard that only string-matches the
	// obvious version.
	for _, tc := range []struct{ url, why string }{
		{"http://169.254.169.254/latest/meta-data/", "cloud metadata, the classic target"},
		{"http://169.254.170.2/v2/credentials", "ECS task credentials"},
		{"http://100.100.100.200/latest/meta-data/", "Alibaba metadata, inside carrier-grade NAT"},
		{"http://127.0.0.1:8080/readyz", "core talking to itself"},
		{"http://localhost./admin", "trailing dot resolves the same and dodges an equality check"},
		{"http://10.0.0.5/", "private range"},
		{"http://192.168.1.1/", "private range"},
		{"http://[::1]/", "IPv6 loopback"},
		{"http://[::ffff:169.254.169.254]/", "IPv4-mapped IPv6 spelling of metadata"},
		{"http://[64:ff9b::a9fe:a9fe]/", "NAT64-embedded metadata address"},
		{"http://honcho.railway.internal:8000/", "our own service network"},
		{"http://kubernetes.default.svc/", "cluster API"},
		{"http://metadata.goog/", "the short GCP name"},
		{"file:///etc/passwd", "not a web address at all"},
	} {
		if err := CheckTarget(tc.url); err == nil {
			t.Errorf("allowed %s (%s) — this is an SSRF hole", tc.url, tc.why)
		}
	}
}

func TestCheckTargetAllowsTheOpenInternet(t *testing.T) {
	// The counterweight. Jarvis fetching the web is the product, not the
	// threat; a guard that blocks ordinary sites would be reverted within a day
	// and take the real protection with it.
	for _, u := range []string{
		"https://example.com/page",
		"http://93.184.216.34/",
		"https://api.stripe.com/v1/charges",
		"https://docs.anthropic.com/en/api",
		"https://[2606:2800:220:1:248:1893:25c8:1946]/",
	} {
		if err := CheckTarget(u); err != nil {
			t.Errorf("refused a legitimate address %s: %v", u, err)
		}
	}
}

func TestBlockedErrorReadsLikeASentence(t *testing.T) {
	// This string reaches the model as a tool result and, through it, the boss.
	// "blocked by network policy" tells him nothing he can act on.
	err := CheckTarget("http://169.254.169.254/")
	var be *BlockedError
	if !errors.As(err, &be) {
		t.Fatalf("want a *BlockedError, got %T", err)
	}
	if !strings.Contains(be.Error(), "refused to connect") || !strings.Contains(be.Error(), "credential endpoints") {
		t.Errorf("error does not explain itself in plain words: %q", be.Error())
	}
}

func TestGuardedClientRefusesAfterResolution(t *testing.T) {
	// The DNS-rebinding hole: a hostname that passes every string check and
	// then resolves somewhere private. Nothing a URL check can do about this,
	// which is the whole reason the guard sits at the dialer.
	//
	// localtest.me and friends are third-party DNS, so we resolve a literal
	// through the same path instead: the client must refuse a loopback server
	// that a plain client would happily reach.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("secrets"))
	}))
	defer srv.Close()

	if _, err := GuardedClient("test", 5*time.Second).Get(srv.URL); err == nil {
		t.Fatal("guarded client reached a loopback server; the dialer guard is not wired")
	}

	// Sanity: the same server IS reachable without the guard, so the test is
	// proving the guard works rather than that the server is down.
	if _, err := (&http.Client{Timeout: 5 * time.Second}).Get(srv.URL); err != nil {
		t.Fatalf("unguarded client could not reach the test server: %v", err)
	}
}

func TestGuardedClientRefusesARedirectIntoThePrivateNetwork(t *testing.T) {
	// The bypass that a URL check cannot close: a clean public-looking first
	// hop that redirects into the metadata endpoint. Before the guard, Go
	// followed up to ten hops with nothing looking at them.
	target := "http://169.254.169.254/latest/meta-data/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	}))
	defer srv.Close()

	// Dial the redirector directly (it is loopback, which the guard also
	// refuses) — so assert on the redirect policy itself instead.
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	via := []*http.Request{req}
	next, _ := http.NewRequest(http.MethodGet, target, nil)

	c := GuardedClient("test", 5*time.Second)
	if err := c.CheckRedirect(next, via); err == nil {
		t.Fatal("a redirect into cloud metadata was allowed")
	}
}

func TestNat64EmbeddedOnlyMatchesTheWellKnownPrefix(t *testing.T) {
	// Guard against over-reach: if this matched more broadly it would start
	// mangling ordinary IPv6 addresses into unrelated IPv4 ones and refusing
	// legitimate sites for reasons nobody could explain.
	if got := nat64Embedded(net.ParseIP("2606:2800:220:1::1")); got != nil {
		t.Errorf("ordinary IPv6 treated as NAT64: %v", got)
	}
	got := nat64Embedded(net.ParseIP("64:ff9b::a9fe:a9fe"))
	if got == nil || got.String() != "169.254.169.254" {
		t.Errorf("NAT64 extraction = %v, want 169.254.169.254", got)
	}
}
