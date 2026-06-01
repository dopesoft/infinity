package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Lab API. Powers /lab in Studio.
//
// /lab is the single surface that replaces the old /code-proposals and
// /gym pages. Three tabs, three concrete things the boss should see:
//
//   1. Fix this    - actionable proposals Jarvis has surfaced. Open
//                    curiosity questions + open code proposals, in
//                    plain English with an Approve & fix button that
//                    seeds a Live session running the
//                    self-improve-from-finding skill.
//
//   2. Lessons     - what Jarvis has learned from past sessions. Reads
//                    mem_training_examples and presents them as
//                    "On {date}, from {source}, I learned: {lesson}".
//                    The same rows are already injected into every
//                    turn's system prompt via plasticity.Provider, so
//                    this tab proves the learning IS active rather
//                    than fantasy.
//
//   3. Skills evolved - skills Voyager has auto-promoted (mem_skills
//                       where source IN ('voyager', 'evolved')). Empty
//                       today, populates as the auto-promotion loop
//                       fires.
//
// All reads are best-effort: a degraded DB still renders the page with
// empty arrays so the boss can see what's expected even when nothing
// has happened yet.

type labProposal struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"` // curiosity | code_proposal
	Title     string    `json:"title"`
	Context   string    `json:"context,omitempty"`
	FilePath  string    `json:"file_path,omitempty"`
	Diff      string    `json:"diff,omitempty"`
	Risk      string    `json:"risk,omitempty"`
	Source    string    `json:"source,omitempty"` // for curiosity questions: high_surprise|contradiction|cron_failure|...
	CreatedAt time.Time `json:"created_at"`
}

type labLesson struct {
	ID         string    `json:"id"`
	TaskKind   string    `json:"task_kind"`
	Label      string    `json:"label"`
	Input      string    `json:"input"`
	Output     string    `json:"output"`
	Score      float64   `json:"score"`
	SourceKind string    `json:"source_kind"`
	CreatedAt  time.Time `json:"created_at"`
}

type labSkill struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Source      string    `json:"source"`
	Version     int       `json:"version"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type labResolved struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`     // curiosity | code_proposal | heartbeat_finding
	Title          string    `json:"title"`
	Source         string    `json:"source,omitempty"`
	Outcome        string    `json:"outcome"`  // resolved | dismissed | approved | applied
	OutcomeReason  string    `json:"outcome_reason,omitempty"`
	AppliedBy      string    `json:"applied_by,omitempty"` // auto | manual (code_proposal only)
	ResolvedAt     time.Time `json:"resolved_at"`
}

type labResponse struct {
	Proposals []labProposal      `json:"proposals"`
	Resolved  []labResolved      `json:"resolved"`
	Lessons   []labLesson        `json:"lessons"`
	Skills    []labSkill         `json:"skills"`
	Counts    labCounts          `json:"counts"`
	Gym       labGymSnapshot     `json:"gym"`
}

type labCounts struct {
	OpenProposals    int `json:"open_proposals"`
	RecentlyResolved int `json:"recently_resolved"`
	Lessons          int `json:"lessons"`
	EvolvedSkills    int `json:"evolved_skills"`
	GymCandidates    int `json:"gym_candidates"`
}

// labGymSnapshot exposes the plasticity tables (mem_training_examples,
// mem_adapter_evals, mem_policy_routes) so the Lab page can show what
// would graduate to a training run when Phase 9 ships GPU infra.
//
// Today only Candidates carries data - the rest render as empty-state
// scaffolding pointing the boss at PHASE_9_GYM_TRAINING.md.
type labGymSnapshot struct {
	Candidates   []labGymCandidate   `json:"candidates"`
	AdapterEvals []labAdapterEval    `json:"adapter_evals"`
	Routes       []labPolicyRoute    `json:"routes"`
}

type labGymCandidate struct {
	SourceKind string `json:"source_kind"`
	TaskKind   string `json:"task_kind"`
	Count      int    `json:"count"`
	AvgScore   float64 `json:"avg_score"`
	LatestAt   time.Time `json:"latest_at"`
}

type labAdapterEval struct {
	ID             string    `json:"id"`
	AdapterID      string    `json:"adapter_id"`
	BaselineScore  float64   `json:"baseline_score"`
	CandidateScore float64   `json:"candidate_score"`
	Passed         bool      `json:"passed"`
	CreatedAt      time.Time `json:"created_at"`
}

type labPolicyRoute struct {
	ID              string `json:"id"`
	TaskKind        string `json:"task_kind"`
	ActiveAdapterID string `json:"active_adapter_id"`
	Status          string `json:"status"`
}

func (s *Server) handleLab(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.pool == nil {
		writeJSON(w, http.StatusOK, labResponse{
			Proposals: []labProposal{},
			Lessons:   []labLesson{},
			Skills:    []labSkill{},
		})
		return
	}
	ctx := r.Context()
	resp := labResponse{
		Proposals: loadLabProposals(ctx, s.pool),
		Resolved:  loadLabResolved(ctx, s.pool),
		Lessons:   loadLabLessons(ctx, s.pool),
		Skills:    loadLabSkills(ctx, s.pool),
		Gym:       loadLabGym(ctx, s.pool),
	}
	resp.Counts.OpenProposals = len(resp.Proposals)
	resp.Counts.RecentlyResolved = len(resp.Resolved)
	resp.Counts.Lessons = len(resp.Lessons)
	resp.Counts.EvolvedSkills = len(resp.Skills)
	for _, c := range resp.Gym.Candidates {
		resp.Counts.GymCandidates += c.Count
	}
	writeJSON(w, http.StatusOK, resp)
}

// loadLabGym reads the plasticity substrate (migration 022) and returns
// today's training-example buckets plus the still-empty adapter eval and
// policy route tables. The Lab "Gym" tab renders all three as empty-state
// scaffolding until Phase 9 wires GPU training.
func loadLabGym(ctx context.Context, pool *pgxpool.Pool) labGymSnapshot {
	out := labGymSnapshot{
		Candidates:   []labGymCandidate{},
		AdapterEvals: []labAdapterEval{},
		Routes:       []labPolicyRoute{},
	}
	// Candidates: group by (source_kind, task_kind) - what would train if
	// you ran a dataset build today. Best-effort; degrade quietly.
	rows, err := pool.Query(ctx, `
		SELECT COALESCE(source_kind, ''),
		       COALESCE(task_kind,   ''),
		       COUNT(*),
		       COALESCE(AVG(score)::float8, 0),
		       MAX(created_at)
		  FROM mem_training_examples
		 GROUP BY source_kind, task_kind
		 ORDER BY COUNT(*) DESC
		 LIMIT 50
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var c labGymCandidate
			if err := rows.Scan(&c.SourceKind, &c.TaskKind, &c.Count, &c.AvgScore, &c.LatestAt); err == nil {
				out.Candidates = append(out.Candidates, c)
			}
		}
	}
	// Adapter evals (still empty until Phase 9 - keep the query so it
	// quietly lights up when the sidecar ships).
	if rs, err := pool.Query(ctx, `
		SELECT id::text, adapter_id::text, baseline_score, candidate_score, passed, created_at
		  FROM mem_adapter_evals
		 ORDER BY created_at DESC
		 LIMIT 20
	`); err == nil {
		defer rs.Close()
		for rs.Next() {
			var e labAdapterEval
			if err := rs.Scan(&e.ID, &e.AdapterID, &e.BaselineScore, &e.CandidateScore, &e.Passed, &e.CreatedAt); err == nil {
				out.AdapterEvals = append(out.AdapterEvals, e)
			}
		}
	}
	if rs, err := pool.Query(ctx, `
		SELECT id::text, task_kind, COALESCE(active_adapter_id::text, ''), status
		  FROM mem_policy_routes
		 ORDER BY task_kind ASC
		 LIMIT 50
	`); err == nil {
		defer rs.Close()
		for rs.Next() {
			var p labPolicyRoute
			if err := rs.Scan(&p.ID, &p.TaskKind, &p.ActiveAdapterID, &p.Status); err == nil {
				out.Routes = append(out.Routes, p)
			}
		}
	}
	return out
}

// loadLabResolved fetches the recently-closed curiosity questions,
// heartbeat findings, and code proposals so Lab's Recently-fixed tab
// can show "what was wrong + what I did about it" instead of just an
// open-issue queue. Capped to the last 30 days so the page stays fast
// even with a large archive.
func loadLabResolved(ctx context.Context, pool *pgxpool.Pool) []labResolved {
	out := make([]labResolved, 0, 60)
	if rows, err := pool.Query(ctx, `
		SELECT id::text,
		       question,
		       COALESCE(source_kind,''),
		       status,
		       COALESCE(resolved_reason,''),
		       COALESCE(resolved_at, created_at) AS resolved_at
		  FROM mem_curiosity_questions
		 WHERE status IN ('dismissed','answered','approved')
		   AND COALESCE(resolved_at, created_at) > NOW() - INTERVAL '30 days'
		 ORDER BY COALESCE(resolved_at, created_at) DESC
		 LIMIT 50
	`); err == nil {
		for rows.Next() {
			var r labResolved
			if err := rows.Scan(&r.ID, &r.Title, &r.Source, &r.Outcome, &r.OutcomeReason, &r.ResolvedAt); err == nil {
				r.Kind = "curiosity"
				out = append(out, r)
			}
		}
		rows.Close()
	}
	if rows, err := pool.Query(ctx, `
		SELECT id::text,
		       title,
		       COALESCE(kind,''),
		       status,
		       COALESCE(resolved_at, created_at) AS resolved_at
		  FROM mem_heartbeat_findings
		 WHERE status IN ('resolved','dismissed')
		   AND COALESCE(resolved_at, created_at) > NOW() - INTERVAL '30 days'
		 ORDER BY COALESCE(resolved_at, created_at) DESC
		 LIMIT 50
	`); err == nil {
		for rows.Next() {
			var r labResolved
			if err := rows.Scan(&r.ID, &r.Title, &r.Source, &r.Outcome, &r.ResolvedAt); err == nil {
				r.Kind = "heartbeat_finding"
				out = append(out, r)
			}
		}
		rows.Close()
	}
	if rows, err := pool.Query(ctx, `
		SELECT id::text, title, status, COALESCE(decision_note,''), decided_at
		  FROM mem_code_proposals
		 WHERE status IN ('approved','applied','rejected')
		   AND COALESCE(decided_at, created_at) > NOW() - INTERVAL '30 days'
		 ORDER BY COALESCE(decided_at, created_at) DESC
		 LIMIT 50
	`); err == nil {
		for rows.Next() {
			var r labResolved
			var note string
			if err := rows.Scan(&r.ID, &r.Title, &r.Outcome, &note, &r.ResolvedAt); err == nil {
				r.Kind = "code_proposal"
				r.OutcomeReason = note
				// The autonomous self-improve loop prefixes its notes with
				// [auto] (see nightly-self-improve skill) so the Lab can show
				// the boss it landed without him.
				if strings.HasPrefix(strings.TrimSpace(note), "[auto]") {
					r.AppliedBy = "auto"
				} else {
					r.AppliedBy = "manual"
				}
				out = append(out, r)
			}
		}
		rows.Close()
	}
	// Sort by resolved_at desc across the three sources. Simple insertion
	// since each source returns at most 50 rows already sorted, the
	// final list is small enough for an in-memory sort.
	sortResolvedByRecency(out)
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

func sortResolvedByRecency(rs []labResolved) {
	for i := 1; i < len(rs); i++ {
		j := i
		for j > 0 && rs[j-1].ResolvedAt.Before(rs[j].ResolvedAt) {
			rs[j-1], rs[j] = rs[j], rs[j-1]
			j--
		}
	}
}

func loadLabProposals(ctx context.Context, pool *pgxpool.Pool) []labProposal {
	out := make([]labProposal, 0, 32)
	// Curiosity questions - the actionable "Jarvis noticed something"
	// stream. source_kind tells the FE how to frame the proposal:
	// high_surprise = "a prediction missed", cron_failure = "your
	// cron broke", contradiction = "two memories disagree", etc.
	if rows, err := pool.Query(ctx, `
		SELECT id::text, question, COALESCE(rationale,''),
		       COALESCE(source_kind,''), created_at
		  FROM mem_curiosity_questions
		 WHERE status = 'open'
		 ORDER BY importance DESC NULLS LAST, created_at DESC
		 LIMIT 50
	`); err == nil {
		for rows.Next() {
			var p labProposal
			if err := rows.Scan(&p.ID, &p.Title, &p.Context, &p.Source, &p.CreatedAt); err == nil {
				p.Kind = "curiosity"
				out = append(out, p)
			}
		}
		rows.Close()
	}
	// Code proposals from Voyager's source extractor. Empty most of
	// the time today; the table is populated when the extractor sees
	// the boss fight the same file 3+ times in a session.
	if rows, err := pool.Query(ctx, `
		SELECT id::text, title, COALESCE(rationale,''),
		       COALESCE(target_path,''),
		       COALESCE(proposed_change,''),
		       COALESCE(risk_level,''),
		       created_at
		  FROM mem_code_proposals
		 WHERE status = 'candidate'
		 ORDER BY created_at DESC
		 LIMIT 50
	`); err == nil {
		for rows.Next() {
			var p labProposal
			if err := rows.Scan(&p.ID, &p.Title, &p.Context, &p.FilePath, &p.Diff, &p.Risk, &p.CreatedAt); err == nil {
				p.Kind = "code_proposal"
				out = append(out, p)
			}
		}
		rows.Close()
	}
	return out
}

func loadLabLessons(ctx context.Context, pool *pgxpool.Pool) []labLesson {
	out := make([]labLesson, 0, 50)
	if rows, err := pool.Query(ctx, `
		SELECT id::text, COALESCE(task_kind,'') AS task_kind,
		       COALESCE(label,'') AS label,
		       COALESCE(input_text,'') AS input,
		       COALESCE(output_text,'') AS output,
		       COALESCE(score, 0)::float8 AS score,
		       COALESCE(source_kind,'') AS source_kind,
		       created_at
		  FROM mem_training_examples
		 ORDER BY created_at DESC
		 LIMIT 100
	`); err == nil {
		for rows.Next() {
			var l labLesson
			if err := rows.Scan(&l.ID, &l.TaskKind, &l.Label, &l.Input, &l.Output, &l.Score, &l.SourceKind, &l.CreatedAt); err == nil {
				out = append(out, l)
			}
		}
		rows.Close()
	}
	return out
}

func loadLabSkills(ctx context.Context, pool *pgxpool.Pool) []labSkill {
	out := make([]labSkill, 0, 16)
	// Show only autonomously-promoted skills here. Manually-created
	// ones live in /skills; this tab is specifically about "Jarvis
	// taught itself something new" so the boss can see the AGI loop
	// closing.
	if rows, err := pool.Query(ctx, `
		SELECT id::text, name, COALESCE(description,''),
		       COALESCE(source,''), COALESCE(version,1), updated_at
		  FROM mem_skills
		 WHERE source IN ('voyager','evolved','auto')
		   AND status = 'active'
		 ORDER BY updated_at DESC
		 LIMIT 50
	`); err == nil {
		for rows.Next() {
			var sk labSkill
			if err := rows.Scan(&sk.ID, &sk.Name, &sk.Description, &sk.Source, &sk.Version, &sk.UpdatedAt); err == nil {
				out = append(out, sk)
			}
		}
		rows.Close()
	}
	return out
}
