package browser

import (
	"strings"

	"github.com/dopesoft/infinity/core/internal/httpx"
)

// guardAgentURL applies the network boundary to a URL the MODEL chose, and
// returns the normalised form to send onward.
//
// Only the agent's own tools go through here. The boss driving the Preview
// pane's URL bar (Registry.Navigate) deliberately does NOT: looking at his own
// internal dashboard from his own browser is a thing he is allowed to do, and
// the threat being defended against is Jarvis being TALKED INTO fetching an
// internal address by something he read. Guarding the boss's typing would block
// legitimate use to stop an attack that isn't happening on that path.
//
// The scheme is filled in here rather than left to the browser container: the
// container prepends https:// itself, so without this a bare "169.254.169.254"
// would slip past a check that only understands full URLs and then be turned
// into a real request downstream.
//
// KNOWN RESIDUAL GAP, stated rather than papered over: this is a URL check, not
// a dialer guard, because the page is fetched by Chromium inside a separate
// container with its own network stack that no Go dialer of ours can sit under.
// It stops the direct case (the model is told to open an internal address); it
// cannot stop a public page that 302s somewhere private once Chromium has it.
// Closing that properly needs egress rules on the browser container itself.
func guardAgentURL(raw string) (string, error) {
	u := strings.TrimSpace(raw)
	if u == "" || strings.EqualFold(u, "about:blank") {
		return u, nil // opening an empty session reaches nothing
	}
	if !strings.Contains(u, "://") {
		u = "https://" + u
	}
	if err := httpx.CheckTarget(u); err != nil {
		return "", err
	}
	return u, nil
}
