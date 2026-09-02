package jh

// The documents written for a specific role: the tailored resume, the cover
// letter, and the positioning read.
//
// Every row here belongs to a role, which is why role_id is NOT NULL and
// cascades. A tailored resume with no role has nothing left to be tailored to.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The three kinds of document the hunt produces. Mirrors
// chk_jobhunt_artifacts_kind in 207_jobhunt_support.sql exactly.
const (
	ArtifactKindResume          = "resume"
	ArtifactKindCoverLetter     = "cover_letter"
	ArtifactKindPositioningRead = "positioning_read"
)

// ArtifactKinds enumerates every accepted kind.
func ArtifactKinds() []string {
	return []string{ArtifactKindResume, ArtifactKindCoverLetter, ArtifactKindPositioningRead}
}

// IsValidArtifactKind reports whether the supplied string is one the database
// will accept.
func IsValidArtifactKind(kind string) bool {
	for _, k := range ArtifactKinds() {
		if k == kind {
			return true
		}
	}
	return false
}

// The approval ladder. Mirrors chk_jobhunt_artifacts_status. 'approved' is the
// gate that matters: nothing reaches an employer until the boss has said so,
// which is why it is a stored status and not an inference from having been
// generated.
const (
	ArtifactStatusDraft    = "draft"
	ArtifactStatusApproved = "approved"
	ArtifactStatusSent     = "sent"
)

// ArtifactStatuses enumerates every accepted status.
func ArtifactStatuses() []string {
	return []string{ArtifactStatusDraft, ArtifactStatusApproved, ArtifactStatusSent}
}

// IsValidArtifactStatus reports whether the supplied string is one the database
// will accept.
func IsValidArtifactStatus(status string) bool {
	for _, s := range ArtifactStatuses() {
		if s == status {
			return true
		}
	}
	return false
}

// Artifact is one generated document filed against a role.
//
// ArtifactID is a pointer into mem_artifacts, where the bytes and the virtual
// path actually live, and is nil until there is something to point at: the row
// is filed the moment a document is commissioned, which is before it exists.
type Artifact struct {
	ID         string    `json:"id"`
	PursuitID  string    `json:"pursuit_id"`
	RoleID     string    `json:"role_id"`
	Kind       string    `json:"kind"`
	ArtifactID *string   `json:"artifact_id"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// ArtifactInput is the write payload.
type ArtifactInput struct {
	RoleID string
	// Kind must be one of ArtifactKinds().
	Kind  string
	Title string
	// ArtifactID is the mem_artifacts row once the document exists. Nil when
	// the document is only commissioned.
	ArtifactID *string
	// Status must be one of ArtifactStatuses(). Blank starts at draft, which
	// is the only honest starting point: nothing is approved by being made.
	Status string
}

// artifactColumns is the SELECT list every artifact read shares, so a column
// added to one read cannot go missing from another.
const artifactColumns = `
	id::text, pursuit_id::text, role_id::text, kind, artifact_id::text,
	title, status, created_at`

// scanArtifact reads one row in artifactColumns order.
func scanArtifact(row interface{ Scan(...any) error }) (Artifact, error) {
	var a Artifact
	var artifactID *string
	if err := row.Scan(
		&a.ID, &a.PursuitID, &a.RoleID, &a.Kind, &artifactID,
		&a.Title, &a.Status, &a.CreatedAt,
	); err != nil {
		return Artifact{}, err
	}
	a.ArtifactID = artifactID
	return a, nil
}

// Artifacts returns every document on the pursuit, grouped by role and
// newest-first within a role.
//
// Role leads the ordering because that is how the cockpit reads them: a role's
// resume, cover letter and positioning read belong together as one package.
// Ordering here rather than in the caller means the caller can walk the slice
// once and cut it into per-role groups without bucketing it itself.
func (s *Store) Artifacts(ctx context.Context, pursuitID string) ([]Artifact, error) {
	if _, err := s.Header(ctx, pursuitID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+artifactColumns+`
		FROM mem_jobhunt_artifacts
		WHERE pursuit_id = $1::uuid
		ORDER BY role_id, created_at DESC
	`, pursuitID)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer rows.Close()
	out := []Artifact{}
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AddArtifact files a document against a role.
//
// A plain insert, not an upsert: a second cover letter for the same role is a
// rewrite worth keeping beside the first, not a correction that should
// overwrite it. The schema agrees, there is no unique constraint here to
// conflict on.
func (s *Store) AddArtifact(ctx context.Context, pursuitID string, in ArtifactInput) (Artifact, error) {
	if _, err := s.Header(ctx, pursuitID); err != nil {
		return Artifact{}, err
	}

	roleID := strings.TrimSpace(in.RoleID)
	if roleID == "" {
		return Artifact{}, errors.New("role_id required")
	}
	if !IsValidArtifactKind(in.Kind) {
		return Artifact{}, fmt.Errorf("unknown artifact kind %q, expected one of: %s",
			in.Kind, strings.Join(ArtifactKinds(), ", "))
	}
	title := clampText(in.Title)
	if title == "" {
		return Artifact{}, errors.New("title required")
	}
	status := strings.TrimSpace(in.Status)
	if status == "" {
		status = ArtifactStatusDraft
	}
	if !IsValidArtifactStatus(status) {
		return Artifact{}, fmt.Errorf("unknown artifact status %q, expected one of: %s",
			in.Status, strings.Join(ArtifactStatuses(), ", "))
	}

	// The role is constrained to this pursuit in the INSERT itself rather than
	// checked first: a SELECT-then-INSERT would let a role be deleted in the
	// gap, and the FK alone would happily accept a role belonging to a
	// different pursuit. No matching row means no insert.
	a, err := scanArtifact(s.pool.QueryRow(ctx, `
		INSERT INTO mem_jobhunt_artifacts
			(pursuit_id, role_id, kind, artifact_id, title, status)
		SELECT $1::uuid, r.id, $3, $4::uuid, $5, $6
		FROM mem_jobhunt_roles r
		WHERE r.id = $2::uuid AND r.pursuit_id = $1::uuid
		RETURNING `+artifactColumns+`
	`, pursuitID, roleID, in.Kind, in.ArtifactID, title, status))
	if err != nil {
		return Artifact{}, fmt.Errorf("add artifact: %w", err)
	}
	return a, nil
}

// SetArtifactStatus moves a document along the approval ladder.
//
// artifact_id is patched through COALESCE in the same statement because the
// two changes almost always happen together: the document finishes generating,
// so it both gains its bytes and stops being a bare commission. Splitting that
// into two writes would leave a window where an approved document points at
// nothing.
func (s *Store) SetArtifactStatus(ctx context.Context, pursuitID, id, status string, artifactID *string) (Artifact, error) {
	if _, err := s.Header(ctx, pursuitID); err != nil {
		return Artifact{}, err
	}
	if !IsValidArtifactStatus(status) {
		return Artifact{}, fmt.Errorf("unknown artifact status %q, expected one of: %s",
			status, strings.Join(ArtifactStatuses(), ", "))
	}
	a, err := scanArtifact(s.pool.QueryRow(ctx, `
		UPDATE mem_jobhunt_artifacts SET
			status      = $3,
			artifact_id = COALESCE($4::uuid, artifact_id)
		WHERE id = $1::uuid AND pursuit_id = $2::uuid
		RETURNING `+artifactColumns+`
	`, id, pursuitID, status, artifactID))
	if err != nil {
		return Artifact{}, fmt.Errorf("set artifact status: %w", err)
	}
	return a, nil
}
