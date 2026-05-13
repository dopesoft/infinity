package push

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// API binds the push store + sender to HTTP routes consumed by Studio.
// Construct via NewAPI and call Routes(mux) from server boot.
type API struct {
	Store  *Store
	Sender *Sender
	Logger *slog.Logger
}

func NewAPI(store *Store, sender *Sender, logger *slog.Logger) *API {
	if logger == nil {
		logger = slog.Default()
	}
	return &API{Store: store, Sender: sender, Logger: logger}
}

// Routes registers /api/push/* on the provided mux.
//
//	GET  /api/push/vapid       — public key for the browser to subscribe
//	POST /api/push/subscribe   — register a new endpoint
//	POST /api/push/unsubscribe — remove an endpoint
//	GET  /api/push/devices     — list active devices for the Settings UI
//	POST /api/push/test        — send a synthetic notification
func (a *API) Routes(mux *http.ServeMux) {
	if a == nil {
		return
	}
	mux.HandleFunc("/api/push/vapid", a.handleVAPID)
	mux.HandleFunc("/api/push/subscribe", a.handleSubscribe)
	mux.HandleFunc("/api/push/unsubscribe", a.handleUnsubscribe)
	mux.HandleFunc("/api/push/devices", a.handleDevices)
	mux.HandleFunc("/api/push/test", a.handleTest)
}

// ── GET /api/push/vapid ───────────────────────────────────────────────────
func (a *API) handleVAPID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	key := ""
	if a.Sender != nil {
		key = a.Sender.PublicKey()
	}
	if key == "" {
		// 200 with empty body, not 503 — Studio uses absence to render
		// "push not configured" instead of throwing on a load error.
		writeJSON(w, http.StatusOK, map[string]any{"publicKey": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"publicKey": key})
}

// ── POST /api/push/subscribe ──────────────────────────────────────────────
//
// Body:
//
//	{
//	  "endpoint": "https://fcm.googleapis.com/fcm/send/abc123",
//	  "keys":     { "p256dh": "...", "auth": "..." },
//	  "userAgent": "Mozilla/...",
//	  "label":    "optional human name"
//	}
func (a *API) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.Store == nil {
		writeErr(w, http.StatusServiceUnavailable, "push not configured")
		return
	}
	var body struct {
		Endpoint  string `json:"endpoint"`
		Keys      struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
		UserAgent string `json:"userAgent"`
		Label     string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Endpoint == "" || body.Keys.P256dh == "" || body.Keys.Auth == "" {
		writeErr(w, http.StatusBadRequest, "endpoint + keys.p256dh + keys.auth required")
		return
	}
	label := body.Label
	if label == "" {
		label = labelFromUA(body.UserAgent)
	}
	sub, err := a.Store.Upsert(r.Context(), Subscription{
		Endpoint:  body.Endpoint,
		P256dh:    body.Keys.P256dh,
		AuthKey:   body.Keys.Auth,
		UserAgent: body.UserAgent,
		Label:     label,
	})
	if err != nil {
		a.Logger.Error("push: upsert", "err", err)
		writeErr(w, http.StatusInternalServerError, "store failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"subscription": map[string]any{
			"id":       sub.ID,
			"endpoint": sub.Endpoint,
			"label":    sub.Label,
		},
	})
}

// ── POST /api/push/unsubscribe ────────────────────────────────────────────
//
// Body: { "endpoint": "https://..." }
func (a *API) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.Store == nil {
		writeErr(w, http.StatusServiceUnavailable, "push not configured")
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Endpoint == "" {
		writeErr(w, http.StatusBadRequest, "endpoint required")
		return
	}
	if err := a.Store.Delete(r.Context(), body.Endpoint); err != nil {
		a.Logger.Error("push: delete", "err", err)
		writeErr(w, http.StatusInternalServerError, "delete failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ── GET /api/push/devices ────────────────────────────────────────────────
func (a *API) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.Store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"devices": []any{}})
		return
	}
	subs, err := a.Store.All(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	out := make([]map[string]any, 0, len(subs))
	for _, s := range subs {
		out = append(out, map[string]any{
			"id":         s.ID,
			"endpoint":   truncEndpoint(s.Endpoint),
			"label":      s.Label,
			"userAgent":  s.UserAgent,
			"revoked":    s.Revoked,
			"lastSeenAt": s.LastSeenAt,
			"createdAt":  s.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

// ── POST /api/push/test ───────────────────────────────────────────────────
//
// Sends a synthetic notification to all active devices so the boss can
// confirm end-to-end delivery (Studio → Core → FCM/APNs → device).
//
// Optional body:
//
//	{ "title": "...", "body": "...", "url": "/dashboard" }
//
// All fields optional; sensible defaults applied.
func (a *API) handleTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.Sender == nil || !a.Sender.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "push not configured")
		return
	}
	var body struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		URL   string `json:"url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // tolerate empty
	if body.Title == "" {
		body.Title = "Hello from Jarvis"
	}
	if body.Body == "" {
		body.Body = "Push notifications are working end-to-end."
	}
	if body.URL == "" {
		body.URL = "/"
	}
	results := a.Sender.Notify(r.Context(), Notification{
		Title: body.Title,
		Body:  body.Body,
		URL:   body.URL,
		Tag:   "push-test",
		Kind:  "test",
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"sent":    len(results),
		"results": results,
	})
}

// ── helpers ───────────────────────────────────────────────────────────────

// labelFromUA derives a friendly device label from the user-agent
// string. Doesn't try to be perfect — just enough that the Notifications
// settings page can show "Safari on iPhone" vs "Chrome on macOS" without
// the boss having to set it manually.
func labelFromUA(ua string) string {
	u := strings.ToLower(ua)
	switch {
	case strings.Contains(u, "iphone"):
		return "Safari on iPhone"
	case strings.Contains(u, "ipad"):
		return "Safari on iPad"
	case strings.Contains(u, "edg/"):
		return "Edge"
	case strings.Contains(u, "chrome/") && strings.Contains(u, "mac"):
		return "Chrome on macOS"
	case strings.Contains(u, "chrome/"):
		return "Chrome"
	case strings.Contains(u, "firefox/"):
		return "Firefox"
	case strings.Contains(u, "safari/") && strings.Contains(u, "mac"):
		return "Safari on macOS"
	case strings.Contains(u, "safari/"):
		return "Safari"
	default:
		return "Browser"
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
