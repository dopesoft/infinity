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

// The cockpit read is a GET and the writes are POSTs. A write verb arriving at
// the read path must be refused with the allowed verb named, never coerced into
// the other operation.
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
// handler. A suffix that is neither the read nor a known write must 404 rather
// than fall through to the cockpit read, which would answer a request for
// something that does not exist with a 200 and a board.
//
// Both verbs are checked: the 404 has to come BEFORE the method check, or a
// typo would read as "wrong verb" and send the caller looking for a request
// they could rephrase into working.
func TestPursuitsJHRejectsAnUnknownAction(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/pursuits/jh/stat?pursuit_id=x"},
		{http.MethodPost, "/api/pursuits/jh/roles"},
		{http.MethodPost, "/api/pursuits/jh/role/stages"},
		{http.MethodPost, "/api/pursuits/jh/"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
			unreachableServer(t).handlePursuitsJH(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), "unknown action") {
				t.Fatalf("body = %s, want it to name the unknown action", rr.Body.String())
			}
		})
	}
}

// The HTTP surface routes on the path suffix and the agent tool routes on the
// action enum, and both hand the result to the same jh.Store.Apply switch. The
// route is registered as a prefix, so the suffix IS the action: if the two ever
// disagree, one caller silently 404s while the other works.
func TestEveryJHWriteActionIsReachableAsAPathSuffix(t *testing.T) {
	const prefix = "/api/pursuits/jh/"
	for _, action := range jh.WriteActions() {
		got := strings.TrimPrefix(prefix+action, prefix)
		if got != action {
			t.Fatalf("path %q yields action %q, want %q", prefix+action, got, action)
		}
		if !jh.IsWriteAction(got) {
			t.Fatalf("action %q parsed from a path is not accepted by Apply", got)
		}
	}
	// "state" is the read and must NOT be a write action, or a GET would be
	// routed into the mutation switch.
	if jh.IsWriteAction("state") {
		t.Fatal(`"state" is the read path and must never be a write action`)
	}
}

// A write arriving as a GET must be refused. Without this a link, a browser
// prefetch or a crawler could move a role between stages or mark outreach sent.
func TestPursuitsJHRejectsAGetOnEveryWritePath(t *testing.T) {
	for _, action := range jh.WriteActions() {
		t.Run(action, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/pursuits/jh/"+action, nil)
			unreachableServer(t).handlePursuitsJH(rr, req)

			if rr.Code != http.StatusMethodNotAllowed {
				t.Fatalf("GET on a write path = %d, want 405", rr.Code)
			}
			if got := rr.Header().Get("Allow"); got != "POST" {
				t.Fatalf("Allow header = %q, want POST", got)
			}
		})
	}
}

// pursuit_id scopes every write to one hunt, exactly as it scopes the read. A
// write without it must be refused before any store call - never defaulted to
// "the first job_hunt pursuit", which would file another board's roles,
// contacts and salary bands onto this one.
func TestEveryJHWriteRequiresAPursuitID(t *testing.T) {
	for _, action := range jh.WriteActions() {
		t.Run(action, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/pursuits/jh/"+action,
				strings.NewReader(`{"company":"Acme","stage":"applied"}`))
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

// A write body that is not JSON must be rejected before any store call, so a
// truncated or malformed request can never be interpreted as a partial write.
func TestPursuitsJHRejectsMalformedJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/pursuits/jh/"+jh.ActionRole,
		strings.NewReader("{not json"))
	unreachableServer(t).handlePursuitsJH(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid json") {
		t.Fatalf("body = %s, want it to name the parse failure", rr.Body.String())
	}
}

// A value outside a column's vocabulary must be refused with the accepted
// values named, and refused BEFORE the database is touched - the pool here
// points nowhere, so a check that ran after the pursuit lookup would fail on
// the connection instead of passing.
//
// Naming the alternatives is the part that matters. A caller told only that
// "sourced" is wrong has to go read the schema; one told the five sources that
// exist fixes it on the spot.
func TestEveryJHWriteRejectsAnInvalidEnumValue(t *testing.T) {
	tests := []struct {
		action  string
		body    string
		wantAny []string
	}{
		{
			action:  jh.ActionRole,
			body:    `{"pursuit_id":"p","company":"Acme","role_title":"Head of Product","source":"carrier_pigeon"}`,
			wantAny: jh.RoleSources(),
		},
		{
			action:  jh.ActionRole,
			body:    `{"pursuit_id":"p","company":"Acme","role_title":"Head of Product","source":"linkedin","stage":"ghosted"}`,
			wantAny: jh.RoleStages(),
		},
		{
			action:  jh.ActionRoleStage,
			body:    `{"pursuit_id":"p","role_id":"r","stage":"ghosted"}`,
			wantAny: jh.RoleStages(),
		},
		{
			action:  jh.ActionCorpus,
			body:    `{"pursuit_id":"p","theme":"reorg","question":"q","answer":"a","source":"telepathy"}`,
			wantAny: jh.CorpusSources(),
		},
		{
			action:  jh.ActionContact,
			body:    `{"pursuit_id":"p","name":"Dana Reyes","status":"ignoring_me"}`,
			wantAny: jh.ContactStatuses(),
		},
		{
			action:  jh.ActionContactStatus,
			body:    `{"pursuit_id":"p","contact_id":"c","status":"ignoring_me"}`,
			wantAny: jh.ContactStatuses(),
		},
		{
			action:  jh.ActionArtifact,
			body:    `{"pursuit_id":"p","role_id":"r","title":"Resume","kind":"haiku"}`,
			wantAny: jh.ArtifactKinds(),
		},
		{
			action:  jh.ActionArtifact,
			body:    `{"pursuit_id":"p","role_id":"r","title":"Resume","kind":"resume","status":"posted"}`,
			wantAny: jh.ArtifactStatuses(),
		},
		{
			action:  jh.ActionArtifactStatus,
			body:    `{"pursuit_id":"p","artifact_id":"a","status":"posted"}`,
			wantAny: jh.ArtifactStatuses(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.action+" "+tt.body, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/pursuits/jh/"+tt.action,
				strings.NewReader(tt.body))
			unreachableServer(t).handlePursuitsJH(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rr.Code, rr.Body.String())
			}
			for _, accepted := range tt.wantAny {
				if !strings.Contains(rr.Body.String(), accepted) {
					t.Fatalf("rejection does not name the accepted value %q: %s",
						accepted, rr.Body.String())
				}
			}
		})
	}
}

// An action whose target id is missing must be refused too, and for the same
// reason: without it the store would have nothing to key the UPDATE on, and a
// caller that omitted it should learn which id it forgot.
func TestJHStatusWritesRequireTheirTargetID(t *testing.T) {
	tests := []struct{ action, body, wantField string }{
		{jh.ActionRoleStage, `{"pursuit_id":"p","stage":"applied"}`, "role_id required"},
		{jh.ActionContactStatus, `{"pursuit_id":"p","status":"sent"}`, "contact_id required"},
		{jh.ActionArtifact, `{"pursuit_id":"p","kind":"resume","title":"Resume"}`, "role_id required"},
		{jh.ActionArtifactStatus, `{"pursuit_id":"p","status":"approved"}`, "artifact_id required"},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/pursuits/jh/"+tt.action,
				strings.NewReader(tt.body))
			unreachableServer(t).handlePursuitsJH(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), tt.wantField) {
				t.Fatalf("body = %s, want it to name %q", rr.Body.String(), tt.wantField)
			}
		})
	}
}

// The write payload embeds jh.WriteRequest alongside pursuit_id. Embedding only
// promotes fields if the struct is anonymous, so a refactor to a named field
// would silently drop every value on the floor: the request would still parse,
// still return 200, and file a role with no company.
func TestPursuitsJHWritePayloadPromotesEmbeddedFields(t *testing.T) {
	var body struct {
		PursuitID string `json:"pursuit_id"`
		jh.WriteRequest
	}
	raw := `{
		"pursuit_id": "11111111-1111-1111-1111-111111111111",
		"company": "Acme",
		"role_title": "Head of Product",
		"source": "linkedin",
		"comp_min": 210000,
		"ghost_flags": ["reposted three times"],
		"external_id": "ln-4417"
	}`
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.PursuitID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("pursuit_id = %q", body.PursuitID)
	}
	if body.Company != "Acme" {
		t.Fatalf("company = %q, want the embedded WriteRequest field to be populated", body.Company)
	}
	if body.CompMin == nil || *body.CompMin != 210000 {
		t.Fatalf("comp_min = %v, want the stated salary band to survive decoding", body.CompMin)
	}
	if body.ExternalID != "ln-4417" {
		t.Fatalf("external_id = %q - without it a re-sweep files a second copy of this role", body.ExternalID)
	}
	if len(body.GhostFlags) != 1 {
		t.Fatalf("ghost_flags = %v", body.GhostFlags)
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
