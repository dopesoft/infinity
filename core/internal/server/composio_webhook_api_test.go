package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestComposioWebhookSummaryDetectsExpiredGmailAccount(t *testing.T) {
	payload := map[string]any{
		"id":        "evt_847cdfcd-d219-4f18-a6dd-91acd42ca94a",
		"timestamp": "2026-02-06T12:00:00.000Z",
		"type":      "composio.connected_account.expired",
		"metadata": map[string]any{
			"project_id": "pr_project",
			"org_id":     "ok_org",
		},
		"data": map[string]any{
			"toolkit": map[string]any{
				"slug": "gmail",
			},
			"auth_config": map[string]any{
				"id":          "ac_gmail",
				"auth_scheme": "OAUTH2",
			},
			"id":            "ca_gmail",
			"status":        "EXPIRED",
			"status_reason": "OAuth refresh token expired",
		},
	}

	sum := summarizeComposioWebhook(payload)
	if sum.Event != "composio.connected_account.expired" {
		t.Fatalf("event = %q", sum.Event)
	}
	if sum.AccountID != "ca_gmail" {
		t.Fatalf("account = %q", sum.AccountID)
	}
	if sum.Toolkit != "gmail" {
		t.Fatalf("toolkit = %q", sum.Toolkit)
	}
	if sum.Status != "EXPIRED" {
		t.Fatalf("status = %q", sum.Status)
	}
	if !composioWebhookIsFailure(sum) {
		t.Fatal("expired account should be treated as failure")
	}
	if got := composioWebhookTitle(sum); got != "Gmail Composio account needs reconnection" {
		t.Fatalf("title = %q", got)
	}
}

func TestComposioWebhookSummaryDetectsDisabledTrigger(t *testing.T) {
	payload := map[string]any{
		"id":   "msg_847cdfcd-d219-4f18-a6dd-91acd42ca94a",
		"type": "composio.trigger.disabled",
		"metadata": map[string]any{
			"project_id": "pr_project",
		},
		"data": map[string]any{
			"id":                   "ti_trigger",
			"connected_account_id": "ca_gmail",
			"trigger_name":         "GMAIL_NEW_GMAIL_MESSAGE",
			"user_id":              "mr-khaya",
			"disabled_reason":      "connection_expired",
		},
	}

	sum := summarizeComposioWebhook(payload)
	if sum.TriggerID != "ti_trigger" {
		t.Fatalf("trigger = %q", sum.TriggerID)
	}
	if sum.AccountID != "ca_gmail" {
		t.Fatalf("account = %q", sum.AccountID)
	}
	if sum.Status != "CONNECTION_EXPIRED" {
		t.Fatalf("status = %q", sum.Status)
	}
	if !composioWebhookIsFailure(sum) {
		t.Fatal("disabled trigger should be treated as failure")
	}
}

func TestComposioWebhookAuthRequiresSecret(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/composio", nil)
	t.Setenv("COMPOSIO_WEBHOOK_SECRET", "")
	if composioWebhookAuthorized(req) {
		t.Fatal("webhook should be rejected when secret is unset")
	}

	t.Setenv("COMPOSIO_WEBHOOK_SECRET", "expected")
	if composioWebhookAuthorized(req) {
		t.Fatal("webhook should be rejected when secret is set and missing")
	}
	req.Header.Set("Authorization", "Bearer expected")
	if !composioWebhookAuthorized(req) {
		t.Fatal("webhook should accept matching bearer secret")
	}
}

func TestVerifyComposioWebhookSignature(t *testing.T) {
	t.Setenv("COMPOSIO_WEBHOOK_SECRET", "composio-signing-secret")
	body := []byte(`{"type":"composio.connected_account.expired","data":{"id":"ca_123"}}`)
	id := "evt_123"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := "v1," + signComposioWebhookForTest("composio-signing-secret", id, timestamp, body)

	if err := verifyComposioWebhookSignature(id, timestamp, body, signature); err != nil {
		t.Fatalf("verifyComposioWebhookSignature() error = %v", err)
	}
	if err := verifyComposioWebhookSignature(id, timestamp, []byte(`{"changed":true}`), signature); err == nil {
		t.Fatal("changed body should fail signature verification")
	}
}

func TestVerifyComposioWebhookRequestPrefersSignatureOverURLToken(t *testing.T) {
	t.Setenv("COMPOSIO_WEBHOOK_SECRET", "composio-signing-secret")
	body := []byte(`{"type":"composio.trigger.disabled","data":{"id":"ti_123"}}`)
	id := "evt_123"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/composio?token=composio-signing-secret", nil)
	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", timestamp)
	req.Header.Set("webhook-signature", "v1,bad")
	if err := verifyComposioWebhookRequest(req, body); err == nil {
		t.Fatal("bad signature should fail even when URL token matches")
	}
}

func signComposioWebhookForTest(secret, id, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(id))
	mac.Write([]byte("."))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
