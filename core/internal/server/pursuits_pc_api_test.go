package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/pursuits/pc"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Every one of these runs without a pool, which is the point: the guards below
// have to fire BEFORE any database work, so a malformed or misdirected request
// can never reach a write.

func TestPursuitsPCRequiresADatabase(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/pursuits/pc/state?pursuit_id=x", nil)
	(&Server{}).handlePursuitsPC(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	// Honest about WHY it is empty. A cockpit that cannot reach the database is
	// not a cockpit with nothing in it, and must never read as one.
	if !strings.Contains(rr.Body.String(), "database") {
		t.Fatalf("a missing pool must say so, got %s", rr.Body.String())
	}
}

// unreachableServer builds a Server holding a real, non-nil pool that points
// nowhere. pgxpool connects lazily, so the pool exists without a database
// behind it - which is exactly what is needed to get PAST the 503 guard and
// exercise the request guards that must reject a call before it ever queries.
// Any test using this asserts a path that returns without touching the pool; if
// one of those guards were removed, the test would hang or fail on connection
// rather than quietly pass.
func unreachableServer(t *testing.T) *Server {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://nobody@127.0.0.1:1/nowhere")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return &Server{pool: pool}
}

// The read is a GET and the writes are POSTs. A write verb at the read path
// must be refused with the allowed verb named, never coerced into the other
// operation.
func TestPursuitsPCRejectsTheWrongMethod(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/pursuits/pc/state", strings.NewReader("{}"))
	unreachableServer(t).handlePursuitsPC(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST to the read path = %d, want 405", rr.Code)
	}
	if got := rr.Header().Get("Allow"); got != "GET" {
		t.Fatalf("Allow header = %q, want GET", got)
	}
}

// A write arriving as a GET must be refused too. Without this a link or a
// prefetch could trigger a mutation.
func TestPursuitsPCRejectsAGetOnAWritePath(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/pursuits/pc/"+pc.ActionReview, nil)
	unreachableServer(t).handlePursuitsPC(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET on a write path = %d, want 405", rr.Code)
	}
	if got := rr.Header().Get("Allow"); got != "POST" {
		t.Fatalf("Allow header = %q, want POST", got)
	}
}

// A write body that is not JSON must be rejected before any store call, so a
// truncated or malformed request can never be interpreted as a partial write.
func TestPursuitsPCRejectsMalformedJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/pursuits/pc/"+pc.ActionEvidence,
		strings.NewReader("{not json"))
	unreachableServer(t).handlePursuitsPC(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid json") {
		t.Fatalf("body = %s, want it to name the parse failure", rr.Body.String())
	}
}

// writePCError is the single place store errors become status codes. Pointing a
// coached-pursuit route at an ORDINARY pursuit is the case that matters most:
// it must be a loud 409, never a silent success and never a generic 400, so the
// caller learns it aimed at the wrong surface instead of assuming the write
// landed.
func TestWritePCErrorMapsHonestStatuses(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{
			name:       "an ordinary pursuit is a conflict, not a success",
			err:        pc.ErrNotPsychoCybernetics,
			wantStatus: http.StatusConflict,
			wantBody:   "psycho_cybernetics",
		},
		{
			name:       "a missing pursuit is a 404",
			err:        pc.ErrNoPursuit,
			wantStatus: http.StatusNotFound,
			wantBody:   "not found",
		},
		{
			name:       "an unknown action is a 404",
			err:        pc.ErrUnknownAction,
			wantStatus: http.StatusNotFound,
			wantBody:   "unknown action",
		},
		{
			name:       "anything else is a 400 carrying the real reason",
			err:        fmt.Errorf("proof label required"),
			wantStatus: http.StatusBadRequest,
			wantBody:   "proof label required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writePCError(rr, tt.err)

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
			if _, ok := body["guidance"]; ok {
				t.Fatal("an error response must not carry a cockpit shape")
			}
		})
	}
}

// A wrapped error still has to map to its real status. The store wraps almost
// everything with fmt.Errorf("...: %w", err), so a mapper that compared with ==
// would downgrade every ordinary-pursuit rejection to a generic 400.
func TestWritePCErrorSeesThroughWrapping(t *testing.T) {
	rr := httptest.NewRecorder()
	writePCError(rr, fmt.Errorf("save identity: %w", pc.ErrNotPsychoCybernetics))
	if rr.Code != http.StatusConflict {
		t.Fatalf("a wrapped ErrNotPsychoCybernetics = %d, want 409", rr.Code)
	}

	rr = httptest.NewRecorder()
	writePCError(rr, fmt.Errorf("load pursuit: %w", pc.ErrNoPursuit))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("a wrapped ErrNoPursuit = %d, want 404", rr.Code)
	}
}

// The HTTP surface routes on the path suffix and the agent tool routes on the
// action enum, and both hand the result to the same pc.Store.Apply switch. The
// route is registered as a prefix, so the suffix IS the action: if the two ever
// disagree, one caller silently 404s while the other works.
func TestEveryWriteActionIsReachableAsAPathSuffix(t *testing.T) {
	const prefix = "/api/pursuits/pc/"
	for _, action := range pc.WriteActions() {
		path := prefix + action
		got := strings.TrimPrefix(path, prefix)
		if got != action {
			t.Fatalf("path %q yields action %q, want %q", path, got, action)
		}
		if !pc.IsWriteAction(got) {
			t.Fatalf("action %q parsed from a path is not accepted by Apply", got)
		}
	}
	// "state" is the read and must NOT be a write action, or a GET would be
	// routed into the mutation switch.
	if pc.IsWriteAction("state") {
		t.Fatal(`"state" is the read path and must never be a write action`)
	}
}

// pursuit_id is what scopes every read and write to one programme. A request
// without it must be refused before any store call - never defaulted to "the
// first pursuit", which would show one programme's identity under another.
func TestPursuitsPCRequiresAPursuitID(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/pursuits/pc/state", nil)
		unreachableServer(t).handlePursuitsPC(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "pursuit_id required") {
			t.Fatalf("body = %s, want it to name the missing field", rr.Body.String())
		}
	})

	t.Run("write", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/pursuits/pc/"+pc.ActionMemory,
			strings.NewReader(`{"title":"Closed the room"}`))
		unreachableServer(t).handlePursuitsPC(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "pursuit_id required") {
			t.Fatalf("body = %s, want it to name the missing field", rr.Body.String())
		}
	})
}

// The write payload embeds pc.WriteRequest alongside pursuit_id. Embedding only
// promotes fields if the struct is anonymous, so a refactor to a named field
// would silently drop every answer on the floor: the request would still parse,
// still return 200, and write an empty session.
func TestPursuitsPCWritePayloadPromotesEmbeddedFields(t *testing.T) {
	var body struct {
		PursuitID string `json:"pursuit_id"`
		pc.WriteRequest
	}
	raw := `{
		"pursuit_id": "11111111-1111-1111-1111-111111111111",
		"kind": "morning",
		"answers": {"rehearsal": "The Thursday pricing call.", "proof_pledge": "Quote the full number"},
		"coach_note": "sized it down"
	}`
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.PursuitID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("pursuit_id = %q", body.PursuitID)
	}
	if body.Kind != "morning" {
		t.Fatalf("kind = %q, want the embedded WriteRequest field to be populated", body.Kind)
	}
	if body.CoachNote != "sized it down" {
		t.Fatalf("coach_note = %q", body.CoachNote)
	}
	if got, _ := body.Answers["proof_pledge"].(string); got != "Quote the full number" {
		t.Fatalf("answers.proof_pledge = %q - the pledge that becomes a tracked proof was dropped", got)
	}
}
