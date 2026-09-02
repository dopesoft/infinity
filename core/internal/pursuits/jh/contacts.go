package jh

// The people side of the job hunt: hiring managers, in-house recruiters, and
// anyone else worth reaching directly.
//
// This lives beside store.go rather than inside it because the two halves are
// read at different times. The pipeline is walked every time the cockpit opens;
// contacts are touched when outreach actually happens. Same package, same
// Store, same Header guard.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The outreach ladder, in the order a contact climbs it. Mirrors
// chk_jobhunt_contacts_outreach_status in 207_jobhunt_support.sql exactly; if
// that constraint ever widens, this list is the other half of the change.
const (
	ContactStatusIdentified = "identified"
	ContactStatusDrafted    = "drafted"
	ContactStatusSent       = "sent"
	ContactStatusReplied    = "replied"
	ContactStatusDead       = "dead"
)

// ContactStatuses enumerates every accepted outreach_status.
func ContactStatuses() []string {
	return []string{
		ContactStatusIdentified,
		ContactStatusDrafted,
		ContactStatusSent,
		ContactStatusReplied,
		ContactStatusDead,
	}
}

// IsValidContactStatus reports whether the supplied string is one the database
// will accept.
func IsValidContactStatus(status string) bool {
	for _, s := range ContactStatuses() {
		if s == status {
			return true
		}
	}
	return false
}

// Contact is one person attached to the hunt.
//
// RoleID is a pointer because the column is nullable and clears rather than
// cascades: someone the boss has actually spoken to outlives the posting that
// introduced them, and collapsing that to the zero value would quietly lose the
// distinction between "not attached to a role" and "attached to role 0".
// OutreachSentAt is a pointer for the same reason — never sent and sent at the
// zero time are different facts.
type Contact struct {
	ID             string     `json:"id"`
	PursuitID      string     `json:"pursuit_id"`
	RoleID         *string    `json:"role_id"`
	Name           string     `json:"name"`
	Title          string     `json:"title"`
	Company        string     `json:"company"`
	LinkedInURL    string     `json:"linkedin_url"`
	Email          string     `json:"email"`
	OutreachStatus string     `json:"outreach_status"`
	OutreachSentAt *time.Time `json:"outreach_sent_at"`
	LastMessage    string     `json:"last_message"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ContactInput is the write payload. A struct rather than positional arguments
// because it is five consecutive optional strings, which is precisely the shape
// that gets transposed at a call site and never caught by the compiler.
type ContactInput struct {
	// ID selects an existing row to update. Blank inserts a new one. There is
	// no natural key on this table to conflict on the way roles have
	// (source, external_id) — two people can share a name at one company — so
	// identity is explicit rather than inferred.
	ID          string
	RoleID      *string
	Name        string
	Title       string
	Company     string
	LinkedInURL string
	Email       string
	LastMessage string
	// OutreachStatus must be one of ContactStatuses(). Blank means leave it
	// alone on update, or start at identified on insert.
	OutreachStatus string
}

// contactColumns is the SELECT list every contact read shares, so a column
// added to one read cannot go missing from another.
const contactColumns = `
	id::text, pursuit_id::text, role_id::text, name, title, company,
	linkedin_url, email, outreach_status, outreach_sent_at, last_message,
	created_at, updated_at`

// scanContact reads one row in contactColumns order.
//
// The nullable text columns are scanned through *string and flattened to "",
// because a caller rendering a contact card wants an empty string, not a nil
// dereference. RoleID keeps its pointer: there the absence is meaningful.
func scanContact(row interface{ Scan(...any) error }) (Contact, error) {
	var c Contact
	var roleID, title, company, linkedIn, email, lastMessage *string
	if err := row.Scan(
		&c.ID, &c.PursuitID, &roleID, &c.Name, &title, &company,
		&linkedIn, &email, &c.OutreachStatus, &c.OutreachSentAt, &lastMessage,
		&c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return Contact{}, err
	}
	c.RoleID = roleID
	c.Title = derefText(title)
	c.Company = derefText(company)
	c.LinkedInURL = derefText(linkedIn)
	c.Email = derefText(email)
	c.LastMessage = derefText(lastMessage)
	return c, nil
}

// derefText flattens a nullable text column to a plain string.
func derefText(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// textOrNil is the inverse: an empty field is stored as NULL rather than as an
// empty string, so "unknown" and "known to be blank" do not become the same
// value in the database.
func textOrNil(s string) *string {
	s = clampText(s)
	if s == "" {
		return nil
	}
	return &s
}

// Contacts returns every contact on the pursuit, waiting-on-a-reply first.
//
// The ordering is deliberate rather than alphabetical. 'sent' is the status
// that carries an obligation — a message is out and nothing has come back — so
// it leads, and within it the oldest send is first, because the one that has
// been silent longest is the one that needs chasing. Everything else falls back
// to newest-first.
func (s *Store) Contacts(ctx context.Context, pursuitID string) ([]Contact, error) {
	if _, err := s.Header(ctx, pursuitID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+contactColumns+`
		FROM mem_jobhunt_contacts
		WHERE pursuit_id = $1::uuid
		ORDER BY
			CASE WHEN outreach_status = 'sent' THEN 0 ELSE 1 END,
			outreach_sent_at ASC NULLS LAST,
			created_at DESC
	`, pursuitID)
	if err != nil {
		return nil, fmt.Errorf("list contacts: %w", err)
	}
	defer rows.Close()
	out := []Contact{}
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertContact inserts a new contact, or updates the one named by in.ID.
//
// The update is written with COALESCE so a partial payload patches rather than
// blanks: a caller that only knows a newly discovered email must not wipe the
// title and company someone already researched. Passing a field through as
// empty therefore means "leave it", which is the safe reading — clearing a
// field is rarer than filling one in, and a caller that truly wants a field
// gone can say so with an explicit update later.
func (s *Store) UpsertContact(ctx context.Context, pursuitID string, in ContactInput) (Contact, error) {
	if _, err := s.Header(ctx, pursuitID); err != nil {
		return Contact{}, err
	}

	status := strings.TrimSpace(in.OutreachStatus)
	if status != "" && !IsValidContactStatus(status) {
		return Contact{}, fmt.Errorf("unknown contact status %q, expected one of: %s",
			in.OutreachStatus, strings.Join(ContactStatuses(), ", "))
	}

	// Update path: the row already exists and is named explicitly.
	if id := strings.TrimSpace(in.ID); id != "" {
		c, err := scanContact(s.pool.QueryRow(ctx, `
			UPDATE mem_jobhunt_contacts SET
				role_id         = COALESCE($3::uuid, role_id),
				name            = COALESCE($4, name),
				title           = COALESCE($5, title),
				company         = COALESCE($6, company),
				linkedin_url    = COALESCE($7, linkedin_url),
				email           = COALESCE($8, email),
				last_message    = COALESCE($9, last_message),
				outreach_status = COALESCE($10, outreach_status),
				updated_at      = NOW()
			WHERE id = $1::uuid AND pursuit_id = $2::uuid
			RETURNING `+contactColumns+`
		`, id, pursuitID, in.RoleID, textOrNil(in.Name), textOrNil(in.Title),
			textOrNil(in.Company), textOrNil(in.LinkedInURL), textOrNil(in.Email),
			textOrNil(in.LastMessage), textOrNil(status)))
		if err != nil {
			return Contact{}, fmt.Errorf("update contact: %w", err)
		}
		return c, nil
	}

	// Insert path. name is the one genuinely required field: a contact without
	// one is not a thinner record, it is an unusable one.
	name := clampText(in.Name)
	if name == "" {
		return Contact{}, errors.New("name required")
	}
	if status == "" {
		status = ContactStatusIdentified
	}
	c, err := scanContact(s.pool.QueryRow(ctx, `
		INSERT INTO mem_jobhunt_contacts
			(pursuit_id, role_id, name, title, company, linkedin_url, email,
			 last_message, outreach_status)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+contactColumns+`
	`, pursuitID, in.RoleID, name, textOrNil(in.Title), textOrNil(in.Company),
		textOrNil(in.LinkedInURL), textOrNil(in.Email), textOrNil(in.LastMessage),
		status))
	if err != nil {
		return Contact{}, fmt.Errorf("add contact: %w", err)
	}
	return c, nil
}

// SetContactStatus moves a contact along the outreach ladder.
//
// Moving to 'sent' stamps outreach_sent_at in the same statement rather than
// leaving the caller to remember it. That timestamp is what Contacts orders on
// and what the cockpit reads as "waiting since", so a status set without it
// would produce a contact that is provably awaiting a reply but sorts as though
// it never went out. The stamp is only written on the first move to sent, so a
// re-save does not reset the clock the boss is measuring silence against.
func (s *Store) SetContactStatus(ctx context.Context, pursuitID, contactID, status string) (Contact, error) {
	if _, err := s.Header(ctx, pursuitID); err != nil {
		return Contact{}, err
	}
	if !IsValidContactStatus(status) {
		return Contact{}, fmt.Errorf("unknown contact status %q, expected one of: %s",
			status, strings.Join(ContactStatuses(), ", "))
	}
	c, err := scanContact(s.pool.QueryRow(ctx, `
		UPDATE mem_jobhunt_contacts SET
			outreach_status  = $3,
			outreach_sent_at = CASE
				WHEN $3 = 'sent' AND outreach_sent_at IS NULL THEN NOW()
				ELSE outreach_sent_at
			END,
			updated_at       = NOW()
		WHERE id = $1::uuid AND pursuit_id = $2::uuid
		RETURNING `+contactColumns+`
	`, contactID, pursuitID, status))
	if err != nil {
		return Contact{}, fmt.Errorf("set contact status: %w", err)
	}
	return c, nil
}
