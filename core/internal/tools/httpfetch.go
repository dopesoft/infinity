package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

type HTTPFetch struct {
	allowed         []string
	enforceAllowlist bool
	client          *http.Client
}

func NewHTTPFetchFromEnv() (*HTTPFetch, error) {
	raw := strings.TrimSpace(os.Getenv("HTTP_FETCH_ALLOWED_DOMAINS"))
	enforce := raw != ""
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return &HTTPFetch{
		allowed:          out,
		enforceAllowlist: enforce,
		client:           &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (h *HTTPFetch) Name() string        { return "http_fetch" }
func (h *HTTPFetch) Description() string { return "Fetch a URL via HTTP. Allowlist enforced. Returns response text and status." }

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
	if err := validateHTTPFetchTarget(parsed, host, h.enforceAllowlist, h.allowed); err != nil {
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

func (h *HTTPFetch) matches(host string) bool {
	for _, pattern := range h.allowed {
		if pattern == host {
			return true
		}
		ok, _ := path.Match(pattern, host)
		if ok {
			return true
		}
		if strings.HasPrefix(pattern, "*.") && strings.HasSuffix(host, strings.TrimPrefix(pattern, "*")) {
			return true
		}
	}
	return false
}

func validateHTTPFetchTarget(parsed *url.URL, host string, enforceAllowlist bool, allowed []string) error {
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
	if enforceAllowlist {
		for _, pattern := range allowed {
			if pattern == host {
				return nil
			}
			ok, _ := path.Match(pattern, host)
			if ok {
				return nil
			}
			if strings.HasPrefix(pattern, "*.") && strings.HasSuffix(host, strings.TrimPrefix(pattern, "*")) {
				return nil
			}
		}
		return fmt.Errorf("host %q not in allowlist", host)
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
