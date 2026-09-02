package jh

// The single read the Job Hunt cockpit is built from.
//
// One call returns the whole board: the pursuit header, the pipeline, the
// banked interview material, the people, the documents, a derived summary, and
// the vocabularies every one of those is constrained to. A cockpit assembled
// from five separate round trips can show a role in a stage the client does not
// have a column for, or a count that disagrees with the rows underneath it;
// assembling it here means the summary and the rows are read from the same
// moment and cannot contradict each other.
//
// Rule #1b: everything in this file is a MECHANIC. The counts are derived, not
// stored, so they cannot drift from the rows. The vocabularies are the same
// validator functions the writes are checked against, so a stage the store
// would reject can never appear as a column the UI offers. The judgment — which
// role is worth chasing, what the outreach says — is not here and never will
// be.

import (
	"context"
)

// Summary is the derived read of the board. Every field is computed from the
// rows in the same Cockpit, so it is a shortcut for the client rather than a
// second source of truth that could disagree with what is on screen.
type Summary struct {
	// TotalRoles counts every role on the pursuit, dead ones included. The
	// per-stage breakdown is what separates live pipeline from closed.
	TotalRoles int `json:"total_roles"`
	// RolesByStage carries a count for EVERY stage in RoleStages(), including
	// the ones at zero. A map that omitted empty stages would make an empty
	// kanban column indistinguishable from a stage the client had never heard
	// of, and the board would silently stop rendering it. JSON objects have no
	// order, which is why the ordered sequence lives in Vocabulary.RoleStages.
	RolesByStage map[string]int `json:"roles_by_stage"`
	// CorpusCount is how much interview material is banked. It is the number
	// that says whether a tailoring pass has anything real to draw on.
	CorpusCount int `json:"corpus_count"`
	// ContactsAwaitingReply counts the contacts sitting at 'sent': a message
	// went out and nothing has come back. This is the only count here that
	// carries an obligation, which is why it is surfaced on its own rather
	// than left inside a per-status map the client would have to index.
	ContactsAwaitingReply int `json:"contacts_awaiting_reply"`
	// ArtifactCount is how many documents have been produced across the board.
	ArtifactCount int `json:"artifact_count"`
	// ContactCount is the total number of people on the hunt, so the awaiting
	// figure above can be read as a proportion rather than a bare number.
	ContactCount int `json:"contact_count"`
}

// Vocabulary is every constrained value the board can hold, shipped with the
// board itself.
//
// This exists so the UI never hardcodes a stage, a source, a kind or a status.
// Those lists are enforced in three places already — a CHECK constraint, a
// validator in this package, and whatever renders the column headers — and the
// third one is the copy that silently rots: a stage added to the schema and the
// store still would not appear on the board, and a stage removed would leave a
// column that can never be filled. Sending the store's own lists over the wire
// makes the client's vocabulary the store's vocabulary by construction.
//
// The order is load-bearing for RoleStages: it is pipeline order, which is the
// left-to-right order of the kanban columns.
type Vocabulary struct {
	RoleStages       []string `json:"role_stages"`
	RoleSources      []string `json:"role_sources"`
	CorpusSources    []string `json:"corpus_sources"`
	ContactStatuses  []string `json:"contact_statuses"`
	ArtifactKinds    []string `json:"artifact_kinds"`
	ArtifactStatuses []string `json:"artifact_statuses"`
}

// NewVocabulary returns the accepted values for every constrained column,
// straight from the same functions the writes are validated against.
func NewVocabulary() Vocabulary {
	return Vocabulary{
		RoleStages:       RoleStages(),
		RoleSources:      RoleSources(),
		CorpusSources:    CorpusSources(),
		ContactStatuses:  ContactStatuses(),
		ArtifactKinds:    ArtifactKinds(),
		ArtifactStatuses: ArtifactStatuses(),
	}
}

// Cockpit is the whole Job Hunt board in one payload.
//
// Every slice is non-nil even when empty, because the stores return empty
// slices rather than nil, so a client can iterate without a null-guard and an
// empty board serialises as [] rather than null.
type Cockpit struct {
	Pursuit   PursuitHeader `json:"pursuit"`
	Roles     []Role        `json:"roles"`
	Corpus    []CorpusEntry `json:"corpus"`
	Contacts  []Contact     `json:"contacts"`
	Artifacts []Artifact    `json:"artifacts"`
	Summary   Summary       `json:"summary"`
	// Vocabulary is the accepted value set for every constrained field above.
	Vocabulary Vocabulary `json:"vocabulary"`
}

// Cockpit loads the entire board for one pursuit.
//
// Header runs first and its error is returned unwrapped, so a pursuit that does
// not exist reads as ErrNoPursuit and an ordinary pursuit pointed at this
// package reads as ErrNotJobHunt. Both are conditions the caller has to be told
// about rather than shown as an empty board: an empty-because-wrong-pursuit
// that rendered as empty-because-nothing-filed-yet is exactly the false-green
// this codebase forbids.
//
// The four reads run in sequence and any failure aborts. A partial cockpit
// would be worse than an error, because it would show a summary computed over
// the rows that happened to load and read as complete.
func (s *Store) Cockpit(ctx context.Context, pursuitID string) (Cockpit, error) {
	var c Cockpit

	header, err := s.Header(ctx, pursuitID)
	if err != nil {
		return c, err
	}
	roles, err := s.Roles(ctx, pursuitID)
	if err != nil {
		return c, err
	}
	corpus, err := s.CorpusEntries(ctx, pursuitID)
	if err != nil {
		return c, err
	}
	contacts, err := s.Contacts(ctx, pursuitID)
	if err != nil {
		return c, err
	}
	artifacts, err := s.Artifacts(ctx, pursuitID)
	if err != nil {
		return c, err
	}

	c.Pursuit = header
	c.Roles = roles
	c.Corpus = corpus
	c.Contacts = contacts
	c.Artifacts = artifacts
	c.Summary = summarise(roles, corpus, contacts, artifacts)
	c.Vocabulary = NewVocabulary()
	return c, nil
}

// summarise derives the board's counts from its rows.
//
// Split out from Cockpit so it can be exercised without a database: these are
// the numbers the boss reads at a glance, and a miscount here is a wrong number
// on a card rather than a visible failure.
func summarise(roles []Role, corpus []CorpusEntry, contacts []Contact, artifacts []Artifact) Summary {
	// Seeded with every known stage at zero before counting, so an empty
	// column is reported as empty rather than missing. Counting first and
	// filling gaps afterwards would leave the same hole for any stage that
	// happens to have no roles in it today.
	byStage := make(map[string]int, len(RoleStages()))
	for _, stage := range RoleStages() {
		byStage[stage] = 0
	}
	for _, r := range roles {
		// A stage the validators do not know about can only mean the database
		// moved ahead of this package. Counting it anyway keeps the totals
		// honest: TotalRoles and the sum of RolesByStage must always agree, or
		// the board quietly loses a card.
		byStage[r.Stage]++
	}

	awaiting := 0
	for _, c := range contacts {
		if c.OutreachStatus == ContactStatusSent {
			awaiting++
		}
	}

	return Summary{
		TotalRoles:            len(roles),
		RolesByStage:          byStage,
		CorpusCount:           len(corpus),
		ContactsAwaitingReply: awaiting,
		ArtifactCount:         len(artifacts),
		ContactCount:          len(contacts),
	}
}
