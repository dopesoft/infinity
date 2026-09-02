package jh

import (
	"strings"
	"testing"
	"time"
)

func ptrInt(n int) *int              { return &n }
func ptrStr(s string) *string        { return &s }
func ptrTime(t time.Time) *time.Time { return &t }

// fullCockpit is a board with something in every part of it, so a section that
// silently stops being written shows up as a failing assertion rather than as a
// conversation that has forgotten half the hunt.
func fullCockpit() Cockpit {
	sent := time.Date(2026, time.August, 20, 9, 0, 0, 0, time.UTC)
	roles := []Role{
		{
			ID: "r1", Company: "Northwind", RoleTitle: "Head of Product",
			Source: SourceLinkedIn, Location: "Remote", URL: "https://example.com/nw",
			CompMin: ptrInt(210000), CompMax: ptrInt(260000),
			FitScore: ptrInt(82), FitReasoning: "Remote, product org of 40, reports to the CEO.",
			Stage: StageTailoring, Notes: "Recruiter said the band is soft.",
		},
		{
			ID: "r2", Company: "Ghostly", RoleTitle: "VP Product",
			Source: SourceBuiltIn, CompText: "Competitive",
			GhostScore: ptrInt(71), GhostFlags: []string{"reposted_four_times", "no_named_manager"},
			Stage: StageDiscovered,
		},
	}
	return Cockpit{
		Pursuit: PursuitHeader{ID: "p1", Title: "Head of Product search", Experience: ExperienceJobHunt},
		Roles:   roles,
		Corpus: []CorpusEntry{
			{ID: "c1", Theme: "Leading through a rewrite", Question: "Tell me about a hard call",
				Answer: "Cut two of the four bets and said so in the all hands.", Source: CorpusSourceInterview},
		},
		Contacts: []Contact{
			{ID: "k1", RoleID: ptrStr("r1"), Name: "Dana Reid", Title: "CTO", Company: "Northwind",
				OutreachStatus: ContactStatusSent, OutreachSentAt: ptrTime(sent),
				LastMessage: "Asked whether the role owns pricing."},
			{ID: "k2", Name: "Sam Okafor", Title: "Recruiter", Company: "Ghostly",
				OutreachStatus: ContactStatusIdentified},
		},
		Artifacts: []Artifact{
			{ID: "a1", RoleID: "r1", Kind: ArtifactKindResume, Title: "Resume for Northwind",
				Status: ArtifactStatusDraft, ArtifactID: ptrStr("doc-1")},
			{ID: "a2", RoleID: "r1", Kind: ArtifactKindCoverLetter, Title: "Cover letter for Northwind",
				Status: ArtifactStatusDraft},
		},
		Summary:    summarise(roles, nil, nil, nil),
		Vocabulary: NewVocabulary(),
	}
}

// Turn one has to land the agent in a conversation that already knows the whole
// board. If any of these fall out, "tailor my resume for this one" opens with
// the agent asking which one, which is the exact failure the seeding exists to
// prevent.
func TestFormatChatContextCarriesTheWholeBoard(t *testing.T) {
	out := FormatChatContext(fullCockpit())

	for _, want := range []string{
		"Head of Product search",     // the pursuit
		"Northwind",                  // a role
		"role id: r1",                // addressable for a write-back
		"Tailoring",                  // the stage it sits in
		"[tailoring]",                // and the key the store accepts
		"fit: 82 out of 100",         // the fit score
		"Remote, product org of 40",  // the reasoning behind it
		"reposted_four_times",        // the ghost signals
		"Leading through a rewrite",  // banked interview material
		"Dana Reid",                  // the people
		"message sent, nothing back", // where the outreach got to
		"Resume for Northwind",       // the documents
		"draft, not approved",        // and whether they are cleared to go out
		"pursuit_jh_write",           // how to write a decision back
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("context block is missing %q; the conversation would open blind to it.\n---\n%s", want, out)
		}
	}
}

// A posting that stated no band has no band. Rendering a missing salary as a
// number is the one lie this block must never tell, because the agent will
// repeat it back to the boss as though the employer had said it.
func TestFormatChatContextNeverInventsASalary(t *testing.T) {
	c := fullCockpit()
	c.Roles = []Role{{ID: "r1", Company: "Northwind", RoleTitle: "Head of Product", Stage: StageDiscovered}}
	c.Summary = summarise(c.Roles, nil, nil, nil)

	out := FormatChatContext(c)
	if strings.Contains(out, "$0") {
		t.Fatalf("a role with no stated band rendered a salary of zero:\n%s", out)
	}
	if !strings.Contains(out, "pay: not listed") {
		t.Fatalf("a role with no stated band must say so plainly:\n%s", out)
	}
	// The same rule for a score nothing has computed yet: unscored must not
	// read as scored zero.
	if !strings.Contains(out, "fit: not scored yet") {
		t.Fatalf("an unscored role must not read as one that scored zero:\n%s", out)
	}
}

// An empty board must read as empty, never as a board that failed to load. The
// three "nothing yet" sentences are what stop the agent answering as though the
// hunt were underway.
func TestFormatChatContextSaysWhenTheBoardIsEmpty(t *testing.T) {
	out := FormatChatContext(Cockpit{
		Pursuit:    PursuitHeader{ID: "p1", Title: "Head of Product search"},
		Summary:    summarise(nil, nil, nil, nil),
		Vocabulary: NewVocabulary(),
	})

	for _, want := range []string{
		"Nothing is on the board yet",
		"No interview material is banked yet",
		"Nobody has been contacted yet",
		"No resumes, cover letters or positioning reads have been written yet",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("empty board is missing %q:\n%s", want, out)
		}
	}
}

// A stage the database grew after this build still has to appear. A role that
// fell out of the context block is a role the agent answers as if it did not
// exist, which is the same class of failure as a card vanishing from a column.
func TestFormatChatContextKeepsRolesAtUnknownStages(t *testing.T) {
	c := fullCockpit()
	c.Roles = append(c.Roles, Role{
		ID: "r3", Company: "Latecomer", RoleTitle: "Head of Product",
		Stage: "stage_added_to_the_schema_after_this_build",
	})
	c.Summary = summarise(c.Roles, nil, nil, nil)

	out := FormatChatContext(c)
	if !strings.Contains(out, "Latecomer") {
		t.Fatalf("a role at an unknown stage vanished from the context block:\n%s", out)
	}
	if !strings.Contains(out, "Stage added to the schema after this build") {
		t.Fatalf("an unknown stage must still read as a phrase, not be dropped:\n%s", out)
	}
}

// The agent writes stages, statuses and kinds back through pursuit_jh_write.
// Without the accepted values in front of it, a write is guesswork: the model
// picks a word the store rejects and the boss watches a move that silently did
// not happen.
func TestFormatChatContextCarriesTheAcceptedValues(t *testing.T) {
	out := FormatChatContext(fullCockpit())
	for _, stage := range RoleStages() {
		if !strings.Contains(out, stage) {
			t.Fatalf("accepted stage %q is not in the context block:\n%s", stage, out)
		}
	}
	for _, status := range ContactStatuses() {
		if !strings.Contains(out, status) {
			t.Fatalf("accepted outreach status %q is not in the context block:\n%s", status, out)
		}
	}
	for _, kind := range ArtifactKinds() {
		if !strings.Contains(out, kind) {
			t.Fatalf("accepted document kind %q is not in the context block:\n%s", kind, out)
		}
	}
}

// A document row with no stored document behind it is a placeholder. Saying so
// is what stops the agent telling the boss a cover letter is ready when nobody
// can open it.
func TestFormatChatContextFlagsAnEmptyDocument(t *testing.T) {
	out := FormatChatContext(fullCockpit())
	if !strings.Contains(out, "nothing written into it yet") {
		t.Fatalf("a document row with no stored document must say so:\n%s", out)
	}
	resume := strings.Index(out, "Resume for Northwind")
	empty := strings.Index(out, "nothing written into it yet")
	if resume == -1 || empty == -1 || empty < resume {
		t.Fatalf("the empty marker landed on the wrong document:\n%s", out)
	}
}
