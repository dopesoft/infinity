package jh

import (
	"strings"
	"testing"
)

// Apply's validation runs with no database, which is what these exercise: the
// Store here has a nil pool, so any check that ran after the pursuit lookup
// would panic instead of passing. That ordering is the point - a caller who
// fat-fingers a stage learns which stages exist without spending a round trip
// on it, and a malformed request never reaches a write.

// A bad value has to name the values that would have worked. "unknown role
// stage" on its own sends the caller to read the schema; the list fixes it on
// the spot, and it is the same list the store and the CHECK constraint use, so
// it cannot drift into advertising a stage the database would reject.
func TestValidateNamesTheAcceptedValues(t *testing.T) {
	tests := []struct {
		name     string
		action   string
		req      WriteRequest
		accepted []string
	}{
		{
			name:     "role source",
			action:   ActionRole,
			req:      WriteRequest{Company: "Acme", RoleTitle: "Head of Product", Source: "carrier_pigeon"},
			accepted: RoleSources(),
		},
		{
			name:     "starting stage on a new role",
			action:   ActionRole,
			req:      WriteRequest{Company: "Acme", RoleTitle: "Head of Product", Source: SourceLinkedIn, Stage: "ghosted"},
			accepted: RoleStages(),
		},
		{
			name:     "role stage",
			action:   ActionRoleStage,
			req:      WriteRequest{RoleID: "r", Stage: "ghosted"},
			accepted: RoleStages(),
		},
		{
			name:     "corpus source",
			action:   ActionCorpus,
			req:      WriteRequest{Theme: "reorg", Question: "q", Answer: "a", Source: "telepathy"},
			accepted: CorpusSources(),
		},
		{
			name:     "contact status on an upsert",
			action:   ActionContact,
			req:      WriteRequest{Name: "Dana Reyes", Status: "ignoring_me"},
			accepted: ContactStatuses(),
		},
		{
			name:     "contact status on a move",
			action:   ActionContactStatus,
			req:      WriteRequest{ContactID: "c", Status: "ignoring_me"},
			accepted: ContactStatuses(),
		},
		{
			name:     "artifact kind",
			action:   ActionArtifact,
			req:      WriteRequest{RoleID: "r", Title: "Resume", Kind: "haiku"},
			accepted: ArtifactKinds(),
		},
		{
			name:     "artifact status on a move",
			action:   ActionArtifactStatus,
			req:      WriteRequest{ArtifactID: "a", Status: "posted"},
			accepted: ArtifactStatuses(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.validate(tt.action)
			if err == nil {
				t.Fatal("a value outside the vocabulary was accepted")
			}
			for _, v := range tt.accepted {
				if !strings.Contains(err.Error(), v) {
					t.Fatalf("rejection does not name the accepted value %q: %v", v, err)
				}
			}
		})
	}
}

// A status action with no target id has nothing to key its UPDATE on. Refusing
// it here names the id that was forgotten; letting it through would produce a
// query that matches no row and an error about the row rather than the request.
func TestValidateRequiresTheTargetID(t *testing.T) {
	tests := []struct {
		action string
		req    WriteRequest
		want   string
	}{
		{ActionRoleStage, WriteRequest{Stage: StageApplied}, "role_id required"},
		{ActionContactStatus, WriteRequest{Status: ContactStatusSent}, "contact_id required"},
		{ActionArtifact, WriteRequest{Kind: ArtifactKindResume, Title: "Resume"}, "role_id required"},
		{ActionArtifactStatus, WriteRequest{Status: ArtifactStatusApproved}, "artifact_id required"},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			err := tt.req.validate(tt.action)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

// Blank means "leave it alone" everywhere the column is not required, which is
// what lets a thin re-sweep patch a role, or a caller add a contact, without
// having to restate every value a richer pass already stored. A validator that
// demanded them would turn every partial update into an error.
func TestValidateAcceptsAnAbsentOptionalValue(t *testing.T) {
	tests := []struct {
		name   string
		action string
		req    WriteRequest
	}{
		{"a role with no starting stage", ActionRole, WriteRequest{Company: "Acme", RoleTitle: "Head of Product", Source: SourceLinkedIn}},
		{"a corpus entry with no source", ActionCorpus, WriteRequest{Theme: "reorg", Question: "q", Answer: "a"}},
		{"a contact with no status", ActionContact, WriteRequest{Name: "Dana Reyes"}},
		{"a document with no status", ActionArtifact, WriteRequest{RoleID: "r", Kind: ArtifactKindResume, Title: "Resume"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.req.validate(tt.action); err != nil {
				t.Fatalf("a blank optional value was rejected: %v", err)
			}
		})
	}
}

// Apply is the chokepoint, so an action it does not handle must be refused as
// ErrUnknownAction and refused FIRST - before the pursuit lookup, so a typo
// reads as a typo rather than as whatever the database happens to say, and
// before any write can land. The nil pool proves the ordering: reaching the
// lookup would panic.
func TestApplyRejectsAnUnknownActionBeforeTouchingTheDatabase(t *testing.T) {
	s := &Store{}
	for _, action := range []string{"", "state", "roles", "role/stages", "checkin"} {
		if err := s.Apply(t.Context(), action, "p", WriteRequest{}); err != ErrUnknownAction {
			t.Fatalf("action %q: err = %v, want ErrUnknownAction", action, err)
		}
	}
}

// The HTTP path suffixes and the tool's action enum are the same strings, and
// IsWriteAction is what both are checked against. "state" is the read: if it
// ever became a write action, a GET would be routed into the mutation switch.
func TestWriteActionsAreConsistent(t *testing.T) {
	for _, action := range WriteActions() {
		if !IsWriteAction(action) {
			t.Fatalf("%q is advertised but not accepted", action)
		}
	}
	for _, notAnAction := range []string{"state", "checkin", "", "ROLE"} {
		if IsWriteAction(notAnAction) {
			t.Fatalf("%q must not be a write action", notAnAction)
		}
	}
	if len(WriteActions()) != 7 {
		t.Fatalf("WriteActions has %d entries; the API and the tool schema both enumerate them, so a new one needs a route and a description too", len(WriteActions()))
	}
}
