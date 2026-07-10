// Package mandate is the per-task DEFINITION OF DONE.
//
// A Mandate is a contract: a title, a summary, and a list of binary, testable
// acceptance criteria. The agent opens one for non-trivial work, checks each
// criterion off with evidence, and CANNOT close it until every criterion
// passes — the done-gate is enforced here in Go (Close), never in skill prose,
// so the LLM can't forget it (Rule #1b). high_stakes mandates additionally
// require a passing Crosscheck (a second LLM vendor auditing the result).
//
// The judgment — when to open one, how to decompose a task into binary
// criteria, when it's high-stakes — lives in the seeded `frame-the-mandate`
// skill. This package is the mechanic.
package mandate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Status values for a mandate.
const (
	StatusOpen      = "open"
	StatusVerifying = "verifying"
	StatusDone      = "done"
	StatusAbandoned = "abandoned"
)

// Criterion status values.
const (
	CritPending = "pending"
	CritPass    = "pass"
	CritFail    = "fail"
)

// Criterion is one binary, testable acceptance condition.
type Criterion struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Status   string `json:"status"`   // pending | pass | fail
	Evidence string `json:"evidence"` // how it was verified
}

// Mandate is a task's definition of done.
type Mandate struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"session_id,omitempty"`
	Title      string         `json:"title"`
	Summary    string         `json:"summary"`
	Status     string         `json:"status"`
	Criteria   []Criterion    `json:"criteria"`
	HighStakes bool           `json:"high_stakes"`
	Importance *int           `json:"importance,omitempty"`
	Source     string         `json:"source"`
	VerifiedAt *time.Time     `json:"verified_at,omitempty"`
	Crosscheck map[string]any `json:"crosscheck,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// AllPass reports whether every criterion is marked pass (and there is at least
// one). An empty criteria list is NOT done — a mandate with nothing to prove is
// a mandate that proves nothing.
func (m *Mandate) AllPass() bool {
	if m == nil || len(m.Criteria) == 0 {
		return false
	}
	for _, c := range m.Criteria {
		if c.Status != CritPass {
			return false
		}
	}
	return true
}

// FailingCriteria returns the texts of criteria not yet passed, for a useful
// gate-refusal message.
func (m *Mandate) FailingCriteria() []string {
	var out []string
	for _, c := range m.Criteria {
		if c.Status != CritPass {
			label := c.Text
			if c.Status == CritFail {
				label += " (failing)"
			}
			out = append(out, label)
		}
	}
	return out
}

// Announcer is how a closed mandate reaches the boss without the mandate
// package importing push/initiative. serve.go provides the impl (a push.Sender
// adapter). Nil-safe — when unset, closing a mandate just doesn't announce.
type Announcer interface {
	Announce(ctx context.Context, m *Mandate)
}

// Store is the persistence + done-gate layer over mem_mandates.
type Store struct {
	pool      *pgxpool.Pool
	announcer Announcer
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// SetAnnouncer injects the ambient "done" announcer after construction.
func (s *Store) SetAnnouncer(a Announcer) {
	if s != nil {
		s.announcer = a
	}
}

// Open inserts a new mandate. Criterion ids are assigned when missing so check
// targets are stable. Returns the new id.
func (s *Store) Open(ctx context.Context, m *Mandate) (string, error) {
	if s == nil || s.pool == nil {
		return "", errors.New("mandate store not configured")
	}
	if strings.TrimSpace(m.Title) == "" {
		return "", errors.New("mandate title required")
	}
	for i := range m.Criteria {
		m.Criteria[i].Text = strings.TrimSpace(m.Criteria[i].Text)
		if m.Criteria[i].ID == "" {
			m.Criteria[i].ID = fmt.Sprintf("c%d", i+1)
		}
		if m.Criteria[i].Status == "" {
			m.Criteria[i].Status = CritPending
		}
	}
	if m.Source == "" {
		m.Source = "agent"
	}
	critJSON, _ := json.Marshal(m.Criteria)
	var sessionArg any
	if strings.TrimSpace(m.SessionID) != "" {
		sessionArg = m.SessionID
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mem_mandates
			(session_id, title, summary, status, criteria, high_stakes, importance, source)
		VALUES ($1::uuid, $2, $3, 'open', $4::jsonb, $5, $6, $7)
		RETURNING id::text
	`, sessionArg, m.Title, m.Summary, string(critJSON), m.HighStakes, m.Importance, m.Source).Scan(&id)
	return id, err
}

// Get loads one mandate by id.
func (s *Store) Get(ctx context.Context, id string) (*Mandate, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("mandate store not configured")
	}
	return scanOne(s.pool.QueryRow(ctx, selectCols+` WHERE id = $1::uuid`, id))
}

// GetOpenForSession returns the most-recently-updated open/verifying mandate
// for a session, or nil when there is none (the session provider's read).
func (s *Store) GetOpenForSession(ctx context.Context, sessionID string) (*Mandate, error) {
	if s == nil || s.pool == nil || strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}
	m, err := scanOne(s.pool.QueryRow(ctx, selectCols+`
		WHERE session_id = $1::uuid AND status IN ('open','verifying')
		ORDER BY updated_at DESC LIMIT 1`, sessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

// List returns recent mandates, optionally filtered to active (open/verifying).
func (s *Store) List(ctx context.Context, activeOnly bool, limit int) ([]*Mandate, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := selectCols
	if activeOnly {
		q += ` WHERE status IN ('open','verifying')`
	}
	q += ` ORDER BY updated_at DESC LIMIT $1`
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Mandate
	for rows.Next() {
		m, err := scanRows(rows)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CheckCriterion sets one criterion's status + evidence (load-mutate-save; the
// single-user concurrency profile makes this safe and keeps the JSONB handling
// in Go rather than fragile jsonb_set paths). Unknown ids are an error so a
// typo doesn't silently no-op.
func (s *Store) CheckCriterion(ctx context.Context, mandateID, critID, status, evidence string) error {
	if s == nil || s.pool == nil {
		return errors.New("mandate store not configured")
	}
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case CritPass, CritFail, CritPending:
	default:
		return fmt.Errorf("invalid criterion status %q (want pass|fail|pending)", status)
	}
	m, err := s.Get(ctx, mandateID)
	if err != nil {
		return err
	}
	found := false
	for i := range m.Criteria {
		if m.Criteria[i].ID == critID || strings.EqualFold(m.Criteria[i].Text, critID) {
			m.Criteria[i].Status = status
			if strings.TrimSpace(evidence) != "" {
				m.Criteria[i].Evidence = strings.TrimSpace(evidence)
			}
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("no criterion %q on this mandate", critID)
	}
	return s.writeCriteria(ctx, mandateID, m.Criteria)
}

// SetCrosscheck records a crosscheck verdict on the mandate: the per-criterion
// results are folded into criteria, and on an overall pass verified_at is
// stamped (which clears the high_stakes done-gate). Called by the crosscheck
// package.
func (s *Store) SetCrosscheck(ctx context.Context, mandateID string, verdict map[string]any, passed bool) error {
	if s == nil || s.pool == nil {
		return errors.New("mandate store not configured")
	}
	vJSON, _ := json.Marshal(verdict)
	verifiedClause := ""
	if passed {
		verifiedClause = ", verified_at = NOW()"
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_mandates
		   SET crosscheck = $2::jsonb, updated_at = NOW()`+verifiedClause+`
		 WHERE id = $1::uuid
	`, mandateID, string(vJSON))
	return err
}

// Close is the DONE-GATE. It refuses unless every criterion passes, and — for a
// high_stakes mandate — unless a passing crosscheck has stamped verified_at.
// This is the load-bearing mechanic: "loop until verified" is enforced in code,
// not trusted to the model. On success it flips status to done and fires the
// ambient announcer.
func (s *Store) Close(ctx context.Context, mandateID string) (*Mandate, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("mandate store not configured")
	}
	m, err := s.Get(ctx, mandateID)
	if err != nil {
		return nil, err
	}
	if m.Status == StatusDone {
		return m, nil
	}
	if !m.AllPass() {
		return nil, fmt.Errorf("can't close this mandate yet — not done: %s. Check each off with mandate_check (with evidence) once it's actually satisfied",
			strings.Join(m.FailingCriteria(), "; "))
	}
	if m.HighStakes && m.VerifiedAt == nil {
		return nil, errors.New("this is a high-stakes mandate — run mandate_verify first: a fresh, independent pass audits the result against the criteria; it can't close on your word alone")
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE mem_mandates SET status = 'done', updated_at = NOW() WHERE id = $1::uuid
	`, mandateID); err != nil {
		return nil, err
	}
	m.Status = StatusDone
	// Ambient "done" announcement — the PAI TTS-on-Stop equivalent, but a push.
	if s.announcer != nil {
		go s.announcer.Announce(context.WithoutCancel(ctx), m)
	}
	return m, nil
}

// Abandon drops a mandate (boss changed direction, task no longer applies).
func (s *Store) Abandon(ctx context.Context, mandateID string) error {
	if s == nil || s.pool == nil {
		return errors.New("mandate store not configured")
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_mandates SET status = 'abandoned', updated_at = NOW()
		 WHERE id = $1::uuid AND status IN ('open','verifying')
	`, mandateID)
	return err
}

func (s *Store) writeCriteria(ctx context.Context, mandateID string, crit []Criterion) error {
	critJSON, _ := json.Marshal(crit)
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_mandates SET criteria = $2::jsonb, updated_at = NOW() WHERE id = $1::uuid
	`, mandateID, string(critJSON))
	return err
}

const selectCols = `
	SELECT id::text, COALESCE(session_id::text,''), title, summary, status,
	       criteria, high_stakes, importance, source, verified_at, crosscheck,
	       created_at, updated_at
	  FROM mem_mandates`

type scanner interface {
	Scan(dest ...any) error
}

func scanOne(row pgx.Row) (*Mandate, error) { return scanInto(row) }
func scanRows(row pgx.Rows) (*Mandate, error) { return scanInto(row) }

func scanInto(row scanner) (*Mandate, error) {
	var (
		m           Mandate
		critRaw     []byte
		crossRaw    []byte
		importance  *int
		verifiedAt  *time.Time
	)
	if err := row.Scan(&m.ID, &m.SessionID, &m.Title, &m.Summary, &m.Status,
		&critRaw, &m.HighStakes, &importance, &m.Source, &verifiedAt, &crossRaw,
		&m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	m.Importance = importance
	m.VerifiedAt = verifiedAt
	if len(critRaw) > 0 {
		_ = json.Unmarshal(critRaw, &m.Criteria)
	}
	// A stored JSON `null` (mandate opened with no criteria) unmarshals to a
	// nil slice, which would re-marshal as `"criteria": null` and crash array
	// reads in Studio. Always serve [].
	if m.Criteria == nil {
		m.Criteria = []Criterion{}
	}
	if len(crossRaw) > 0 {
		_ = json.Unmarshal(crossRaw, &m.Crosscheck)
	}
	return &m, nil
}
