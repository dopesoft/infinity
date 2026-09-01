package httpx

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// The SSRF boundary: Jarvis must never be talkable into fetching his own
// insides.
//
// The threat is specific and live. Jarvis reads email, web pages and connector
// payloads, any of which can contain a link. A prompt-injected turn that gets
// him to fetch http://169.254.169.254/ (the cloud metadata endpoint) or
// http://honcho.railway.internal/ is using him as a key to his own house.
//
// WHY A DIALER AND NOT A URL CHECK. http_fetch already validated the URL
// string, and that guard is bypassable two ways that no string check can close:
//
//   - Redirects. Nothing in the tree set CheckRedirect, so Go followed up to 10
//     hops silently. http://attacker.example/x returning 302 to
//     http://169.254.169.254/ walked straight through a clean-looking URL.
//   - DNS rebinding. A hostname is only string-matched, so evil.example
//     resolving to 10.0.0.5 passed. Even resolving it ourselves first would
//     leave a gap, because the dialer resolves again when it connects.
//
// ControlContext runs AFTER resolution and immediately BEFORE connect, on the
// address actually being dialled, once per hop. Both holes close by
// construction rather than by remembering to re-check.
//
// WHY THIS IS NOT INSTALLED ON http.DefaultTransport. It was the obvious move
// and it is wrong: core legitimately dials at least six PRIVATE addresses on
// the Railway network (gepa, honcho, workspace, camofox, the browser sidecar,
// and its own /readyz), and Railway's private network is ULA IPv6, which is
// exactly what this policy refuses. A global guard would have severed all of
// them. The rule that matters is not "guard every socket" but "guard every
// socket whose destination the MODEL chose" — internal service calls read
// their host from an env var, never from a turn. So the guard rides a
// transport handed to the model-facing paths, and the internal plumbing is
// untouched.

// BlockedError is returned when a destination is refused. It reads as a
// sentence because it reaches the model as a tool result and, through it, the
// boss — "blocked by network policy" tells him nothing about what happened.
type BlockedError struct {
	Host   string
	Reason string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("refused to connect to %s: %s", e.Host, e.Reason)
}

// blockedIP reports whether an address is somewhere Jarvis has no business
// reaching from a link he was handed, and why in plain words.
//
// The named ranges are the ones every cloud provider parks its credential
// endpoint in. 169.254.169.254 (AWS/GCP/Azure) and 169.254.170.2 (ECS) fall
// under link-local; 100.100.100.200 (Alibaba) falls under carrier-grade NAT.
// They are covered by range rather than listed by address on purpose: a list
// goes stale the day a provider picks a new one.
func blockedIP(ip net.IP) (bool, string) {
	if ip == nil {
		return true, "that address could not be read"
	}
	// Normalise 4-in-6 (::ffff:10.0.0.1) so an IPv4 rule cannot be dodged by
	// spelling the same address the other way.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	} else if v4 := nat64Embedded(ip); v4 != nil {
		// 64:ff9b::/96 carries an IPv4 address inside an IPv6 one. Judge it by
		// what it actually reaches.
		ip = v4
	}

	switch {
	case ip.IsUnspecified():
		return true, "it is an unspecified address"
	case ip.IsLoopback():
		return true, "it points back at this machine"
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return true, "it is a link-local address, where cloud providers keep their credential endpoints"
	case ip.IsPrivate():
		return true, "it is on a private network, not the public internet"
	case ip.IsMulticast():
		return true, "it is a multicast address"
	case ip.IsInterfaceLocalMulticast():
		return true, "it is an interface-local address"
	}
	// Carrier-grade NAT, 100.64.0.0/10. Not covered by IsPrivate, and it is
	// where Alibaba's metadata service lives.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return true, "it is on a carrier-private network, not the public internet"
	}
	return false, ""
}

// nat64Embedded returns the IPv4 address carried inside a 64:ff9b::/96 address,
// or nil. Without this, 64:ff9b::a9fe:a9fe reaches 169.254.169.254 while
// looking like an ordinary public IPv6 address.
func nat64Embedded(ip net.IP) net.IP {
	v6 := ip.To16()
	if v6 == nil || ip.To4() != nil {
		return nil
	}
	prefix := []byte{0x00, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0}
	for i, b := range prefix {
		if v6[i] != b {
			return nil
		}
	}
	return net.IPv4(v6[12], v6[13], v6[14], v6[15])
}

// blockedHostname refuses names that resolve somewhere internal, before we even
// look them up. The dialer below is the real boundary — this exists so the
// model gets a useful sentence back instead of a connection error, and so an
// internal name is refused even if DNS is lying in the other direction.
//
// The trailing-dot form matters: "localhost." is a distinct string that
// resolves identically, and every string-matching guard that forgets it has a
// hole.
func blockedHostname(host string) (bool, string) {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return true, "no host was given"
	}
	switch h {
	case "localhost", "0.0.0.0", "::", "[::]":
		return true, "it points back at this machine"
	case "metadata.google.internal", "metadata.goog", "metadata",
		"instance-data", "kubernetes.default.svc", "kubernetes.default":
		return true, "it is a cloud or cluster service endpoint, not a public site"
	}
	for _, suffix := range []string{".local", ".internal", ".localhost", ".svc", ".cluster.local"} {
		if strings.HasSuffix(h, suffix) {
			return true, "it is an internal network name, not a public site"
		}
	}
	return false, ""
}

// CheckTarget validates a URL before it is fetched. Exported so every
// model-facing caller shares ONE policy: these rules used to live unexported
// inside package tools, which is why the browser, the extension HTTP tools and
// the sentinel poller had no guard at all.
//
// This is the fail-fast half. The dialer is the half that cannot be fooled.
func CheckTarget(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("that URL could not be read: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return &BlockedError{Host: raw, Reason: fmt.Sprintf("%q is not a web address", u.Scheme)}
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if bad, why := blockedIP(ip); bad {
			return &BlockedError{Host: host, Reason: why}
		}
		return nil
	}
	if bad, why := blockedHostname(host); bad {
		return &BlockedError{Host: host, Reason: why}
	}
	return nil
}

// guardedDialer refuses a connection after the name has resolved and before the
// socket opens, which is the only moment at which the destination is known for
// certain.
var guardedDialer = &net.Dialer{
	Timeout:   30 * time.Second,
	KeepAlive: 30 * time.Second,
	ControlContext: func(_ context.Context, _, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return &BlockedError{Host: address, Reason: "that address could not be read"}
		}
		if bad, why := blockedIP(net.ParseIP(host)); bad {
			return &BlockedError{Host: host, Reason: why}
		}
		return nil
	},
}

// GuardedClient returns an *http.Client for fetching addresses the MODEL chose:
// http_fetch, extension HTTP tools, sentinel polls, web search.
//
// It carries the same failure instrumentation as every other client here (so a
// 401 still reaches mem_http_failures and can never read as an empty success),
// plus the dialer guard and a redirect check that applies the policy to every
// hop rather than only the first.
//
// Do NOT use this for calls to our own services on the Railway private network
// — it will correctly refuse them. Those hosts come from env vars, not from a
// turn, and are not part of this threat model.
func GuardedClient(name string, timeout time.Duration) *http.Client {
	base := Unwrap(http.DefaultTransport)
	t, ok := base.(*http.Transport)
	if !ok {
		t = &http.Transport{}
	} else {
		t = t.Clone()
	}
	t.DialContext = guardedDialer.DialContext

	return &http.Client{
		Timeout:   timeout,
		Transport: Wrap(t, defaultRec, name),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			// The hop is what matters. A clean first URL that redirects into a
			// private range was the actual bypass; the dialer would catch it
			// anyway, but refusing here names the redirect in the error the
			// model reads.
			return CheckTarget(req.URL.String())
		},
	}
}
