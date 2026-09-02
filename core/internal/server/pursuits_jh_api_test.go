package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/pursuits/jh"
)

// Every one of these runs against a pool that points nowhere, which is the
// point: the guards below have to fire BEFORE any database work, so a malformed
// or misdirected request can never reach a query. If one of them were removed
// the test would fail on the connection rather than quietly pass.
// unreachableServer lives in pursuits_pc_api_test.go — same package, one copy.

func TestPursuitsJHRequiresADatabase(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/pursuits/jh/state?pursuit_id=x", nil)
	(&Server{}).handlePursuitsJH(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	// Honest about WHY the board is empty. A cockpit that cannot reach the
	// database is not a board with nothing on it, and must never read as one.
	if !strings.Contains(rr.Body.String(), "database") {
		t.Fatalf("a missing pool must say so, got %s", rr.Body.String())
	}
}

// The cockpit read is a GET. This route is registered as a prefix, so a POST
// arriving at it must be refused with the allowed verb named, never accepted
// and interpreted as some other operation. The write endpoints are a separate
// pass; until they exist, a write verb here has nowhere legitimate to land.
func TestPursuitsJHRejectsTheWrongMethod(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(method, "/api/pursuits/jh/state?pursuit_id=x",
				strings.NewReader("{}"))
			unreachableServer(t).handlePursuitsJH(rr, req)

			if rr.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s on the read path = %d, want 405", method, rr.Code)
			}
			if got := rr.Header().Get("Allow"); got != "GET" {
				t.Fatalf("Allow header = %q, want GET", got)
			}
		})
	}
}

// pursuit_id is what scopes the read to one hunt. A request without it must be
// refused before any store call — never defaulted to "the first job_hunt
// pursuit", which would show one board's roles, contacts and salary bands under
// another.
func TestPursuitsJHRequiresAPursuitID(t *testing.T) {
	for _, path := range []string{"/api/pursuits/jh/state", "/api/pursuits/jh/state?pursuit_id=%20"} {
		t.Run(path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			unreachableServer(t).handlePursuitsJH(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), "pursuit_id required") {
				t.Fatalf("body = %s, want it to name the missing field", rr.Body.String())
			}
		})
	}
}

// The route is a prefix, so anything after /api/pursuits/jh/ reaches this
// handler. Only "state" exists today; a typo or a write path that has not been
// built yet must 404 rather than fall through to the cockpit read, which would
// answer a request for something that does not exist with a 200.
func TestPursuitsJHRejectsAnUnknownAction(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/pursuits/jh/stat?pursuit_id=x", nil)
	unreachableServer(t).handlePursuitsJH(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "unknown action") {
		t.Fatalf("body = %s, want it to name the unknown action", rr.Body.String())
	}
}

// writeJHError is the single place store errors become status codes. Pointing
// this route at an ORDINARY pursuit is the case that matters most: it must be a
// loud 409, never a 200 carrying an empty board, so the caller learns it aimed
// at the wrong surface instead of concluding the hunt has nothing in it.
func TestWriteJHErrorMapsHonestStatuses(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{
			name:       "an ordinary pursuit is a conflict, not an empty board",
			err:        jh.ErrNotJobHunt,
			wantStatus: http.StatusConflict,
			wantBody:   "job_hunt",
		},
		{
			name:       "a missing pursuit is a 404",
			err:        jh.ErrNoPursuit,
			wantStatus: http.StatusNotFound,
			wantBody:   "not found",
		},
		{
			name:       "anything else is a 400 carrying the real reason",
			err:        fmt.Errorf("list roles: connection refused"),
			wantStatus: http.StatusBadRequest,
			wantBody:   "connection refused",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeJHError(rr, tt.err)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			var body map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not json: %v (%s)", err, rr.Body.String())
			}
			if !strings.Contains(body["error"], tt.wantBody) {
				t.Fatalf("error = %q, want it to mention %q", body["error"], tt.wantBody)
			}
			// An error response must never be mistakable for a cockpit.
			if _, ok := body["summary"]; ok {
				t.Fatal("an error response must not carry a cockpit shape")
			}
		})
	}
}

// The store wraps almost everything with fmt.Errorf("...: %w", err), so a
// mapper comparing with == would downgrade every wrong-experience rejection to
// a generic 400 and the caller would never learn it aimed at the wrong pursuit.
func TestWriteJHErrorSeesThroughWrapping(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJHError(rr, fmt.Errorf("list roles: %w", jh.ErrNotJobHunt))
	if rr.Code != http.StatusConflict {
		t.Fatalf("a wrapped ErrNotJobHunt = %d, want 409", rr.Code)
	}

	rr = httptest.NewRecorder()
	writeJHError(rr, fmt.Errorf("load pursuit: %w", jh.ErrNoPursuit))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("a wrapped ErrNoPursuit = %d, want 404", rr.Code)
	}
}
