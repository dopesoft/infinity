// Package eval implements the verification substrate — Phase 4 of the
// assembly substrate.
//
// As the agent assembles more (skills, workflows, runtime tools), it must
// be able to prove its assemblies work and notice when one regresses.
// mem_evals is the generic outcome ledger; Scorecard rolls a subject's
// outcomes into a success rate + a recent-vs-historical trend.
//
// The agent records via eval_record / reads via eval_scorecard. The
// workflow engine auto-records every run on completion.
package eval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Outcome is the result of one evaluated run.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
	OutcomePartial Outcome = "partial"
)

func (o Outcome) Valid() bool {
	return o == OutcomeSuccess || o == OutcomeFailure || o == OutcomePartial
}

// Eval is one recorded outcome.
type Eval struct {
	ID          string    `json:"id"`
	SubjectKind string    `json:"subjectKind"` // skill | workflow | tool | extension
	SubjectName string    `json:"subjectName"`
	RunID       string    `json:"runId,omitempty"`
	Outcome     Outcome   `json:"outcome"`
	Score       *int      `json:"score,omitempty"`
	Notes       string    `json:"notes,omitempty"`
	Source      string    `json:"source"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Scorecard is the rollup for one subject.
type Scorecard struct {
	SubjectKind   string  `json:"subjectKind"`
	SubjectName   string  `json:"subjectName"`
	Total         int     `json:"total"`
	SuccessRate   float64 `json:"successRate"`   // over the whole window
	RecentRate    float64 `json:"recentRate"`    // last <=5 outcomes
	PriorRate     float64 `json:"priorRate"`     // the 5 before that
	Trend         string  `json:"trend"`         // improving | steady | regressing
	AvgScore      *int    `json:"avgScore,omitempty"`
	Regressing    bool    `json:"regressing"`
	LastOutcome   Outcome `json:"lastOutcome,omitempty"`
	LastNote      string  `json:"lastNote,omitempty"`
}

// Store is the persistence boundary for mem_evals.
type Store struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewStore(pool *pgxpool.Pool, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{pool: pool, logger: logger}
}

// Record persists one outcome.
func (s *Store) Record(ctx context.Context, e *Eval) error {
	if s == nil || s.pool == nil {
		return errors.New("eval: no pool")
	}
	if e.SubjectKind == "" || e.SubjectName == "" {
		return errors.New("eval: subject_kind and subject_name are required")
	}
	if !e.Outcome.Valid() {
		return fmt.Errorf("eval: invalid outcome %q (want success|failure|partial)", e.Outcome)
	}
	if e.Source == "" {
		e.Source = "agent"
	}
	if e.Score != nil {
		if *e.Score < 0 {
			*e.Score = 0
		}
		if *e.Score > 100 {
			*e.Score = 100
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_evals
		  (subject_kind, subject_name, run_id, outcome, score, notes, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, e.SubjectKind, e.SubjectName, e.RunID, string(e.Outcome), e.Score, e.Notes, e.Source)
	if err != nil {
		return fmt.Errorf("eval: record: %w", err)
	}
	return nil
}

// Scorecard rolls up the last `window` outcomes for a subject into a
// success rate + a recent-vs-prior trend. window<=0 defaults to 50.
func (s *Store) Scorecard(ctx context.Context, subjectKind, subjectName string, window int) (*Scorecard, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("eval: no pool")
	}
	if window <= 0 || window > 500 {
		window = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT outcome, score, notes
		  FROM mem_evals
		 WHERE subject_kind = $1 AND subject_name = $2
		 ORDER BY created_at DESC
		 LIMIT $3
	`, subjectKind, subjectName, window)
	if err != nil {
		return nil, fmt.Errorf("eval: scorecard: %w", err)
	}
	defer rows.Close()

	type row struct {
		outcome Outcome
		score   *int
		notes   string
	}
	var evals []row
	for rows.Next() {
		var (
			outcome string
			score   *int16
			notes   string
		)
		if err := rows.Scan(&outcome, &score, &notes); err != nil {
			return nil, err
		}
		var sc *int
		if score != nil {
			v := int(*score)
			sc = &v
		}
		evals = append(evals, row{Outcome(outcome), sc, notes})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	card := &Scorecard{SubjectKind: subjectKind, SubjectName: subjectName, Total: len(evals)}
	if len(evals) == 0 {
		card.Trend = "steady"
		return card, nil
	}

	// evals[0] is newest. Whole-window rate, score average.
	successCount, scoreSum, scoreN := 0, 0, 0
	for _, e := range evals {
		if e.outcome == OutcomeSuccess {
			successCount++
		}
		if e.score != nil {
			scoreSum += *e.score
			scoreN++
		}
	}
	card.SuccessRate = round2(float64(successCount) / float64(len(evals)))
	if scoreN > 0 {
		avg := scoreSum / scoreN
		card.AvgScore = &avg
	}
	card.LastOutcome = evals[0].outcome
	card.LastNote = evals[0].notes

	// Recent (last <=5) vs prior (the 5 before that).
	rate := func(from, to int) float64 {
		if from >= to {
			return 0
		}
		ok := 0
		for i := from; i < to; i++ {
			if evals[i].outcome == OutcomeSuccess {
				ok++
			}
		}
		return round2(float64(ok) / float64(to-from))
	}
	card.RecentRate = rate(0, min(5, len(evals)))
	if len(evals) > 5 {
		card.PriorRate = rate(5, min(10, len(evals)))
	} else {
		card.PriorRate = card.RecentRate
	}
	switch {
	case card.RecentRate < card.PriorRate-0.15:
		card.Trend = "regressing"
		card.Regressing = true
	case card.RecentRate > card.PriorRate+0.15:
		card.Trend = "improving"
	default:
		card.Trend = "steady"
	}
	return card, nil
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
