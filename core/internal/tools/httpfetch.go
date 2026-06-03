package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPFetch struct {
	client *http.Client
}

func NewHTTPFetchFromEnv() (*HTTPFetch, error) {
	// No domain allowlist: this is a single-user personal agent and "the internet"
	// is a first-class building block (see Rule #1). The only network restriction is
	// the SSRF guard below (localhost / private ranges / cloud metadata), which is a
	// security boundary, not an allowlist.
	return &HTTPFetch{
		client: &http.Client{Timeout: 30 * time.Second},
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
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	host := strings.ToLower(parsed.Hostname())
	if err := validateHTTPFetchTarget(parsed, host); err != nil {
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

// validateHTTPFetchTarget enforces the SSRF boundary only: a real http(s) scheme
// and a host that isn't localhost, a private/link-local range, or a cloud-metadata
// endpoint. There is deliberately no domain allowlist.
func validateHTTPFetchTarget(parsed *url.URL, host string) error {
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	if host == "" {
		return errors.New("url host is required")
	}
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("host %q blocked by network policy", host)
		}
	} else {
		if isBlockedHostname(host) {
			return fmt.Errorf("host %q blocked by network policy", host)
		}
	}
	return nil
}

func isBlockedHostname(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" || h == "metadata.google.internal" || h == "169.254.169.254" || h == "0.0.0.0" {
		return true
	}
	if strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") || strings.HasSuffix(h, ".localhost") {
		return true
	}
	return false
}

func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 169 && v4[1] == 254 {
			return true
		}
	}
	return false
}
