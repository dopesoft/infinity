package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/proactive"
	"github.com/dopesoft/infinity/core/internal/surface"
)

const composioWebhookMaxBytes = 1 << 20
const composioWebhookTolerance = 5 * time.Minute

type composioWebhookSummary struct {
	Event     string
	AccountID string
	Toolkit   string
	Status    string
	TriggerID string
	EntityID  string
}

func (s *Server) handleComposioWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if strings.TrimSpace(os.Getenv("COMPOSIO_WEBHOOK_SECRET")) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "COMPOSIO_WEBHOOK_SECRET not configured"})
		return
	}
	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, composioWebhookMaxBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if err := verifyComposioWebhookRequest(r, body); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var payload map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&payload); err != nil {
		if err == io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty JSON body"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	summary := summarizeComposioWebhook(payload)
	if s.connectors != nil {
		if err := s.connectors.Refresh(r.Context()); err != nil {
			slog.Default().Warn("composio webhook connector refresh", "event", summary.Event, "err", err)
		}
	}

	findingID, surfaceID := "", ""
	if composioWebhookIsFailure(summary) {
		findingID, surfaceID, err = s.recordComposioWebhookFailure(r.Context(), summary, payload)
		if err != nil {
			slog.Default().Warn("composio webhook finding", "event", summary.Event, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"event":      summary.Event,
		"failure":    composioWebhookIsFailure(summary),
		"finding_id": findingID,
		"surface_id": surfaceID,
	})
}

func composioWebhookAuthorized(r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("COMPOSIO_WEBHOOK_SECRET"))
	if expected == "" {
		return false
	}
	candidates := []string{
		r.Header.Get("X-Infinity-Webhook-Secret"),
		r.URL.Query().Get("token"),
	}
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		candidates = append(candidates, strings.TrimSpace(auth[len("Bearer "):]))
	}
	for _, got := range candidates {
		got = strings.TrimSpace(got)
		if got == "" || len(got) != len(expected) {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1 {
			return true
		}
	}
	return false
}

func verifyComposioWebhookRequest(r *http.Request, body []byte) error {
	signature := strings.TrimSpace(r.Header.Get("webhook-signature"))
	webhookID := strings.TrimSpace(r.Header.Get("webhook-id"))
	timestamp := strings.TrimSpace(r.Header.Get("webhook-timestamp"))
	if signature == "" && webhookID == "" && timestamp == "" {
		if composioWebhookAuthorized(r) {
			return nil
		}
		return errors.New("missing webhook signature")
	}
	if err := verifyComposioWebhookSignature(webhookID, timestamp, body, signature); err != nil {
		return err
	}
	return nil
}

func verifyComposioWebhookSignature(webhookID, timestamp string, body []byte, signature string) error {
	secret := strings.TrimSpace(os.Getenv("COMPOSIO_WEBHOOK_SECRET"))
	if secret == "" {
		return errors.New("missing webhook secret")
	}
	if strings.TrimSpace(webhookID) == "" || strings.TrimSpace(timestamp) == "" || strings.TrimSpace(signature) == "" {
		return errors.New("missing webhook signature headers")
	}
	if err := validateComposioWebhookTimestamp(timestamp, time.Now()); err != nil {
		return err
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(webhookID))
	mac.Write([]byte("."))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	for _, received := range composioWebhookSignatureCandidates(signature) {
		if hmac.Equal([]byte(expected), []byte(received)) {
			return nil
		}
	}
	return errors.New("invalid webhook signature")
}

func validateComposioWebhookTimestamp(timestamp string, now time.Time) error {
	ts, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return errors.New("invalid webhook timestamp")
	}
	at := time.Unix(ts, 0)
	if at.After(now.Add(composioWebhookTolerance)) || at.Before(now.Add(-composioWebhookTolerance)) {
		return errors.New("webhook timestamp outside tolerance")
	}
	return nil
}

func composioWebhookSignatureCandidates(signature string) []string {
	parts := strings.Fields(signature)
	if len(parts) == 0 {
		parts = []string{signature}
	}
	out := make([]string, 0, len(parts)*2)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		segments := strings.Split(part, ",")
		candidate := strings.TrimSpace(segments[len(segments)-1])
		if candidate != "" {
			out = append(out, candidate)
		}
		if idx := strings.LastIndex(candidate, "="); idx >= 0 && idx+1 < len(candidate) {
			out = append(out, strings.TrimSpace(candidate[idx+1:]))
		}
	}
	return out
}

func (s *Server) recordComposioWebhookFailure(
	ctx context.Context,
	sum composioWebhookSummary,
	payload map[string]any,
) (findingID string, surfaceID string, err error) {
	tag := composioWebhookSourceTag(sum, payload)
	title := composioWebhookTitle(sum)
	detail := composioWebhookDetail(sum)

	findingID, _, _, err = proactive.UpsertFinding(ctx, s.pool, slog.Default(), proactive.FindingDraft{
		Kind:       "outcome",
		Source:     "composio-webhook",
		SourceTag:  tag,
		Title:      title,
		Detail:     detail,
		Importance: 10,
		Sample:     composioWebhookSample(sum),
	})
	if err != nil {
		return "", "", err
	}

	imp := 100
	store := surface.NewStore(s.pool, slog.Default())
	item := &surface.Item{
		Surface:          "alerts",
		Kind:             "alert",
		Source:           "composio-webhook",
		ExternalID:       tag,
		Title:            title,
		Subtitle:         "Connector automation needs attention",
		Body:             composioWebhookBody(sum),
		Importance:       &imp,
		ImportanceReason: "Composio reported an account or trigger failure; connected workflows may be stale until it is reconnected.",
		Metadata: map[string]any{
			"event":      sum.Event,
			"account_id": sum.AccountID,
			"toolkit":    sum.Toolkit,
			"status":     sum.Status,
			"trigger_id": sum.TriggerID,
			"entity_id":  sum.EntityID,
		},
	}
	surfaceID, err = store.Upsert(ctx, item)
	if err != nil {
		return findingID, "", err
	}
	return findingID, surfaceID, nil
}

func summarizeComposioWebhook(payload map[string]any) composioWebhookSummary {
	metadata := webhookMap(payload["metadata"])
	data := webhookMap(payload["data"])
	sum := composioWebhookSummary{
		Event: strings.ToLower(firstWebhookString(payload,
			"event", "type", "event_type", "eventtype", "event_name", "eventname",
		)),
		AccountID: firstWebhookString(payload,
			"connected_account_id", "connectedaccountid", "connectedaccount_id",
			"connected_account_uuid", "account_id", "accountid",
		),
		Toolkit: strings.ToLower(firstWebhookString(payload,
			"toolkit_slug", "toolkitslug", "toolkit", "app", "app_name", "appname", "integration",
		)),
		Status: strings.ToUpper(firstWebhookString(payload,
			"status", "account_status", "accountstatus", "state",
		)),
		TriggerID: firstWebhookString(payload,
			"trigger_id", "triggerid", "trigger_instance_id", "triggerinstanceid",
		),
		EntityID: firstWebhookString(payload,
			"entity_id", "entityid", "user_id", "userid",
		),
	}

	// V3 envelope: top-level type, Composio metadata in metadata, provider
	// payload or account/trigger resource in data.
	if sum.AccountID == "" {
		sum.AccountID = webhookString(metadata["connected_account_id"])
	}
	if sum.TriggerID == "" {
		sum.TriggerID = webhookString(metadata["trigger_id"])
	}
	if sum.EntityID == "" {
		sum.EntityID = webhookString(metadata["user_id"])
	}
	switch sum.Event {
	case "composio.connected_account.expired":
		if sum.AccountID == "" {
			sum.AccountID = webhookString(data["id"])
		}
		if sum.Toolkit == "" {
			if toolkit := webhookMap(data["toolkit"]); toolkit != nil {
				sum.Toolkit = strings.ToLower(webhookString(toolkit["slug"]))
			}
		}
		if sum.Status == "" {
			sum.Status = strings.ToUpper(webhookString(data["status"]))
		}
	case "composio.trigger.disabled":
		if sum.TriggerID == "" {
			sum.TriggerID = webhookString(data["id"])
		}
		if sum.AccountID == "" {
			sum.AccountID = webhookString(data["connected_account_id"])
		}
		if sum.EntityID == "" {
			sum.EntityID = webhookString(data["user_id"])
		}
		if sum.Status == "" {
			sum.Status = strings.ToUpper(webhookString(data["disabled_reason"]))
		}
	}

	if sum.AccountID == "" {
		sum.AccountID = firstNestedWebhookString(payload,
			[]string{"connected_account", "connectedaccount", "account"},
			[]string{"id", "uuid"},
		)
	}
	if sum.TriggerID == "" {
		sum.TriggerID = firstNestedWebhookString(payload,
			[]string{"trigger", "trigger_instance", "triggerinstance"},
			[]string{"id", "uuid"},
		)
	}
	return sum
}

func composioWebhookIsFailure(sum composioWebhookSummary) bool {
	event := strings.ToLower(sum.Event)
	if strings.Contains(event, "connected_account.expired") || strings.Contains(event, "trigger.disabled") {
		return true
	}
	status := strings.ToUpper(sum.Status)
	switch status {
	case "EXPIRED", "REVOKED", "DISABLED", "INACTIVE":
		return true
	default:
		return false
	}
}

func composioWebhookSourceTag(sum composioWebhookSummary, payload map[string]any) string {
	parts := []string{"composio-webhook", nonEmpty(sum.Event, "unknown")}
	switch {
	case sum.AccountID != "":
		parts = append(parts, sum.AccountID)
	case sum.TriggerID != "":
		parts = append(parts, sum.TriggerID)
	default:
		parts = append(parts, webhookPayloadHash(payload))
	}
	return strings.Join(parts, ":")
}

func composioWebhookTitle(sum composioWebhookSummary) string {
	toolkit := strings.TrimSpace(sum.Toolkit)
	if toolkit == "gmail" {
		return "Gmail Composio account needs reconnection"
	}
	if strings.Contains(strings.ToLower(sum.Event), "trigger.disabled") {
		return "Composio trigger disabled"
	}
	if toolkit != "" {
		return strings.ToUpper(toolkit[:1]) + toolkit[1:] + " Composio account needs reconnection"
	}
	return "Composio connector needs reconnection"
}

func composioWebhookDetail(sum composioWebhookSummary) string {
	fields := []string{
		"Composio reported a connector failure that can make agent skills and dashboard rows stale.",
	}
	if sum.Toolkit != "" {
		fields = append(fields, "Toolkit: "+sum.Toolkit)
	}
	if sum.AccountID != "" {
		fields = append(fields, "Connected account: "+sum.AccountID)
	}
	if sum.TriggerID != "" {
		fields = append(fields, "Trigger: "+sum.TriggerID)
	}
	if sum.Status != "" {
		fields = append(fields, "Status: "+sum.Status)
	}
	fields = append(fields, "Action: reconnect the account in Studio Connectors, then rerun any affected skills or refresh stale dashboard rows.")
	return strings.Join(fields, "\n")
}

func composioWebhookBody(sum composioWebhookSummary) string {
	lines := []string{
		"Event: " + nonEmpty(sum.Event, "unknown"),
	}
	if sum.Toolkit != "" {
		lines = append(lines, "Toolkit: "+sum.Toolkit)
	}
	if sum.AccountID != "" {
		lines = append(lines, "Connected account: "+sum.AccountID)
	}
	if sum.TriggerID != "" {
		lines = append(lines, "Trigger: "+sum.TriggerID)
	}
	if sum.EntityID != "" {
		lines = append(lines, "Entity: "+sum.EntityID)
	}
	if sum.Status != "" {
		lines = append(lines, "Status: "+sum.Status)
	}
	lines = append(lines,
		"Why it matters: Connector-backed agent skills may now be reading stale data or failing hidden tool calls.",
		"Action: Reconnect the account in Studio Connectors, then rerun affected skills or refresh the dashboard.",
	)
	return strings.Join(lines, "\n")
}

func composioWebhookSample(sum composioWebhookSummary) string {
	parts := []string{"event=" + nonEmpty(sum.Event, "unknown")}
	if sum.Toolkit != "" {
		parts = append(parts, "toolkit="+sum.Toolkit)
	}
	if sum.AccountID != "" {
		parts = append(parts, "account="+sum.AccountID)
	}
	if sum.Status != "" {
		parts = append(parts, "status="+sum.Status)
	}
	return strings.Join(parts, " ")
}

func firstWebhookString(v any, wanted ...string) string {
	set := make(map[string]struct{}, len(wanted))
	for _, key := range wanted {
		set[normalizeWebhookKey(key)] = struct{}{}
	}
	return firstWebhookStringInValue(v, set)
}

func firstWebhookStringInValue(v any, wanted map[string]struct{}) string {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, ok := wanted[normalizeWebhookKey(k)]; ok {
				if s := webhookValueString(x[k]); s != "" {
					return s
				}
			}
		}
		for _, k := range keys {
			if s := firstWebhookStringInValue(x[k], wanted); s != "" {
				return s
			}
		}
	case []any:
		for _, item := range x {
			if s := firstWebhookStringInValue(item, wanted); s != "" {
				return s
			}
		}
	}
	return ""
}

func firstNestedWebhookString(v any, containers []string, fields []string) string {
	containerSet := make(map[string]struct{}, len(containers))
	for _, key := range containers {
		containerSet[normalizeWebhookKey(key)] = struct{}{}
	}
	fieldSet := make(map[string]struct{}, len(fields))
	for _, key := range fields {
		fieldSet[normalizeWebhookKey(key)] = struct{}{}
	}
	return firstNestedWebhookStringInValue(v, containerSet, fieldSet)
}

func firstNestedWebhookStringInValue(v any, containers, fields map[string]struct{}) string {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, ok := containers[normalizeWebhookKey(k)]; !ok {
				continue
			}
			if s := firstWebhookStringInValue(x[k], fields); s != "" {
				return s
			}
		}
		for _, k := range keys {
			if s := firstNestedWebhookStringInValue(x[k], containers, fields); s != "" {
				return s
			}
		}
	case []any:
		for _, item := range x {
			if s := firstNestedWebhookStringInValue(item, containers, fields); s != "" {
				return s
			}
		}
	}
	return ""
}

func webhookValueString(v any) string {
	return webhookString(v)
}

func webhookString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return strings.TrimSpace(x.String())
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return ""
	}
}

func webhookMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func normalizeWebhookKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	k = strings.ReplaceAll(k, "_", "")
	k = strings.ReplaceAll(k, "-", "")
	return k
}

func webhookPayloadHash(payload map[string]any) string {
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}
