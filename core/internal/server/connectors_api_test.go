package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func withComposioHTTP(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	prev := composioHTTP
	composioHTTP = &http.Client{Transport: rt}
	t.Cleanup(func() { composioHTTP = prev })
}

func TestComposioRESTKeyPrefersProjectAPIKey(t *testing.T) {
	t.Setenv("COMPOSIO_API_KEY", "project-key")
	t.Setenv("COMPOSIO_ADMIN_API_KEY", "legacy-key")

	if got := composioRESTKey(); got != "project-key" {
		t.Fatalf("composioRESTKey() = %q, want project-key", got)
	}
}

func TestApplyComposioAuthUsesProjectRESTHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	applyComposioAuth(req, "project-key")

	if got := req.Header.Get("x-api-key"); got != "project-key" {
		t.Fatalf("x-api-key = %q, want project-key", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want empty", got)
	}
	if got := req.Header.Get("x-consumer-api-key"); got != "" {
		t.Fatalf("x-consumer-api-key = %q, want empty", got)
	}
}

func TestComposioToolkitsProxyUsesProjectAPIKey(t *testing.T) {
	t.Setenv("COMPOSIO_API_KEY", "project-key")
	t.Setenv("COMPOSIO_ADMIN_API_KEY", "")

	var sawPath, sawAPIKey, sawAuth, sawConsumerKey string
	withComposioHTTP(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sawPath = r.URL.Path
		sawAPIKey = r.Header.Get("x-api-key")
		sawAuth = r.Header.Get("Authorization")
		sawConsumerKey = r.Header.Get("x-consumer-api-key")
		return jsonResp(http.StatusOK, `{"items":[]}`), nil
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/connectors/composio/toolkits?limit=3", nil)
	(&Server{}).handleComposioToolkits(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if sawPath != "/api/v3.1/toolkits" {
		t.Fatalf("proxied path = %q, want /api/v3.1/toolkits", sawPath)
	}
	if sawAPIKey != "project-key" {
		t.Fatalf("x-api-key = %q, want project-key", sawAPIKey)
	}
	if sawAuth != "" {
		t.Fatalf("Authorization = %q, want empty", sawAuth)
	}
	if sawConsumerKey != "" {
		t.Fatalf("x-consumer-api-key = %q, want empty", sawConsumerKey)
	}
}

func TestComposioConnectWorksWithProjectAPIKey(t *testing.T) {
	t.Setenv("COMPOSIO_API_KEY", "project-key")
	t.Setenv("COMPOSIO_ADMIN_API_KEY", "")

	var calls []string
	withComposioHTTP(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if got := r.Header.Get("x-api-key"); got != "project-key" {
			t.Fatalf("x-api-key = %q, want project-key", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want empty", got)
		}
		if got := r.Header.Get("x-consumer-api-key"); got != "" {
			t.Fatalf("x-consumer-api-key = %q, want empty", got)
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v3.1/auth_configs":
			return jsonResp(http.StatusOK, `{"items":[{"auth_config":{"id":"ac_123"}}]}`), nil
		case "POST /api/v3.1/connected_accounts/link":
			return jsonResp(http.StatusOK, `{"redirect_url":"https://example.test/oauth","id":"ca_123"}`), nil
		default:
			t.Fatalf("unexpected upstream call %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/connectors/composio/connect",
		strings.NewReader(`{"toolkit_slug":"gmail","user_id":"personal","alias":"personal"}`),
	)
	(&Server{}).handleComposioConnect(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if got, want := strings.Join(calls, ","), "GET /api/v3.1/auth_configs,POST /api/v3.1/connected_accounts/link"; got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
	if !strings.Contains(rr.Body.String(), "https://example.test/oauth") {
		t.Fatalf("response missing redirect URL: %s", rr.Body.String())
	}
}

func TestComposioAccountRefreshUsesExistingConnectedAccount(t *testing.T) {
	t.Setenv("COMPOSIO_API_KEY", "project-key")
	t.Setenv("COMPOSIO_ADMIN_API_KEY", "")

	var sawPath, sawAPIKey, sawAuth string
	withComposioHTTP(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sawPath = r.URL.Path
		sawAPIKey = r.Header.Get("x-api-key")
		sawAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		return jsonResp(http.StatusOK, `{"id":"ca_existing","redirect_url":"https://example.test/reauth"}`), nil
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/composio/accounts/ca_existing/refresh", nil)
	(&Server{}).handleComposioAccount(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if sawPath != "/api/v3.1/connected_accounts/ca_existing/refresh" {
		t.Fatalf("proxied path = %q", sawPath)
	}
	if sawAPIKey != "project-key" {
		t.Fatalf("x-api-key = %q, want project-key", sawAPIKey)
	}
	if sawAuth != "" {
		t.Fatalf("Authorization = %q, want empty", sawAuth)
	}
	if !strings.Contains(rr.Body.String(), "https://example.test/reauth") {
		t.Fatalf("response missing redirect URL: %s", rr.Body.String())
	}
}
