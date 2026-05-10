package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

type HTTPFetch struct {
	allowed []string
	client  *http.Client
}

func NewHTTPFetchFromEnv() (*HTTPFetch, error) {
	raw := strings.TrimSpace(os.Getenv("HTTP_FETCH_ALLOWED_DOMAINS"))
	if raw == "" {
		return nil, errors.New("HTTP_FETCH_ALLOWED_DOMAINS not set")
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return &HTTPFetch{
		allowed: out,
		client:  &http.Client{Timeout: 30 * time.Second},
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
	if !h.matches(host) {
		return "", fmt.Errorf("host %q not in allowlist", host)
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
