package jh

// The single write chokepoint for the Job Hunt experience.
//
// Two callers reach these tables — the HTTP cockpit and the pursuit_jh_write
// agent tool — and Rule #1c says the gate belongs at the one method they both
// flow through, not copied into each of them. Without this file the handler
// would carry a seven-branch switch and the tool would carry a second one, and
// the two would disagree the first time an action grew a field: a role filed
// from the board would carry a salary band and the same role filed from chat
// would not, with nothing in either file to show the divergence.
//
// This is the sibling of pc.Store.Apply and is deliberately the same shape.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// WriteRequest is the union of every board mutation payload. One envelope so
// the HTTP API, the agent tool, and any future caller speak the same shape and
// inherit the same validation.
//
// Several fields are shared across actions on purpose — Company by a role and
// a contact, Title by a contact's job title and a document's name, Status by
// the outreach ladder and the approval ladder. No single action reads two
// meanings of one field, and a per-action copy of each would be five more
// fields for a caller to transpose.
type WriteRequest struct {
	// role
	RoleID       string     `json:"role_id"`
	Company      string     `json:"company"`
	RoleTitle    string     `json:"role_title"`
	Source       string     `json:"source"`
	URL          string     `json:"url"`
	Location     string     `json:"location"`
	CompMin      *int       `json:"comp_min"`
	CompMax      *int       `json:"comp_max"`
	CompText     string     `json:"comp_text"`
	PostedAt     *time.Time `json:"posted_at"`
	FitScore     *int       `json:"fit_score"`
	FitReasoning string     `json:"fit_reasoning"`
	GhostScore   *int       `json:"ghost_score"`
	GhostFlags   []string   `json:"ghost_flags"`
	Notes        string     `json:"notes"`
	ExternalID   string     `json:"external_id"`
	Stage        string     `json:"stage"`

	// corpus
	Theme    string         `json:"theme"`
	Question string         `json:"question"`
	Answer   string         `json:"answer"`
	Metrics  map[string]any `json:"metrics"`
	Tags     []string       `json:"tags"`

	// contact
	ContactID   string `json:"contact_id"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	LinkedInURL string `json:"linkedin_url"`
	Email       string `json:"email"`
	LastMessage string `json:"last_message"`

	// artifact
	//
	// ArtifactID names the mem_jobhunt_artifacts row this action targets, the
	// same way RoleID and ContactID name theirs. StoredArtifactID is the
	// separate mem_artifacts row where the document's bytes live, and is nil
	// until the document actually exists.
	ArtifactID       string  `json:"artifact_id"`
	Kind             string  `json:"kind"`
	StoredArtifactID *string `json:"stored_artifact_id"`

	// Status is the outreach ladder for a contact and the approval ladder for
	// a document. Which one it means is decided by the action, never guessed.
	Status string `json:"status"`
}

// Write actions. These double as the HTTP path suffixes under
// /api/pursuits/jh/ and as the `action` enum on the pursuit_jh_write tool.
const (
	ActionRole           = "role"
	ActionRoleStage      = "role/stage"
	ActionCorpus         = "corpus"
	ActionContact        = "contact"
	ActionContactStatus  = "contact/status"
	ActionArtifact       = "artifact"
	ActionArtifactStatus = "artifact/status"
)

// WriteActions enumerates every accepted action, for schema generation and
// validation.
func WriteActions() []string {
	return []string{
		ActionRole, ActionRoleStage, ActionCorpus,
		ActionContact, ActionContactStatus,
		ActionArtifact, ActionArtifactStatus,
	}
}

// IsWriteAction reports whether the supplied action is one Apply handles.
func IsWriteAction(action string) bool {
	for _, a := range WriteActions() {
		if a == action {
			return true
		}
	}
	return false
}

// ErrUnknownAction is returned for an action outside WriteActions.
var ErrUnknownAction = errors.New("unknown pursuit_jh action")

// Apply is the SINGLE write chokepoint for the Job Hunt experience.
//
// The order of the three guards is what makes a bad request cheap and legible:
// an unknown action reads as a typo rather than as whatever the pursuit lookup
// happens to say, a rejected value names the values that would have worked,
// and only then does anything touch the database. A caller that fat-fingers a
// stage learns which stages exist without spending a round trip on it.
func (s *Store) Apply(ctx context.Context, action, pursuitID string, req WriteRequest) error {
	if !IsWriteAction(action) {
		return ErrUnknownAction
	}
	if err := req.validate(action); err != nil {
		return err
	}
	// Reject an ordinary pursuit, or one running a different experience,
	// before any write lands — uniformly for every action.
	if _, err := s.Header(ctx, pursuitID); err != nil {
		return err
	}

	switch action {
	case ActionRole:
		_, err := s.UpsertRole(ctx, pursuitID, RoleInput{
			Company:      req.Company,
			RoleTitle:    req.RoleTitle,
			Source:       req.Source,
			URL:          req.URL,
			Location:     req.Location,
			CompMin:      req.CompMin,
			CompMax:      req.CompMax,
			CompText:     req.CompText,
			PostedAt:     req.PostedAt,
			FitScore:     req.FitScore,
			FitReasoning: req.FitReasoning,
			GhostScore:   req.GhostScore,
			GhostFlags:   req.GhostFlags,
			Notes:        req.Notes,
			ExternalID:   req.ExternalID,
			Stage:        req.Stage,
		})
		return err

	case ActionRoleStage:
		_, err := s.SetRoleStage(ctx, pursuitID, req.RoleID, strings.TrimSpace(req.Stage))
		return err

	case ActionCorpus:
		_, err := s.AddCorpusEntry(ctx, pursuitID, CorpusInput{
			Theme:    req.Theme,
			Question: req.Question,
			Answer:   req.Answer,
			Metrics:  req.Metrics,
			Tags:     req.Tags,
			Source:   req.Source,
		})
		return err

	case ActionContact:
		_, err := s.UpsertContact(ctx, pursuitID, ContactInput{
			ID:             req.ContactID,
			RoleID:         optionalID(req.RoleID),
			Name:           req.Name,
			Title:          req.Title,
			Company:        req.Company,
			LinkedInURL:    req.LinkedInURL,
			Email:          req.Email,
			LastMessage:    req.LastMessage,
			OutreachStatus: req.Status,
		})
		return err

	case ActionContactStatus:
		_, err := s.SetContactStatus(ctx, pursuitID, req.ContactID, strings.TrimSpace(req.Status))
		return err

	case ActionArtifact:
		_, err := s.AddArtifact(ctx, pursuitID, ArtifactInput{
			RoleID:     req.RoleID,
			Kind:       strings.TrimSpace(req.Kind),
			Title:      req.Title,
			ArtifactID: req.StoredArtifactID,
			Status:     strings.TrimSpace(req.Status),
		})
		return err

	case ActionArtifactStatus:
		_, err := s.SetArtifactStatus(ctx, pursuitID, req.ArtifactID,
			strings.TrimSpace(req.Status), req.StoredArtifactID)
		return err

	default:
		return ErrUnknownAction
	}
}

// validate checks every constrained value the chosen action carries, with no
// database access, so a bad value costs nothing and names what would work.
//
// It duplicates no vocabulary: the checks below call the same IsValid*
// functions the store's own writes are guarded by, and unknownValue builds the
// same sentence the store builds, so a value rejected here reads exactly as it
// would if it had been rejected one layer down. This is a fail-fast, never a
// replacement — the store still validates, so a caller reaching it directly is
// no less protected.
//
// A blank value is only rejected where the column is genuinely required. Blank
// elsewhere means "leave it alone", which is what lets a thin re-sweep patch a
// role without erasing the salary band a richer pass already stored.
func (req WriteRequest) validate(action string) error {
	switch action {
	case ActionRole:
		if !IsValidRoleSource(strings.TrimSpace(req.Source)) {
			return unknownValue("role source", req.Source, RoleSources())
		}
		if stage := strings.TrimSpace(req.Stage); stage != "" && !IsValidRoleStage(stage) {
			return unknownValue("role stage", req.Stage, RoleStages())
		}

	case ActionRoleStage:
		if strings.TrimSpace(req.RoleID) == "" {
			return errors.New("role_id required")
		}
		if !IsValidRoleStage(strings.TrimSpace(req.Stage)) {
			return unknownValue("role stage", req.Stage, RoleStages())
		}

	case ActionCorpus:
		if source := strings.TrimSpace(req.Source); source != "" && !IsValidCorpusSource(source) {
			return unknownValue("corpus source", req.Source, CorpusSources())
		}

	case ActionContact:
		if status := strings.TrimSpace(req.Status); status != "" && !IsValidContactStatus(status) {
			return unknownValue("contact status", req.Status, ContactStatuses())
		}

	case ActionContactStatus:
		if strings.TrimSpace(req.ContactID) == "" {
			return errors.New("contact_id required")
		}
		if !IsValidContactStatus(strings.TrimSpace(req.Status)) {
			return unknownValue("contact status", req.Status, ContactStatuses())
		}

	case ActionArtifact:
		if strings.TrimSpace(req.RoleID) == "" {
			return errors.New("role_id required")
		}
		if !IsValidArtifactKind(strings.TrimSpace(req.Kind)) {
			return unknownValue("artifact kind", req.Kind, ArtifactKinds())
		}
		if status := strings.TrimSpace(req.Status); status != "" && !IsValidArtifactStatus(status) {
			return unknownValue("artifact status", req.Status, ArtifactStatuses())
		}

	case ActionArtifactStatus:
		if strings.TrimSpace(req.ArtifactID) == "" {
			return errors.New("artifact_id required")
		}
		if !IsValidArtifactStatus(strings.TrimSpace(req.Status)) {
			return unknownValue("artifact status", req.Status, ArtifactStatuses())
		}
	}
	return nil
}

// unknownValue builds the rejection sentence, in the same shape the store's own
// validators produce. Naming the accepted values is the whole point: a caller
// told only that "sourced" is wrong has to go read the schema, and one told the
// five sources that exist fixes it on the spot.
func unknownValue(label, got string, accepted []string) error {
	return fmt.Errorf("unknown %s %q, expected one of: %s",
		label, got, strings.Join(accepted, ", "))
}

// optionalID turns a blank id into nil, which every store update reads as
// "leave this alone" rather than as a request to clear the column.
func optionalID(id string) *string {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	return &id
}
