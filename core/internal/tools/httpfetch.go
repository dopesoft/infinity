package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/httpx"
)

type HTTPFetch struct {
	client *http.Client
}

func NewHTTPFetchFromEnv() (*HTTPFetch, error) {
	// No domain allowlist: this is a single-user personal agent and "the internet"
	// is a first-class building block (see Rule #1). The only network restriction is
	// the SSRF boundary (localhost / private ranges / cloud metadata), which is a
	// security boundary, not an allowlist.
	//
	// The boundary is the GUARDED CLIENT, not the pre-check below. This client
	// refuses a destination after the name resolves and before the socket opens,
	// once per redirect hop — closing the two holes the old string check could
	// not: a 302 into the metadata endpoint, and a hostname that resolves into a
	// private range.
	return &HTTPFetch{
		client: httpx.GuardedClient("http_fetch", 30*time.Second),
	}, nil
}

func (h *HTTPFetch) Name() string        { return "http_fetch" }
func (h *HTTPFetch) Description() string { return "Fetch any URL via HTTP(S). Returns response text and status." }

func (h *HTTPFetch) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url":    map[string]any{"type": "string", "description": "Full URL to fetch."},
			"method": map[string]any{"type": "string", "enum": []string{"GET", "POST", "PUT"}, "default": "GET"},
			"body":   map[string]any{"type": "string", "description": "Request body for POST/PUT."},
		},
		"required": []string{"url"},
	}
}

func (h *HTTPFetch) Execute(ctx context.Context, input map[string]any) (string, error) {
	rawURL, _ := input["url"].(string)
	if rawURL == "" {
		return "", errors.New("url is required")
	}
	// Fail fast with a sentence the model can act on. The guarded client is the
	// boundary that cannot be fooled; this is here so an obviously-internal URL
	// comes back explained rather than as a connection error.
	if err := httpx.CheckTarget(rawURL); err != nil {
		return "", err
	}

	method, _ := input["method"].(string)
	if method == "" {
		method = http.MethodGet
	}
	body, _ := input["body"].(string)

	req, err := http.NewRequestWithContext(ctx, method, rawURL, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Infinity/0.1 (+http_fetch)")

	resp, err := h.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	const maxBytes = 1 << 20 // 1 MB
	limited := io.LimitReader(resp.Body, maxBytes)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("HTTP %d %s\n\n%s", resp.StatusCode, resp.Status, string(data)), nil
}

// The SSRF policy that used to live here (validateHTTPFetchTarget,
// isBlockedHostname, isBlockedIP) MOVED to httpx.CheckTarget and the guarded
// dialer behind httpx.GuardedClient. It was unexported and package-local, which
// is precisely why the agentic browser, the extension HTTP tools and the
// sentinel poller shipped with no guard at all — they could not reach it. The
// shared version covers everything these did, plus IPv4-mapped and NAT64
// spellings, carrier-grade NAT, trailing-dot hostnames, and every redirect hop.
