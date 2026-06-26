package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCacheLoadAccountsUsesProjectAPIKeyOnly(t *testing.T) {
	var sawPath, sawAPIKey, sawAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawAPIKey = r.Header.Get("x-api-key")
		sawAuth = r.Header.Get("Authorization")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"ca_123","status":"ACTIVE","user_id":"boss","created_at":"2026-06-25T16:00:00Z","toolkit":{"slug":"gmail","name":"Gmail"}}]}`))
	}))
	t.Cleanup(ts.Close)

	c := New(nil, func() string { return "project-key" })
	c.apiBaseURL = ts.URL
	c.httpClient = ts.Client()

	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	if sawPath != "/connected_accounts" {
		t.Fatalf("path = %q, want /connected_accounts", sawPath)
	}
	if sawAPIKey != "project-key" {
		t.Fatalf("x-api-key = %q, want project-key", sawAPIKey)
	}
	if sawAuth != "" {
		t.Fatalf("Authorization = %q, want empty", sawAuth)
	}
	if got := c.ActiveAccountsByToolkit("gmail"); len(got) != 1 || got[0].ID != "ca_123" {
		t.Fatalf("active gmail accounts = %#v, want ca_123", got)
	}
}

func TestCacheRefreshKeepsComposioAuthErrorVisible(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Multiple authentication modes were provided. Provide exactly one authentication mode per request.","code":10401}}`))
	}))
	t.Cleanup(ts.Close)

	c := New(nil, func() string { return "project-key" })
	c.apiBaseURL = ts.URL
	c.httpClient = ts.Client()

	err := c.Refresh(context.Background())
	if err == nil {
		t.Fatal("Refresh returned nil error, want Composio auth error")
	}
	st := c.Status()
	if !strings.Contains(st.LastError, "composio list") ||
		!strings.Contains(st.LastError, "401") ||
		!strings.Contains(st.LastError, "10401") {
		t.Fatalf("LastError = %q, want visible Composio 401/10401", st.LastError)
	}
}
