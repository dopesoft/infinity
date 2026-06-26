package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExecuteClientUsesProjectAPIKeyOnly(t *testing.T) {
	var sawPath, sawAPIKey, sawAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawAPIKey = r.Header.Get("x-api-key")
		sawAuth = r.Header.Get("Authorization")

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := body["connected_account_id"]; got != "ca_123" {
			t.Fatalf("connected_account_id = %v, want ca_123", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"successful":true,"data":{"ok":true}}`))
	}))
	t.Cleanup(ts.Close)

	c := NewExecuteClient(func() string { return "project-key" })
	c.baseURL = ts.URL
	c.httpClient = ts.Client()

	resp, err := c.Execute(context.Background(), ExecuteRequest{
		Slug:               "GMAIL_FETCH_EMAILS",
		ConnectedAccountID: "ca_123",
		Arguments:          map[string]any{"query": "in:inbox"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if resp == nil || !resp.Successful {
		t.Fatalf("Execute response = %#v, want successful", resp)
	}
	if sawPath != "/tools/execute/GMAIL_FETCH_EMAILS" {
		t.Fatalf("path = %q, want /tools/execute/GMAIL_FETCH_EMAILS", sawPath)
	}
	if sawAPIKey != "project-key" {
		t.Fatalf("x-api-key = %q, want project-key", sawAPIKey)
	}
	if sawAuth != "" {
		t.Fatalf("Authorization = %q, want empty", sawAuth)
	}
}

func TestExecuteClientReturnsComposioAuthError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Multiple authentication modes were provided","code":10401}}`))
	}))
	t.Cleanup(ts.Close)

	c := NewExecuteClient(func() string { return "project-key" })
	c.baseURL = ts.URL
	c.httpClient = ts.Client()

	_, err := c.Execute(context.Background(), ExecuteRequest{Slug: "GMAIL_FETCH_EMAILS"})
	if err == nil {
		t.Fatal("Execute returned nil error, want Composio auth error")
	}
	if got := err.Error(); !strings.Contains(got, "401") || !strings.Contains(got, "10401") {
		t.Fatalf("error = %q, want status and vendor code", got)
	}
}
