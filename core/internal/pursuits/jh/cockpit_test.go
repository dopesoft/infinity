package jh

import (
	"encoding/json"
	"testing"
)

// The summary is what the boss reads at a glance, so a miscount here is a wrong
// number on a card rather than a visible failure. summarise is pure, which is
// the whole reason it is split out of Cockpit: these invariants can be pinned
// without a database.

func TestSummaryCountsEveryStageIncludingTheEmptyOnes(t *testing.T) {
	roles := []Role{
		{Stage: StageDiscovered},
		{Stage: StageDiscovered},
		{Stage: StageApplied},
		{Stage: StageDead},
	}
	got := summarise(roles, nil, nil, nil)

	if got.TotalRoles != 4 {
		t.Fatalf("total_roles = %d, want 4", got.TotalRoles)
	}
	// Every stage must be present, including the ones at zero. Omitting them
	// makes an empty kanban column indistinguishable from a stage the client
	// never heard of, and the board silently stops rendering it.
	for _, stage := range RoleStages() {
		if _, ok := got.RolesByStage[stage]; !ok {
			t.Fatalf("stage %q is missing from roles_by_stage - an empty column would vanish from the board", stage)
		}
	}
	if got.RolesByStage[StageDiscovered] != 2 {
		t.Fatalf("discovered = %d, want 2", got.RolesByStage[StageDiscovered])
	}
	if got.RolesByStage[StageApplied] != 1 {
		t.Fatalf("applied = %d, want 1", got.RolesByStage[StageApplied])
	}
	if got.RolesByStage[StageInterviewing] != 0 {
		t.Fatalf("interviewing = %d, want 0", got.RolesByStage[StageInterviewing])
	}
}

// TotalRoles and the sum of the per-stage counts are the same fact reported two
// ways, and the board shows both. If they can disagree, a card is missing from
// a column while the header still counts it - which reads as a rendering bug
// rather than as the data problem it is. This holds even for a stage this
// package does not know about, which is why summarise counts unknown stages
// rather than dropping them.
func TestSummaryStageCountsAlwaysSumToTheTotal(t *testing.T) {
	roles := []Role{
		{Stage: StageDiscovered},
		{Stage: StageOffer},
		{Stage: "stage_added_to_the_schema_after_this_build"},
	}
	got := summarise(roles, nil, nil, nil)

	sum := 0
	for _, n := range got.RolesByStage {
		sum += n
	}
	if sum != got.TotalRoles {
		t.Fatalf("stage counts sum to %d but total_roles = %d - a role is missing from the board", sum, got.TotalRoles)
	}
}

// 'sent' is the only status that carries an obligation: a message went out and
// nothing came back. Counting anything else into it would tell the boss to
// chase people who already replied, or who were never written to.
func TestSummaryCountsOnlyContactsActuallyAwaitingAReply(t *testing.T) {
	contacts := []Contact{
		{OutreachStatus: ContactStatusSent},
		{OutreachStatus: ContactStatusSent},
		{OutreachStatus: ContactStatusReplied},
		{OutreachStatus: ContactStatusIdentified},
		{OutreachStatus: ContactStatusDrafted},
		{OutreachStatus: ContactStatusDead},
	}
	got := summarise(nil, nil, contacts, nil)

	if got.ContactsAwaitingReply != 2 {
		t.Fatalf("contacts_awaiting_reply = %d, want 2", got.ContactsAwaitingReply)
	}
	if got.ContactCount != 6 {
		t.Fatalf("contact_count = %d, want 6", got.ContactCount)
	}
}

func TestSummaryCountsCorpusAndArtifacts(t *testing.T) {
	got := summarise(nil,
		[]CorpusEntry{{Theme: "leadership"}, {Theme: "conflict"}},
		nil,
		[]Artifact{{Kind: ArtifactKindResume}})

	if got.CorpusCount != 2 {
		t.Fatalf("corpus_count = %d, want 2", got.CorpusCount)
	}
	if got.ArtifactCount != 1 {
		t.Fatalf("artifact_count = %d, want 1", got.ArtifactCount)
	}
}

// The vocabulary shipped to the client has to BE the store's vocabulary. If a
// stage the store accepts is missing from the payload the board has no column
// to put it in, and if the payload carries one the store rejects, the UI offers
// a move that can only fail. Comparing against the validators is what keeps the
// two from drifting apart.
func TestVocabularyMatchesWhatTheStoreWillAccept(t *testing.T) {
	v := NewVocabulary()

	for _, stage := range v.RoleStages {
		if !IsValidRoleStage(stage) {
			t.Fatalf("vocabulary offers stage %q that the store would reject", stage)
		}
	}
	for _, source := range v.RoleSources {
		if !IsValidRoleSource(source) {
			t.Fatalf("vocabulary offers role source %q that the store would reject", source)
		}
	}
	for _, source := range v.CorpusSources {
		if !IsValidCorpusSource(source) {
			t.Fatalf("vocabulary offers corpus source %q that the store would reject", source)
		}
	}
	for _, status := range v.ContactStatuses {
		if !IsValidContactStatus(status) {
			t.Fatalf("vocabulary offers contact status %q that the store would reject", status)
		}
	}
	for _, kind := range v.ArtifactKinds {
		if !IsValidArtifactKind(kind) {
			t.Fatalf("vocabulary offers artifact kind %q that the store would reject", kind)
		}
	}
	for _, status := range v.ArtifactStatuses {
		if !IsValidArtifactStatus(status) {
			t.Fatalf("vocabulary offers artifact status %q that the store would reject", status)
		}
	}

	// Stage order is the left-to-right order of the kanban columns, so the
	// payload must preserve the pipeline sequence rather than sort it.
	want := RoleStages()
	if len(v.RoleStages) != len(want) {
		t.Fatalf("vocabulary carries %d stages, store has %d", len(v.RoleStages), len(want))
	}
	for i := range want {
		if v.RoleStages[i] != want[i] {
			t.Fatalf("stage %d = %q, want %q - the columns would render out of pipeline order",
				i, v.RoleStages[i], want[i])
		}
	}
}

// An empty board must serialise as empty arrays, not nulls. A client that has
// to null-guard every collection is a client that will forget one, and the
// board renders nothing at all on the day the hunt starts.
func TestEmptyCockpitSerialisesAsArraysNotNulls(t *testing.T) {
	c := Cockpit{
		Roles:      []Role{},
		Corpus:     []CorpusEntry{},
		Contacts:   []Contact{},
		Artifacts:  []Artifact{},
		Summary:    summarise(nil, nil, nil, nil),
		Vocabulary: NewVocabulary(),
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]json.RawMessage
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"roles", "corpus", "contacts", "artifacts"} {
		if string(back[key]) != "[]" {
			t.Fatalf("%s = %s, want []", key, back[key])
		}
	}
}
