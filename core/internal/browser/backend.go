package browser

import "context"

// Backend is the swappable browser engine that sits behind the browser_* verb
// tools. The verb contract — open → navigate → observe → act → extract → close,
// plus a live screencast — is identical across engines; only the wire protocol
// differs. Per Rule #1 (CLAUDE.md) the "how to browse" cognition stays in the
// seeded browser skill, never in here.
//
// Two engines ship:
//
//   - *Client          the chromedp / headless-Chromium sidecar (docker/browser),
//     fast and CDP-screencast native. No anti-detect.
//   - *CamofoxBackend   the anti-detect Camoufox / Firefox REST server
//     (redf0x1/camofox-browser): C++ engine-level fingerprint
//     spoofing, proxy + GeoIP. Beats Cloudflare / DataDome.
//
// *RoutingBackend composes a Mac (residential-IP) and a Cloud Camoufox backend
// and picks Mac-first per session — the residential IP is what makes anti-detect
// actually land, so we prefer it whenever the home Mac is reachable.
type Backend interface {
	Health(ctx context.Context) error
	CreateSession(ctx context.Context, url string) (*SessionInfo, error)
	Navigate(ctx context.Context, sessionID, url string) (*NavResult, error)
	Observe(ctx context.Context, sessionID string) (*ObserveResult, error)
	Act(ctx context.Context, sessionID string, req ActRequest) (*ActResult, error)
	// Input forwards a raw human interaction (click/type/scroll) to the live
	// session - the boss's manual takeover of the screencast. Engines without a
	// raw-input path return an error so the caller can surface "not supported".
	Input(ctx context.Context, sessionID string, ev InputEvent) error
	Extract(ctx context.Context, sessionID, format string) (*ExtractResult, error)
	Screenshot(ctx context.Context, sessionID string) (*ShotResult, error)
	Close(ctx context.Context, sessionID string) error
	// SubscribeScreencast returns a channel of live frames for Studio's Preview
	// pane. The chromedp backend relays the sidecar's CDP screencast SSE; the
	// Camoufox backend has no screencast, so it polls screenshots internally and
	// emits frames on the same channel. The registry relay is identical for both.
	SubscribeScreencast(ctx context.Context, sessionID string) (<-chan Frame, error)
}

// Compile-time proof every engine satisfies the contract.
var (
	_ Backend = (*Client)(nil)
	_ Backend = (*CamofoxBackend)(nil)
	_ Backend = (*RoutingBackend)(nil)
)
