// Job Hunt cockpit tools - let Jarvis read and write the hunt from chat.
//
//	pursuit_jh_state → read the whole board (pipeline, corpus, contacts, docs)
//	pursuit_jh_write → every mutation, behind one `action` discriminator
//
// Two tools rather than eight, for the same reason the coached pursuit has two:
// the write side maps onto the single jh.Store.Apply chokepoint the HTTP
// cockpit also uses (Rule #1c), so a role moved from chat and the same role
// moved on the board produce byte-identical state, including the mechanics
// neither caller has to remember - that a re-swept posting updates rather than
// duplicates, that a stage change stamps the clock, and that sending outreach
// stamps when it went.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/pursuits/jh"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterPursuitJHTools wires the job-hunt read/write pair. No-op if the pool
// is nil so chat-only deployments don't break.
func RegisterPursuitJHTools(r *Registry, pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	r.Register(&pursuitJHState{pool: pool})
	r.Register(&pursuitJHWrite{pool: pool})
}

// pipelineNotHabit is the one thing both descriptions have to establish, so it
// is written once. A job_hunt pursuit has no done-today flag and no streak;
// pursuit_checkin against one is a category error that would file a habit tick
// against a pipeline and tell the boss nothing about his hunt.
const pipelineNotHabit = "A job_hunt pursuit is a PIPELINE, not a habit: it has roles moving between stages, banked interview answers, people to contact and documents per role, and no done-today flag or streak. Read and write it with pursuit_jh_state and pursuit_jh_write only - never pursuit_checkin, which is for ordinary habit pursuits and has nothing to record here."

// ── pursuit_jh_state ───────────────────────────────────────────────────────

type pursuitJHState struct{ pool *pgxpool.Pool }

func (t *pursuitJHState) Name() string { return "pursuit_jh_state" }

// ReadOnly is declared explicitly because the `_state` suffix is not one the
// name heuristic recognises as a read, and system_map would otherwise bucket
// this pure read under the mutating tools.
func (t *pursuitJHState) ReadOnly() bool { return true }
func (t *pursuitJHState) Description() string {
	return "Read the current state of a Job Hunt pursuit: every role in the pipeline with its stage, fit score, ghost score and salary band; the banked interview answers grouped by theme; the hiring managers and recruiters with their outreach state; the tailored resumes, cover letters and positioning reads; derived counts; and the accepted values for every stage, source, status and kind. " +
		"Call this before advising him on the hunt so you work from what is actually filed rather than asking him to repeat it, and before any write so you use the real role, contact and document ids. " +
		pipelineNotHabit + " Use pursuit_list to find the pursuit id."
}
func (t *pursuitJHState) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pursuit_id": map[string]any{"type": "string", "description": "The pursuit's UUID."},
		},
		"required": []string{"pursuit_id"},
	}
}
func (t *pursuitJHState) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "pursuit_id")
	if id == "" {
		return "", errors.New("pursuit_id required")
	}
	cockpit, err := jh.NewStore(t.pool).Cockpit(ctx, id)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(cockpit)
	if err != nil {
		return "", fmt.Errorf("encode cockpit: %w", err)
	}
	return string(out), nil
}

// ── pursuit_jh_write ───────────────────────────────────────────────────────

type pursuitJHWrite struct{ pool *pgxpool.Pool }

func (t *pursuitJHWrite) Name() string { return "pursuit_jh_write" }
func (t *pursuitJHWrite) Description() string {
	return "Write a decision from this conversation back into a Job Hunt pursuit so the board and the chat never disagree. " +
		"action='role' files a role, or updates it in place when the same posting is seen again (the source's own id in external_id is what makes a re-sweep update rather than duplicate). " +
		"action='role/stage' moves a role between pipeline stages and stamps when it moved. " +
		"action='corpus' banks one interview question and the answer he actually gave, under a theme, so a later tailoring pass draws on a real story instead of inventing one. " +
		"action='contact' adds a hiring manager or recruiter, or patches one named by contact_id; action='contact/status' moves them along the outreach ladder and stamps the send. " +
		"action='artifact' files a tailored resume, cover letter or positioning read against a role; action='artifact/status' moves it from draft to approved to sent. " +
		"Nothing reaches an employer by being written here - approval is a status he sets, never something you infer from having generated the document. " +
		"Only write what he actually decided, or what a posting actually said. Never invent a salary band, a fit score, an interview answer or a contact. " +
		pipelineNotHabit
}
func (t *pursuitJHWrite) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pursuit_id": map[string]any{"type": "string", "description": "The pursuit's UUID."},
			"action": map[string]any{
				"type": "string",
				"enum": jh.WriteActions(),
			},

			// role
			"role_id":    map[string]any{"type": "string", "description": "The role's UUID (from pursuit_jh_state). Required for action='role/stage' and action='artifact'; optional on action='contact' to attach the person to a role."},
			"company":    map[string]any{"type": "string", "description": "For action='role' (required) and action='contact': the company."},
			"role_title": map[string]any{"type": "string", "description": "For action='role': the posting's title, e.g. 'Head of Product'. Required."},
			"source": map[string]any{
				"type":        "string",
				"description": "For action='role' (required): where the posting was found. For action='corpus': where the answer came from, defaulting to adhoc.",
				"enum":        append(append([]string{}, jh.RoleSources()...), jh.CorpusSources()...),
			},
			"url":           map[string]any{"type": "string", "description": "For action='role': the posting URL."},
			"location":      map[string]any{"type": "string", "description": "For action='role': the location as stated, e.g. 'Remote (US)'."},
			"comp_min":      map[string]any{"type": "integer", "description": "For action='role': the bottom of the stated salary band. Omit when the posting states none - never guess a number."},
			"comp_max":      map[string]any{"type": "integer", "description": "For action='role': the top of the stated band. Omit when unstated."},
			"comp_text":     map[string]any{"type": "string", "description": "For action='role': the compensation exactly as the posting words it, when it does not parse into a band."},
			"posted_at":     map[string]any{"type": "string", "description": "For action='role': when the posting went up, RFC3339 or YYYY-MM-DD."},
			"fit_score":     map[string]any{"type": "integer", "description": "For action='role': how well it fits him, 0-100."},
			"fit_reasoning": map[string]any{"type": "string", "description": "For action='role': why that fit score, in a sentence he can argue with."},
			"ghost_score":   map[string]any{"type": "integer", "description": "For action='role': how likely the posting is a ghost listing, 0-100."},
			"ghost_flags":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "For action='role': what made it look like a ghost listing, e.g. reposted repeatedly, no salary, vague scope."},
			"notes":         map[string]any{"type": "string", "description": "For action='role': anything else worth keeping about the posting."},
			"external_id":   map[string]any{"type": "string", "description": "For action='role': the source's own id for the posting. Supplying it is what makes a repeat sweep update the role rather than file a second copy."},
			"stage": map[string]any{
				"type":        "string",
				"description": "The pipeline stage. Required for action='role/stage'. On action='role' it only applies when the role is new, so a re-sweep can never drag a card he has already applied to back to discovered.",
				"enum":        jh.RoleStages(),
			},

			// corpus
			"theme":    map[string]any{"type": "string", "description": "For action='corpus' (required): what the answer is about, e.g. 'leading through a reorg'. Free text - reuse an existing theme from pursuit_jh_state when one fits."},
			"question": map[string]any{"type": "string", "description": "For action='corpus' (required): the interview question."},
			"answer":   map[string]any{"type": "string", "description": "For action='corpus' (required): his answer, in his own words. Never a paraphrase you improved."},
			"metrics":  map[string]any{"type": "object", "description": "For action='corpus': the numbers in the story, so a tailoring pass can quote them exactly."},
			"tags":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "For action='corpus': labels to retrieve the answer by."},

			// contact
			"contact_id":   map[string]any{"type": "string", "description": "The contact's UUID (from pursuit_jh_state). Required for action='contact/status'. On action='contact' it selects the person to patch; omit it to add a new one."},
			"name":         map[string]any{"type": "string", "description": "For action='contact': the person's name. Required when adding."},
			"title":        map[string]any{"type": "string", "description": "For action='contact': their job title. For action='artifact': the document's title (required)."},
			"linkedin_url": map[string]any{"type": "string", "description": "For action='contact': their LinkedIn profile."},
			"email":        map[string]any{"type": "string", "description": "For action='contact': their email."},
			"last_message": map[string]any{"type": "string", "description": "For action='contact': the message that actually went out, or the last exchange."},

			// artifact
			"kind": map[string]any{
				"type":        "string",
				"description": "For action='artifact' (required): what the document is.",
				"enum":        jh.ArtifactKinds(),
			},
			"artifact_id":        map[string]any{"type": "string", "description": "For action='artifact/status' (required): the UUID of the filed document (from pursuit_jh_state)."},
			"stored_artifact_id": map[string]any{"type": "string", "description": "The mem_artifacts UUID holding the document itself, once it exists. Omit while the document is only commissioned."},

			// shared
			"status": map[string]any{
				"type":        "string",
				"description": "For action='contact'/'contact/status': the outreach ladder (identified, drafted, sent, replied, dead) - setting it to sent stamps when it went. For action='artifact'/'artifact/status': the approval ladder (draft, approved, sent). Required on both status actions.",
				"enum":        append(append([]string{}, jh.ContactStatuses()...), jh.ArtifactStatuses()...),
			},
		},
		"required": []string{"pursuit_id", "action"},
	}
}

func (t *pursuitJHWrite) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "pursuit_id")
	if id == "" {
		return "", errors.New("pursuit_id required")
	}
	action := strings.TrimSpace(strString(in, "action"))
	if action == "" {
		return "", errors.New("action required")
	}
	// Unlike the coached pursuit, this tool is NOT closed to unattended turns.
	// Most of the board is external fact - a posting exists, a recruiter has a
	// title, a document was generated - and a nightly sweep filing roles is the
	// point of the experience, so a blanket refusal here would break it.
	//
	// The corpus is the exception and is refused: an entry is a question and
	// the answer HE gave, so an unattended turn has nobody who said it, and
	// anything written would be invented and then shown back to him as his own
	// words. The skill body says "only what he actually said", but prose is
	// droppable by the model (Rule #1b), so the refusal holds here instead.
	if action == jh.ActionCorpus && IsAutonomous(ctx) {
		return "", errors.New("refusing to bank an interview answer on an unattended turn: a corpus entry is the boss's own answer in his own words, and nothing was said on this turn to write down. Ask him for it in live chat instead")
	}

	req := jh.WriteRequest{
		RoleID:       strString(in, "role_id"),
		Company:      strString(in, "company"),
		RoleTitle:    strString(in, "role_title"),
		Source:       strString(in, "source"),
		URL:          strString(in, "url"),
		Location:     strString(in, "location"),
		CompText:     strString(in, "comp_text"),
		FitReasoning: strString(in, "fit_reasoning"),
		GhostFlags:   stringSliceOrEmpty(in, "ghost_flags"),
		Notes:        strString(in, "notes"),
		ExternalID:   strString(in, "external_id"),
		Stage:        strString(in, "stage"),

		Theme:    strString(in, "theme"),
		Question: strString(in, "question"),
		Answer:   strString(in, "answer"),
		Tags:     stringSliceOrEmpty(in, "tags"),

		ContactID:   strString(in, "contact_id"),
		Name:        strString(in, "name"),
		Title:       strString(in, "title"),
		LinkedInURL: strString(in, "linkedin_url"),
		Email:       strString(in, "email"),
		LastMessage: strString(in, "last_message"),

		ArtifactID: strString(in, "artifact_id"),
		Kind:       strString(in, "kind"),
		Status:     strString(in, "status"),
	}
	if m, ok := in["metrics"].(map[string]any); ok {
		req.Metrics = m
	}
	// Absent stays absent. A salary band or a score the posting never stated
	// must reach the store as nil, not as a zero the board would render as a
	// real number.
	req.CompMin = optionalInt(in, "comp_min")
	req.CompMax = optionalInt(in, "comp_max")
	req.FitScore = optionalInt(in, "fit_score")
	req.GhostScore = optionalInt(in, "ghost_score")
	if v := strString(in, "stored_artifact_id"); v != "" {
		req.StoredArtifactID = &v
	}
	posted, err := parseOptionalTime(in["posted_at"])
	if err != nil {
		return "", errors.New("bad posted_at: use RFC3339 or YYYY-MM-DD")
	}
	req.PostedAt = posted

	store := jh.NewStore(t.pool)
	if err := store.Apply(ctx, action, id, req); err != nil {
		return "", err
	}

	// Return the refreshed board summary so the model immediately knows what
	// the write changed, without a second tool call. The counts rather than the
	// rows: a full cockpit here would put the whole pipeline back into context
	// after every single write, and pursuit_jh_state exists for when the rows
	// are actually wanted.
	cockpit, err := store.Cockpit(ctx, id)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok":                      true,
		"action":                  action,
		"total_roles":             cockpit.Summary.TotalRoles,
		"roles_by_stage":          cockpit.Summary.RolesByStage,
		"corpus_count":            cockpit.Summary.CorpusCount,
		"contact_count":           cockpit.Summary.ContactCount,
		"contacts_awaiting_reply": cockpit.Summary.ContactsAwaitingReply,
		"artifact_count":          cockpit.Summary.ArtifactCount,
	})
	return string(out), nil
}

// optionalInt reads a whole number that may simply not have been supplied.
// Missing and zero are different facts here - "no salary stated" versus "a band
// starting at nothing" - so an absent key returns nil rather than a zero the
// board would render as real.
func optionalInt(in map[string]any, key string) *int {
	v, ok := in[key]
	if !ok || v == nil {
		return nil
	}
	f, ok := numFloat(v)
	if !ok {
		return nil
	}
	n := int(f)
	return &n
}
