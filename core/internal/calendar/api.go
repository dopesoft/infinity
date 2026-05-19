// api.go - HTTP endpoints Studio calls for calendar actions.
//
// Three routes:
//
//   POST /api/calendar/events/{id}/respond   body: { response }
//   POST /api/calendar/accounts/{id}/sync    on-demand re-sync trigger
//   GET  /api/calendar/accounts              list connected google calendars
//
// /respond goes through runs.Track so the Studio spinner persists across
// navigation/refresh per the server-tracked progress rule.

package calendar

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dopesoft/infinity/core/internal/runs"
)

// API exposes the HTTP surface. Construct in serve.go after the Syncer
// (and connectors.Cache for the accounts list) are ready.
type API struct {
	syncer   *Syncer
	listAcct func() []AccountDTO
}

// AccountDTO is the shape Studio renders in "Calendar accounts."
type AccountDTO struct {
	ID          string `json:"id"`
	Alias       string `json:"alias,omitempty"`
	Email       string `json:"email,omitempty"`
	LastSyncAt  string `json:"last_sync_at,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	SyncedCount int    `json:"synced_count,omitempty"`
}

// NewAPI builds the API. listAcct may be nil; the /accounts endpoint
// then returns an empty list.
func NewAPI(syncer *Syncer, listAcct func() []AccountDTO) *API {
	return &API{syncer: syncer, listAcct: listAcct}
}

// Routes mounts the calendar handlers on mux. No-op when syncer is nil.
func (a *API) Routes(mux *http.ServeMux) {
	if a == nil || a.syncer == nil {
		return
	}
	mux.HandleFunc("/api/calendar/events/", a.handleEvents)
	mux.HandleFunc("/api/calendar/accounts", a.handleAccountsList)
	mux.HandleFunc("/api/calendar/accounts/", a.handleAccountAction)
}

// handleEvents routes POST /api/calendar/events/{id}/respond.
func (a *API) handleEvents(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/calendar/events/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	eventID, action := parts[0], parts[1]
	if eventID == "" {
		http.NotFound(w, r)
		return
	}
	switch action {
	case "respond":
		a.handleRespond(w, r, eventID)
	default:
		http.NotFound(w, r)
	}
}

func (a *API) handleRespond(w http.ResponseWriter, r *http.Request, eventID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	resp := Response(strings.TrimSpace(body.Response))
	if !resp.Valid() {
		http.Error(w, "response must be accepted|declined|tentative", http.StatusBadRequest)
		return
	}

	var ev *Event
	err := runs.Track(r.Context(), runs.Kind("calendar.respond"), eventID,
		"calendar respond: "+string(resp), runs.SourceManual,
		func(ctx context.Context) error {
			var rerr error
			ev, rerr = a.syncer.Respond(ctx, eventID, resp)
			return rerr
		})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "event": ev})
}

// handleAccountsList: GET /api/calendar/accounts
func (a *API) handleAccountsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out := []AccountDTO{}
	if a.listAcct != nil {
		out = a.listAcct()
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAccountAction: POST /api/calendar/accounts/{id}/sync
func (a *API) handleAccountAction(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/calendar/accounts/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	accountID, action := parts[0], parts[1]
	switch action {
	case "sync":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var res SyncResult
		err := runs.Track(r.Context(), runs.Kind("calendar.sync"), accountID,
			"calendar sync: "+accountID, runs.SourceManual,
			func(ctx context.Context) error {
				res = a.syncer.SyncNow(ctx, accountID, defaultCalendarID)
				if res.Error != "" {
					return &syncErr{msg: res.Error}
				}
				return nil
			})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, res)
	default:
		http.NotFound(w, r)
	}
}

// syncErr lets runs.Track see a string error from SyncResult.
type syncErr struct{ msg string }

func (e *syncErr) Error() string { return e.msg }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
