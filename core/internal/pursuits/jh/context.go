package jh

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// FormatChatContext renders the whole board as the turn-1 context block for a
// seeded "pursuit_jh" session.
//
// This is the mechanic behind "the conversation starts with the board loaded"
// (Rule #1b): the agent receives every role and the stage it sits in, why each
// one scores the way it does, which postings look like ghosts, what interview
// material is banked, where every outreach has got to, and what has been
// written for which role — without calling a single tool. If this block were
// prose in a skill instead, a run that dropped the line would open by
// interrogating the boss for things the database already holds.
//
// The judgment — which role is worth chasing, what an outreach says, whether a
// resume is ready — is NOT here and never will be. That lives in the skill and
// in the conversation.
//
// Kept plain-text and bounded: this rides in the turn-1 prompt, so the digests
// are capped and long free text is truncated rather than dumped. It is written
// as prose the model reads, not as a JSON dump it has to parse.
func FormatChatContext(c Cockpit) string {
	var b strings.Builder

	b.WriteString("Job hunt: ")
	b.WriteString(strings.TrimSpace(c.Pursuit.Title))
	b.WriteString("\n")
	b.WriteString(boardLine(c.Summary))
	b.WriteString("\n")

	writePipeline(&b, c)
	writeCorpus(&b, c.Corpus)
	writeOutreach(&b, c.Contacts, c.Roles)
	writeArtifacts(&b, c.Artifacts, c.Roles)
	writeVocabulary(&b, c.Vocabulary)
	writeGuidance(&b)

	return b.String()
}

// boardLine is the one-sentence state of the board. It is derived from the
// same Summary the cockpit renders, so the sentence the agent reads and the
// line on the boss's screen cannot disagree.
func boardLine(s Summary) string {
	if s.TotalRoles == 0 {
		return "Nothing is on the board yet: no roles have been filed."
	}
	parts := []string{fmt.Sprintf("%s on the board", plural(s.TotalRoles, "role", "roles"))}
	if s.ContactsAwaitingReply > 0 {
		parts = append(parts, fmt.Sprintf("%s waiting on a reply",
			plural(s.ContactsAwaitingReply, "contact", "contacts")))
	}
	if s.CorpusCount > 0 {
		parts = append(parts, fmt.Sprintf("%s of interview material banked",
			plural(s.CorpusCount, "answer", "answers")))
	}
	if s.ArtifactCount > 0 {
		parts = append(parts, fmt.Sprintf("%s filed",
			plural(s.ArtifactCount, "document", "documents")))
	}
	return strings.Join(parts, ", ") + "."
}

// writePipeline walks the board in pipeline order, one heading per stage that
// actually holds something.
//
// Stages are walked in RoleStages() order rather than over the roles, so the
// agent reads the board the same way the boss sees it: left to right, first
// sighting through to an offer. A stage the store has never heard of would
// still be printed, under an "other" heading, because a role that vanished
// from this block is a role the agent would answer as if it did not exist.
func writePipeline(b *strings.Builder, c Cockpit) {
	if len(c.Roles) == 0 {
		return
	}

	byStage := make(map[string][]Role, len(c.Roles))
	for _, r := range c.Roles {
		byStage[r.Stage] = append(byStage[r.Stage], r)
	}

	b.WriteString("\nThe pipeline, stage by stage:\n")
	printed := make(map[string]bool, len(byStage))
	for _, stage := range c.Vocabulary.RoleStages {
		printed[stage] = true
		writeStage(b, stage, byStage[stage])
	}
	// Anything the vocabulary does not carry, so a role can never fall out.
	var extra []string
	for stage := range byStage {
		if !printed[stage] {
			extra = append(extra, stage)
		}
	}
	sort.Strings(extra)
	for _, stage := range extra {
		writeStage(b, stage, byStage[stage])
	}
}

// maxRolesPerStage caps a single stage's listing. A hundred rows at
// "discovered" would crowd out everything downstream of it, and the stages
// that matter are the ones with movement in them.
const maxRolesPerStage = 12

func writeStage(b *strings.Builder, stage string, roles []Role) {
	if len(roles) == 0 {
		return
	}
	fmt.Fprintf(b, "  %s (%d):\n", stageSentence(stage), len(roles))
	for i, r := range roles {
		if i >= maxRolesPerStage {
			fmt.Fprintf(b, "    ... and %d more at this stage\n", len(roles)-maxRolesPerStage)
			break
		}
		writeRole(b, r)
	}
}

func writeRole(b *strings.Builder, r Role) {
	fmt.Fprintf(b, "    - %s, %s", strings.TrimSpace(r.Company), strings.TrimSpace(r.RoleTitle))
	if where := strings.TrimSpace(r.Location); where != "" {
		fmt.Fprintf(b, " (%s)", where)
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "      role id: %s", r.ID)
	if pay := payPhrase(r); pay != "" {
		fmt.Fprintf(b, " · pay: %s", pay)
	}
	if r.FitScore != nil {
		fmt.Fprintf(b, " · fit: %d out of 100", *r.FitScore)
	} else {
		b.WriteString(" · fit: not scored yet")
	}
	if src := strings.TrimSpace(r.Source); src != "" {
		fmt.Fprintf(b, " · found on %s", src)
	}
	b.WriteString("\n")

	if reason := strings.TrimSpace(r.FitReasoning); reason != "" {
		fmt.Fprintf(b, "      why it fits: %s\n", truncate(reason, 300))
	}
	// The flags ARE the evidence. A ghost score with nothing behind it is a
	// number nobody can act on, so the signals are always named.
	if len(r.GhostFlags) > 0 {
		if r.GhostScore != nil {
			fmt.Fprintf(b, "      may not be a real opening (%d out of 100): %s\n",
				*r.GhostScore, strings.Join(r.GhostFlags, ", "))
		} else {
			fmt.Fprintf(b, "      may not be a real opening: %s\n", strings.Join(r.GhostFlags, ", "))
		}
	}
	if notes := strings.TrimSpace(r.Notes); notes != "" {
		fmt.Fprintf(b, "      notes: %s\n", truncate(notes, 300))
	}
	if url := strings.TrimSpace(r.URL); url != "" {
		fmt.Fprintf(b, "      posting: %s\n", url)
	}
}

// payPhrase never renders an unstated salary as a number. A stored zero is the
// absence of a band, not a band of nothing, and "$0" is the one thing this
// block must not say to a model that will then repeat it to the boss.
func payPhrase(r Role) string {
	min, max := realAmount(r.CompMin), realAmount(r.CompMax)
	switch {
	case min != nil && max != nil && *min == *max:
		return money(*min)
	case min != nil && max != nil:
		return money(*min) + " to " + money(*max)
	case min != nil:
		return money(*min) + " and up"
	case max != nil:
		return "up to " + money(*max)
	}
	if stated := strings.TrimSpace(r.CompText); stated != "" {
		return stated
	}
	return "not listed"
}

func realAmount(n *int) *int {
	if n == nil || *n <= 0 {
		return nil
	}
	return n
}

func money(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("$%dk", n/1000)
	}
	return fmt.Sprintf("$%d", n)
}

// maxCorpusThemes and maxCorpusPerTheme bound the interview digest. The themes
// are what the agent needs to know it HAS material to draw on; the full answers
// are one tool call away.
const (
	maxCorpusThemes   = 10
	maxCorpusPerTheme = 3
)

func writeCorpus(b *strings.Builder, corpus []CorpusEntry) {
	if len(corpus) == 0 {
		b.WriteString("\nNo interview material is banked yet, so nothing can be tailored from his own words.\n")
		return
	}

	// Grouped by theme in first-seen order, which is the order the entries came
	// back in, so the digest reads the same way twice.
	var themes []string
	byTheme := make(map[string][]CorpusEntry, len(corpus))
	for _, e := range corpus {
		theme := strings.TrimSpace(e.Theme)
		if _, seen := byTheme[theme]; !seen {
			themes = append(themes, theme)
		}
		byTheme[theme] = append(byTheme[theme], e)
	}

	fmt.Fprintf(b, "\nInterview material banked, by theme (%s across %s):\n",
		plural(len(corpus), "answer", "answers"), plural(len(themes), "theme", "themes"))
	for i, theme := range themes {
		if i >= maxCorpusThemes {
			fmt.Fprintf(b, "  ... and %d more themes\n", len(themes)-maxCorpusThemes)
			break
		}
		entries := byTheme[theme]
		fmt.Fprintf(b, "  %s (%d):\n", theme, len(entries))
		for j, e := range entries {
			if j >= maxCorpusPerTheme {
				break
			}
			fmt.Fprintf(b, "    - %s\n", truncate(e.Question, 180))
			if ans := strings.TrimSpace(e.Answer); ans != "" {
				fmt.Fprintf(b, "      %s\n", truncate(ans, 260))
			}
		}
	}
}

// maxContacts bounds the outreach digest.
const maxContacts = 15

// writeOutreach reports where every conversation with a human has got to.
//
// The contacts sitting at 'sent' are listed first and named as an obligation,
// because that is the only status on this board carrying one: a message went
// out and nothing has come back.
func writeOutreach(b *strings.Builder, contacts []Contact, roles []Role) {
	if len(contacts) == 0 {
		b.WriteString("\nNobody has been contacted yet.\n")
		return
	}

	byRole := roleIndex(roles)
	waiting := make([]Contact, 0, len(contacts))
	rest := make([]Contact, 0, len(contacts))
	for _, c := range contacts {
		if c.OutreachStatus == ContactStatusSent {
			waiting = append(waiting, c)
			continue
		}
		rest = append(rest, c)
	}

	b.WriteString("\nOutreach:\n")
	shown := 0
	if len(waiting) > 0 {
		b.WriteString("  Waiting on a reply:\n")
		for _, c := range waiting {
			if shown >= maxContacts {
				break
			}
			writeContact(b, c, byRole)
			shown++
		}
	}
	if len(rest) > 0 && shown < maxContacts {
		b.WriteString("  Everyone else:\n")
		for _, c := range rest {
			if shown >= maxContacts {
				break
			}
			writeContact(b, c, byRole)
			shown++
		}
	}
	if total := len(contacts); total > shown {
		fmt.Fprintf(b, "  ... and %d more contacts\n", total-shown)
	}
}

func writeContact(b *strings.Builder, c Contact, byRole map[string]Role) {
	fmt.Fprintf(b, "    - %s", strings.TrimSpace(c.Name))
	if title := strings.TrimSpace(c.Title); title != "" {
		fmt.Fprintf(b, ", %s", title)
	}
	if company := strings.TrimSpace(c.Company); company != "" {
		fmt.Fprintf(b, " at %s", company)
	}
	fmt.Fprintf(b, " — %s", contactSentence(c.OutreachStatus))
	if c.OutreachSentAt != nil {
		fmt.Fprintf(b, " (sent %s)", c.OutreachSentAt.Format(time.RFC3339))
	}
	if c.RoleID != nil {
		if r, ok := byRole[*c.RoleID]; ok {
			fmt.Fprintf(b, ", found for the %s role at %s", strings.TrimSpace(r.RoleTitle), strings.TrimSpace(r.Company))
		}
	}
	b.WriteString("\n")
	if msg := strings.TrimSpace(c.LastMessage); msg != "" {
		fmt.Fprintf(b, "      last message: %s\n", truncate(msg, 260))
	}
}

// maxArtifacts bounds the document digest.
const maxArtifacts = 20

func writeArtifacts(b *strings.Builder, artifacts []Artifact, roles []Role) {
	if len(artifacts) == 0 {
		b.WriteString("\nNo resumes, cover letters or positioning reads have been written yet.\n")
		return
	}
	byRole := roleIndex(roles)
	b.WriteString("\nDocuments filed:\n")
	for i, a := range artifacts {
		if i >= maxArtifacts {
			fmt.Fprintf(b, "  ... and %d more documents\n", len(artifacts)-maxArtifacts)
			break
		}
		fmt.Fprintf(b, "  - %s: %s", artifactSentence(a.Kind), strings.TrimSpace(a.Title))
		if r, ok := byRole[a.RoleID]; ok {
			fmt.Fprintf(b, " for %s at %s", strings.TrimSpace(r.RoleTitle), strings.TrimSpace(r.Company))
		}
		fmt.Fprintf(b, " — %s", artifactStatusSentence(a.Status))
		// A document row with no stored document behind it is a placeholder,
		// and saying so stops the agent claiming a resume exists that nobody
		// can open.
		if a.ArtifactID == nil || strings.TrimSpace(*a.ArtifactID) == "" {
			b.WriteString(" (nothing written into it yet)")
		}
		b.WriteString("\n")
	}
}

func roleIndex(roles []Role) map[string]Role {
	byRole := make(map[string]Role, len(roles))
	for _, r := range roles {
		byRole[r.ID] = r
	}
	return byRole
}

// writeVocabulary hands the agent the exact strings the store will accept.
//
// Without it, a write-back is guesswork: the model picks "screening" for a
// stage, the store rejects it, and the boss watches a move that silently did
// not happen. The lists come from the cockpit, which took them from the same
// validators the writes are checked against, so they cannot drift.
func writeVocabulary(b *strings.Builder, v Vocabulary) {
	b.WriteString("\nThe values the board will accept, exactly as written:\n")
	fmt.Fprintf(b, "  role stages, in pipeline order: %s\n", strings.Join(v.RoleStages, ", "))
	fmt.Fprintf(b, "  where a role was found: %s\n", strings.Join(v.RoleSources, ", "))
	fmt.Fprintf(b, "  outreach statuses: %s\n", strings.Join(v.ContactStatuses, ", "))
	fmt.Fprintf(b, "  document kinds: %s\n", strings.Join(v.ArtifactKinds, ", "))
	fmt.Fprintf(b, "  document statuses: %s\n", strings.Join(v.ArtifactStatuses, ", "))
	fmt.Fprintf(b, "  where banked material came from: %s\n", strings.Join(v.CorpusSources, ", "))
}

func writeGuidance(b *strings.Builder) {
	b.WriteString("\nHow to hold this conversation. This is his job hunt and the board " +
		"above is the whole of it, so do not ask him for anything already written " +
		"here and do not read the board back at him — he is looking at it. Answer " +
		"the question he actually asked. When he decides something concrete in " +
		"this conversation, write it back with the pursuit_jh_write tool (file or " +
		"update a role, move a role to another stage, bank an interview answer, " +
		"add a contact or move their outreach on, file a document or change its " +
		"status) so the board and this chat never disagree, and use pursuit_jh_state " +
		"to re-read the board rather than trusting a stale memory of it. Never " +
		"invent a role, a contact, a salary or an interview answer on his behalf; " +
		"a posting that did not state a band has no band. Nothing reaches an " +
		"employer until he has approved it: a document you write is a draft, and " +
		"an outreach message is his to send.\n")
}

/* ── Wording ────────────────────────────────────────────────────────────── */

// The sentences below exist for the same reason the Studio labels file does:
// `google_jobs` and `positioning_read` are storage keys, and a model handed a
// storage key repeats it back to the boss. An unknown value is humanised
// rather than dropped, so a stage this file has never heard of still reads as
// a phrase and its roles still appear.

var roleStageSentences = map[string]string{
	StageDiscovered:   "Found",
	StageReviewed:     "Reviewed",
	StageTailoring:    "Tailoring",
	StageApplied:      "Applied",
	StageOutreached:   "Reached out",
	StageResponded:    "They replied",
	StageInterviewing: "Interviewing",
	StageOffer:        "Offer",
	StageDead:         "Closed",
}

var contactStatusSentences = map[string]string{
	ContactStatusIdentified: "found, not contacted yet",
	ContactStatusDrafted:    "a message is drafted but not sent",
	ContactStatusSent:       "message sent, nothing back yet",
	ContactStatusReplied:    "they replied",
	ContactStatusDead:       "gone quiet",
}

var artifactKindSentences = map[string]string{
	ArtifactKindResume:          "Resume",
	ArtifactKindCoverLetter:     "Cover letter",
	ArtifactKindPositioningRead: "Positioning read",
}

var artifactStatusSentences = map[string]string{
	ArtifactStatusDraft:    "draft, not approved",
	ArtifactStatusApproved: "approved by him",
	ArtifactStatusSent:     "sent",
}

// stageSentence names a stage AND keeps the storage key beside it, because the
// agent has to write the key back through pursuit_jh_write. This is the one
// place both belong together: it is a model-facing block, not a screen.
func stageSentence(stage string) string {
	return fmt.Sprintf("%s [%s]", phraseFor(roleStageSentences, stage), stage)
}

func contactSentence(status string) string { return phraseFor(contactStatusSentences, status) }
func artifactSentence(kind string) string  { return phraseFor(artifactKindSentences, kind) }
func artifactStatusSentence(status string) string {
	return phraseFor(artifactStatusSentences, status)
}

func phraseFor(m map[string]string, value string) string {
	if v, ok := m[value]; ok {
		return v
	}
	return humanise(value)
}

// humanise turns a storage key into something sayable: `no_named_manager`
// reads as "No named manager". Mirrors the Studio helper of the same name.
func humanise(value string) string {
	words := strings.TrimSpace(strings.NewReplacer("_", " ", "-", " ").Replace(value))
	if words == "" {
		return ""
	}
	return strings.ToUpper(words[:1]) + words[1:]
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
