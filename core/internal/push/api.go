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
	Prefs  *PrefsStore
	Logger *slog.Logger
}

func NewAPI(store *Store, sender *Sender, logger *slog.Logger) *API {
	if logger == nil {
		logger = slog.Default()
	}
	return &API{Store: store, Sender: sender, Logger: logger}
}

// SetPrefs wires the prefs store so /api/push/prefs returns and writes
// live data. nil-safe — if prefs is nil the GET returns defaults and the
// POST 503's.
func (a *API) SetPrefs(p *PrefsStore) {
	if a == nil {
		return
	}
	a.Prefs = p
}

// Routes registers /api/push/* on the provided mux.
//
//	GET  /api/push/vapid       - public key for the browser to subscribe
//	POST /api/push/subscribe   - register a new endpoint
//	POST /api/push/unsubscribe - remove an endpoint
//	GET  /api/push/devices     - list active devices for the Settings UI
//	POST /api/push/test        - send a synthetic notification
func (a *API) Routes(mux *http.ServeMux) {
	if a == nil {
		return
	}
	mux.HandleFunc("/api/push/vapid", a.handleVAPID)
	mux.HandleFunc("/api/push/subscribe", a.handleSubscribe)
	mux.HandleFunc("/api/push/unsubscribe", a.handleUnsubscribe)
	mux.HandleFunc("/api/push/devices", a.handleDevices)
	mux.HandleFunc("/api/push/test", a.handleTest)
	mux.HandleFunc("/api/push/prefs", a.handlePrefs)
}

// ── GET/POST /api/push/prefs ──────────────────────────────────────────────
//
// GET returns the merged prefs (defaults overlaid with whatever's saved).
// POST accepts a partial { "<kind>": true|false, ... } body and merges it
// over the current values; unknown kinds are ignored.
//
// Response shape (both verbs):
//
//	{
//	  "prefs": { "trust": true, "followup_new": false, ... },
//	  "kinds": [
//	    { "kind": "trust", "label": "Trust requests", "description": "..." },
//	    ...
//	  ]
//	}
//
// `kinds` carries the canonical render order + copy so Studio doesn't have
// to mirror the taxonomy in the FE.
func (a *API) handlePrefs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getPrefs(w, r)
	case http.MethodPost, http.MethodPut:
		a.savePrefs(w, r)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *API) getPrefs(w http.ResponseWriter, r *http.Request) {
	prefs := DefaultPrefs()
	if a.Prefs != nil {
		p, err := a.Prefs.Load(r.Context())
		if err != nil {
			a.Logger.Warn("push: prefs load", "err", err)
		} else {
			prefs = p
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"prefs": prefs,
		"kinds": kindCatalog(),
	})
}

func (a *API) savePrefs(w http.ResponseWriter, r *http.Request) {
	if a.Prefs == nil {
		writeErr(w, http.StatusServiceUnavailable, "prefs not configured")
		return
	}
	var body map[string]bool
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	incoming := Prefs{}
	for k, v := range body {
		incoming[PrefKind(k)] = v
	}
	merged, err := a.Prefs.Save(r.Context(), incoming)
	if err != nil {
		a.Logger.Error("push: prefs save", "err", err)
		writeErr(w, http.StatusInternalServerError, "save failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"prefs": merged,
		"kinds": kindCatalog(),
	})
}

// kindCatalog is the canonical taxonomy + copy Studio renders. Lives next
// to the kinds themselves so adding a new push source is one file edit:
// declare the PrefKind constant, add it to AllKinds(), append an entry
// here.
func kindCatalog() []map[string]string {
	descriptions := map[PrefKind][2]string{
		PrefTrust:            {"Approvals", "Jarvis needs a yes/no before running something destructive."},
		PrefAgentInitiative:  {"Agent initiative", "Jarvis surfaces something urgent on his own (notify tool fires)."},
		PrefFollowupNew:      {"New emails", "A new follow-up landed in the inbox (every connector-polled message)."},
		PrefCalendarUpcoming: {"Upcoming events", "A calendar event is starting within 15 minutes."},
		PrefRunStarted:       {"Run started", "A long server action (cron, skill, voyager…) began. Noisier — leave off if you only care about finishes."},
		PrefRunFinished:      {"Run finished", "A long server action wrapped up (ok or error)."},
	}
	out := make([]map[string]string, 0, len(AllKinds()))
	for _, k := range AllKinds() {
		copy := descriptions[k]
		out = append(out, map[string]string{
			"kind":        string(k),
			"label":       copy[0],
			"description": copy[1],
		})
	}
	return out
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
		// 200 with empty body, not 503 - Studio uses absence to render
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
// string. Doesn't try to be perfect - just enough that the Notifications
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
