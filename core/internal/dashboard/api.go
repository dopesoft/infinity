// Package dashboard backs the Studio /dashboard surface.
//
// The single aggregator endpoint (GET /api/dashboard) returns every
// dashboard section in one round trip. Sections sourced from tables
// introduced in migration 014 (mem_tasks, mem_pursuits, mem_followups,
// mem_saved, mem_calendar_events) are populated from the live database;
// sections backed by data still being wired (agent_work, activity,
// reflection-of-day, approvals) return nil so the Studio layer can fall
// back to the local mock fixture without throwing.
//
// As more upstream surfaces land (e.g. mem_reflections is already there;
// it just needs querying) they slot into this aggregator and Studio's
// fallback for the matching section drops out automatically.
package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type API struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
}

func NewAPI(pool *pgxpool.Pool, logger *slog.Logger) *API {
	if logger == nil {
		logger = slog.Default()
	}
	return &API{Pool: pool, Logger: logger}
}

func (a *API) Routes(mux *http.ServeMux) {
	if a == nil {
		return
	}
	mux.HandleFunc("/api/dashboard", a.handleDashboard)
}

// Response is the single payload returned to Studio. Each section is a
// (possibly nil) array — nil means "backend doesn't serve this yet, use
// your local mock," empty array means "backend has nothing to surface."
type Response struct {
	Pursuits       []Pursuit       `json:"pursuits"`
	Todos          []Todo          `json:"todos"`
	CalendarEvents []CalendarEvent `json:"calendarEvents"`
	FollowUps      []FollowUp      `json:"followUps"`
	Saved          []Saved         `json:"saved"`
	Approvals      []Approval      `json:"approvals"`
	Reflection     *Reflection     `json:"reflection,omitempty"`
	Activity       []ActivityEvent `json:"activity"`
	Work           []WorkItem      `json:"work"`
	MemoryStats    *MemoryStats    `json:"memoryStats,omitempty"`
}

// ── DTOs (mirror studio/lib/dashboard/types.ts) ───────────────────────────

type Pursuit struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Cadence      string     `json:"cadence"`
	DoneToday    bool       `json:"doneToday"`
	DoneAt       *time.Time `json:"doneAt,omitempty"`
	StreakDays   int        `json:"streakDays"`
	CurrentValue *float64   `json:"currentValue,omitempty"`
	TargetValue  *float64   `json:"targetValue,omitempty"`
	Unit         *string    `json:"unit,omitempty"`
	DueAt        *time.Time `json:"dueAt,omitempty"`
	Status       *string    `json:"status,omitempty"`
}

type Todo struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Body     string     `json:"body,omitempty"`
	Done     bool       `json:"done"`
	DueAt    *time.Time `json:"dueAt,omitempty"`
	Priority string     `json:"priority,omitempty"`
	Source   string     `json:"source"`
}

type PrepItem struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Done      bool   `json:"done"`
	Rationale string `json:"rationale,omitempty"`
}

type CalendarEvent struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	StartsAt       time.Time  `json:"startsAt"`
	EndsAt         *time.Time `json:"endsAt,omitempty"`
	Location       string     `json:"location,omitempty"`
	Attendees      []string   `json:"attendees,omitempty"`
	Classification string     `json:"classification"`
	Prep           []PrepItem `json:"prep"`
}

type FollowUp struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	Account    string    `json:"account,omitempty"`
	From       string    `json:"from"`
	Subject    string    `json:"subject,omitempty"`
	Preview    string    `json:"preview"`
	Body       string    `json:"body,omitempty"`
	ThreadURL  string    `json:"threadUrl,omitempty"`
	Draft      string    `json:"draft,omitempty"`
	Unread     bool      `json:"unread"`
	ReceivedAt time.Time `json:"receivedAt"`
}

type Saved struct {
	ID              string    `json:"id"`
	Kind            string    `json:"kind"`
	Title           string    `json:"title"`
	Body            string    `json:"body,omitempty"`
	URL             string    `json:"url,omitempty"`
	Source          string    `json:"source,omitempty"`
	ReadingMinutes  *int      `json:"readingMinutes,omitempty"`
	SavedAt         time.Time `json:"savedAt"`
}

type MemoryStats struct {
	NewToday      int `json:"newToday"`
	PromotedToday int `json:"promotedToday"`
	Procedural    int `json:"procedural"`
	StreakDays    int `json:"streakDays"`
}

type Reflection struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Body          string    `json:"body"`
	CapturedAt    time.Time `json:"capturedAt"`
	EvidenceCount int       `json:"evidenceCount"`
}

type Approval struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`              // trust_bash | trust_edit | trust_write | code_proposal | curiosity
	Title     string    `json:"title"`
	Subtitle  string    `json:"subtitle,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	// Trust-specific (carried as a generic map so the FE can render the
	// JSON args as a code block in ObjectViewer).
	ToolCall *ToolCall `json:"toolCall,omitempty"`
	// Code-proposal specific
	Diff      string `json:"diff,omitempty"`
	FilePath  string `json:"filePath,omitempty"`
	RiskLevel string `json:"riskLevel,omitempty"`
	Rationale string `json:"rationale,omitempty"`
	// Curiosity specific
	Question string `json:"question,omitempty"`
	Context  string `json:"context,omitempty"`
}

type ToolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type ActivityEvent struct {
	ID     string    `json:"id"`
	Kind   string    `json:"kind"` // scheduled | completed | alert | memory | reflection
	Title  string    `json:"title"`
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
	Future bool      `json:"future,omitempty"`
}

// WorkItem mirrors studio/lib/dashboard/types.ts WorkItem. Each row maps
// onto a column in the agent-work Kanban: queued (scheduled for later),
// running (in-flight), awaiting (needs boss decision), done (finished
// today).
type WorkItem struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"` // cron_run|voyager_opt|sentinel|skill_run|trust|code_proposal|curiosity|memory_op|reflection
	Title        string     `json:"title"`
	Subtitle     string     `json:"subtitle,omitempty"`
	Column       string     `json:"column"` // queued|running|awaiting|done
	ScheduledFor *time.Time `json:"scheduledFor,omitempty"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	DurationMs   *int       `json:"durationMs,omitempty"`
	DetailHref   string     `json:"detailHref,omitempty"`
}

// ── handler ───────────────────────────────────────────────────────────────

func (a *API) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.Pool == nil {
		writeJSON(w, http.StatusOK, Response{})
		return
	}
	ctx := r.Context()
	resp := Response{}

	// Each section is independent — if one query fails we log and
	// return the rest. Studio falls back to mock for missing pieces.
	if p, err := a.loadPursuits(ctx); err != nil {
		a.Logger.Warn("dashboard: pursuits", "err", err)
	} else {
		resp.Pursuits = p
	}
	if t, err := a.loadTodos(ctx); err != nil {
		a.Logger.Warn("dashboard: todos", "err", err)
	} else {
		resp.Todos = t
	}
	if e, err := a.loadCalendar(ctx); err != nil {
		a.Logger.Warn("dashboard: calendar", "err", err)
	} else {
		resp.CalendarEvents = e
	}
	if f, err := a.loadFollowUps(ctx); err != nil {
		a.Logger.Warn("dashboard: followups", "err", err)
	} else {
		resp.FollowUps = f
	}
	if s, err := a.loadSaved(ctx); err != nil {
		a.Logger.Warn("dashboard: saved", "err", err)
	} else {
		resp.Saved = s
	}
	if m, err := a.loadMemoryStats(ctx); err != nil {
		a.Logger.Warn("dashboard: memory stats", "err", err)
	} else {
		resp.MemoryStats = m
	}
	if r, err := a.loadReflection(ctx); err != nil {
		a.Logger.Warn("dashboard: reflection", "err", err)
	} else {
		resp.Reflection = r
	}
	if a2, err := a.loadApprovals(ctx); err != nil {
		a.Logger.Warn("dashboard: approvals", "err", err)
	} else {
		resp.Approvals = a2
	}
	if e, err := a.loadActivity(ctx); err != nil {
		a.Logger.Warn("dashboard: activity", "err", err)
	} else {
		resp.Activity = e
	}
	if wi, err := a.loadWork(ctx); err != nil {
		a.Logger.Warn("dashboard: work", "err", err)
	} else {
		resp.Work = wi
	}

	writeJSON(w, http.StatusOK, resp)
}

// ── loaders ───────────────────────────────────────────────────────────────

func (a *API) loadPursuits(ctx context.Context) ([]Pursuit, error) {
	if a.Pool == nil {
		return nil, errors.New("no pool")
	}
	rows, err := a.Pool.Query(ctx, `
		SELECT id, title, cadence, done_today, done_at, streak_days,
		       current_value, target_value, unit, due_at, status
		FROM mem_pursuits
		ORDER BY
			CASE cadence WHEN 'daily' THEN 0 WHEN 'weekly' THEN 1 ELSE 2 END,
			title
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Pursuit
	for rows.Next() {
		var p Pursuit
		var status *string
		if err := rows.Scan(
			&p.ID, &p.Title, &p.Cadence, &p.DoneToday, &p.DoneAt, &p.StreakDays,
			&p.CurrentValue, &p.TargetValue, &p.Unit, &p.DueAt, &status,
		); err != nil {
			return nil, err
		}
		if status != nil && *status != "" {
			p.Status = status
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (a *API) loadTodos(ctx context.Context) ([]Todo, error) {
	rows, err := a.Pool.Query(ctx, `
		SELECT id, title, body, status = 'done', due_at, priority, source
		FROM mem_tasks
		WHERE status IN ('open', 'done')
		ORDER BY
			CASE status WHEN 'open' THEN 0 ELSE 1 END,
			CASE priority WHEN 'high' THEN 0 WHEN 'med' THEN 1 ELSE 2 END,
			due_at NULLS LAST,
			created_at DESC
		LIMIT 200
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Todo
	for rows.Next() {
		var t Todo
		if err := rows.Scan(&t.ID, &t.Title, &t.Body, &t.Done, &t.DueAt, &t.Priority, &t.Source); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (a *API) loadCalendar(ctx context.Context) ([]CalendarEvent, error) {
	// Forward-looking window: 6 months from today, plus events still
	// active right now (ends_at >= now). Past events are excluded —
	// the dashboard surface is "what's coming," not history.
	rows, err := a.Pool.Query(ctx, `
		SELECT id, title, starts_at, ends_at, location,
		       COALESCE(attendees, '[]'::jsonb), classification,
		       COALESCE(prep, '[]'::jsonb)
		FROM mem_calendar_events
		WHERE (ends_at IS NULL AND starts_at >= NOW() - INTERVAL '1 day')
		   OR ends_at >= NOW()
		ORDER BY starts_at ASC
		LIMIT 200
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalendarEvent
	for rows.Next() {
		var e CalendarEvent
		var attendeesRaw, prepRaw []byte
		if err := rows.Scan(
			&e.ID, &e.Title, &e.StartsAt, &e.EndsAt, &e.Location,
			&attendeesRaw, &e.Classification, &prepRaw,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(attendeesRaw, &e.Attendees)
		_ = json.Unmarshal(prepRaw, &e.Prep)
		if e.Prep == nil {
			e.Prep = []PrepItem{}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (a *API) loadFollowUps(ctx context.Context) ([]FollowUp, error) {
	rows, err := a.Pool.Query(ctx, `
		SELECT id, source, account, from_name, subject, preview, body, thread_url,
		       draft, unread, received_at
		FROM mem_followups
		WHERE status = 'open'
		   OR (status = 'snoozed' AND snoozed_until < NOW())
		ORDER BY received_at DESC
		LIMIT 50
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FollowUp
	for rows.Next() {
		var f FollowUp
		if err := rows.Scan(
			&f.ID, &f.Source, &f.Account, &f.From, &f.Subject,
			&f.Preview, &f.Body, &f.ThreadURL, &f.Draft, &f.Unread, &f.ReceivedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (a *API) loadSaved(ctx context.Context) ([]Saved, error) {
	rows, err := a.Pool.Query(ctx, `
		SELECT id, kind, title, body, url, source_label, reading_minutes, saved_at
		FROM mem_saved
		WHERE read_at IS NULL
		ORDER BY saved_at DESC
		LIMIT 50
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Saved
	for rows.Next() {
		var s Saved
		var url, source *string
		if err := rows.Scan(&s.ID, &s.Kind, &s.Title, &s.Body, &url, &source, &s.ReadingMinutes, &s.SavedAt); err != nil {
			return nil, err
		}
		if url != nil {
			s.URL = *url
		}
		if source != nil {
			s.Source = *source
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// loadMemoryStats reads the same numbers shown in the dashboard's
// footer strip — daily memory growth + procedural count + a streak
// approximation (consecutive days with at least one new memory).
func (a *API) loadMemoryStats(ctx context.Context) (*MemoryStats, error) {
	stats := &MemoryStats{}
	// New today
	if err := a.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mem_memories
		WHERE created_at >= date_trunc('day', NOW())
	`).Scan(&stats.NewToday); err != nil {
		// If the table doesn't exist yet (early boot, fresh DB), return
		// a zeroed stats block rather than erroring the whole endpoint.
		return stats, nil
	}
	// Promoted today (anything that left tier='working' since midnight)
	_ = a.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mem_memories
		WHERE updated_at >= date_trunc('day', NOW())
		  AND tier IN ('episodic', 'semantic', 'procedural')
	`).Scan(&stats.PromotedToday)
	// Procedural count
	_ = a.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mem_memories WHERE tier = 'procedural'
	`).Scan(&stats.Procedural)
	// Streak: consecutive days with ≥1 new memory.
	_ = a.Pool.QueryRow(ctx, `
		WITH days AS (
			SELECT DISTINCT date_trunc('day', created_at)::date AS d
			FROM mem_memories
			WHERE created_at >= NOW() - INTERVAL '60 days'
		),
		gaps AS (
			SELECT d,
			       d - (ROW_NUMBER() OVER (ORDER BY d DESC))::int AS grp
			FROM days
			WHERE d <= CURRENT_DATE
		)
		SELECT COUNT(*) FROM gaps
		WHERE grp = (SELECT grp FROM gaps ORDER BY d DESC LIMIT 1)
	`).Scan(&stats.StreakDays)
	return stats, nil
}

// loadReflection returns the most recent high-quality reflection from the
// metacognition loop. Title is derived from the first sentence; the rest
// goes into Body. Returns nil when no reflection exists yet.
func (a *API) loadReflection(ctx context.Context) (*Reflection, error) {
	row := a.Pool.QueryRow(ctx, `
		SELECT id::text, critique, lessons, created_at
		FROM mem_reflections
		ORDER BY created_at DESC
		LIMIT 1
	`)
	var (
		id, critique string
		lessonsRaw   []byte
		createdAt    time.Time
	)
	if err := row.Scan(&id, &critique, &lessonsRaw, &createdAt); err != nil {
		// "no rows" is the normal empty-DB case — not an error.
		return nil, nil //nolint:nilerr
	}
	title, body := splitTitleBody(critique)
	evidenceCount := 0
	var lessons []any
	if err := json.Unmarshal(lessonsRaw, &lessons); err == nil {
		evidenceCount = len(lessons)
	}
	return &Reflection{
		ID:            id,
		Title:         title,
		Body:          body,
		CapturedAt:    createdAt,
		EvidenceCount: evidenceCount,
	}, nil
}

// loadApprovals unions three "needs you" surfaces — trust contracts,
// code proposals, curiosity questions — into the same Approval shape
// the dashboard's Approvals card already consumes.
func (a *API) loadApprovals(ctx context.Context) ([]Approval, error) {
	out := make([]Approval, 0, 16)

	// Trust contracts pending. action_spec.tool drives the kind so the
	// FE can show the right icon (bash vs edit vs write).
	trustRows, err := a.Pool.Query(ctx, `
		SELECT id::text, title, source, action_spec, risk_level, reasoning,
		       preview, created_at
		FROM mem_trust_contracts
		WHERE status = 'pending'
		ORDER BY created_at DESC
		LIMIT 50
	`)
	if err != nil {
		return nil, err
	}
	for trustRows.Next() {
		var (
			id, title, source, risk, reasoning, preview string
			actionRaw                                   []byte
			created                                     time.Time
		)
		if err := trustRows.Scan(&id, &title, &source, &actionRaw, &risk, &reasoning, &preview, &created); err != nil {
			trustRows.Close()
			return nil, err
		}
		var action map[string]any
		_ = json.Unmarshal(actionRaw, &action)
		kind := "trust_bash"
		toolName := ""
		if t, ok := action["tool"].(string); ok && t != "" {
			toolName = t
			// claude_code__Edit / claude_code__Write / claude_code__Bash
			low := strings.ToLower(t)
			switch {
			case strings.Contains(low, "edit"):
				kind = "trust_edit"
			case strings.Contains(low, "write"):
				kind = "trust_write"
			case strings.Contains(low, "bash"):
				kind = "trust_bash"
			}
		}
		ap := Approval{
			ID:        id,
			Kind:      kind,
			Title:     title,
			Subtitle:  toolName,
			CreatedAt: created,
			RiskLevel: risk,
			Rationale: reasoning,
		}
		if toolName != "" {
			args, _ := action["args"].(map[string]any)
			if args == nil {
				// Some callers store the full action minus the routing tag —
				// fall back to the whole map sans `tool` so the boss still
				// sees the args in the viewer.
				args = map[string]any{}
				for k, v := range action {
					if k == "tool" || k == "session_id" {
						continue
					}
					args[k] = v
				}
			}
			ap.ToolCall = &ToolCall{Name: toolName, Args: args}
		}
		if preview != "" && reasoning == "" {
			ap.Rationale = preview
		}
		out = append(out, ap)
	}
	trustRows.Close()

	// Code proposals candidate.
	codeRows, err := a.Pool.Query(ctx, `
		SELECT id::text, title, target_path, rationale, proposed_change,
		       risk_level, created_at
		FROM mem_code_proposals
		WHERE status = 'candidate'
		ORDER BY created_at DESC
		LIMIT 30
	`)
	if err != nil {
		return nil, err
	}
	for codeRows.Next() {
		var (
			id, title, rationale, change, risk string
			targetPath                         *string
			created                            time.Time
		)
		if err := codeRows.Scan(&id, &title, &targetPath, &rationale, &change, &risk, &created); err != nil {
			codeRows.Close()
			return nil, err
		}
		path := ""
		if targetPath != nil {
			path = *targetPath
		}
		out = append(out, Approval{
			ID:        id,
			Kind:      "code_proposal",
			Title:     title,
			Subtitle:  path,
			CreatedAt: created,
			Diff:      change,
			FilePath:  path,
			RiskLevel: risk,
			Rationale: rationale,
		})
	}
	codeRows.Close()

	// Curiosity questions open.
	curRows, err := a.Pool.Query(ctx, `
		SELECT id::text, question, rationale, importance, created_at
		FROM mem_curiosity_questions
		WHERE status = 'open'
		ORDER BY importance DESC, created_at DESC
		LIMIT 20
	`)
	if err != nil {
		return nil, err
	}
	for curRows.Next() {
		var (
			id, question, rationale string
			importance              int
			created                 time.Time
		)
		if err := curRows.Scan(&id, &question, &rationale, &importance, &created); err != nil {
			curRows.Close()
			return nil, err
		}
		title := question
		if len(title) > 80 {
			title = title[:78] + "…"
		}
		out = append(out, Approval{
			ID:        id,
			Kind:      "curiosity",
			Title:     title,
			Subtitle:  "Jarvis has a question",
			CreatedAt: created,
			Question:  question,
			Context:   rationale,
		})
	}
	curRows.Close()

	// Sort the merged slice by created_at DESC for a consistent order.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// loadActivity unions heartbeat findings + recent reflections + sentinel
// fires into a single time-ordered stream. Capped at 40 rows so the
// payload stays small.
func (a *API) loadActivity(ctx context.Context) ([]ActivityEvent, error) {
	out := make([]ActivityEvent, 0, 40)

	hbRows, err := a.Pool.Query(ctx, `
		SELECT id::text, kind, title, detail, created_at
		FROM mem_heartbeat_findings
		ORDER BY created_at DESC
		LIMIT 20
	`)
	if err == nil {
		for hbRows.Next() {
			var (
				id, kind, title, detail string
				createdAt               time.Time
			)
			if err := hbRows.Scan(&id, &kind, &title, &detail, &createdAt); err != nil {
				hbRows.Close()
				return nil, err
			}
			out = append(out, ActivityEvent{
				ID:     "hb-" + id,
				Kind:   activityKindFromFinding(kind),
				Title:  title,
				Detail: detail,
				At:     createdAt,
			})
		}
		hbRows.Close()
	}

	refRows, err := a.Pool.Query(ctx, `
		SELECT id::text, critique, created_at
		FROM mem_reflections
		ORDER BY created_at DESC
		LIMIT 10
	`)
	if err == nil {
		for refRows.Next() {
			var (
				id, critique string
				createdAt    time.Time
			)
			if err := refRows.Scan(&id, &critique, &createdAt); err != nil {
				refRows.Close()
				return nil, err
			}
			title, _ := splitTitleBody(critique)
			out = append(out, ActivityEvent{
				ID:    "ref-" + id,
				Kind:  "reflection",
				Title: "Reflection: " + title,
				At:    createdAt,
			})
		}
		refRows.Close()
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].At.After(out[j].At)
	})
	if len(out) > 40 {
		out = out[:40]
	}
	return out, nil
}

// loadWork unions live cron/sentinel/skill-run/trust/code-proposal rows
// into the Kanban shape. One small query per source — no joins — keeps
// the payload predictable and each column independently fail-safe.
//
// Column policy:
//   - queued    → enabled crons with next_run_at > now, plus voyager
//                 skill proposals still awaiting verifier decision.
//   - running   → enabled sentinels (always watching), plus skill runs
//                 in-flight (started but not ended).
//   - awaiting  → pending trust contracts + candidate code proposals.
//                 These also appear in Approvals — that's intentional;
//                 the Kanban is a *status board*, the Approvals card is
//                 the decision surface.
//   - done      → today's completed cron runs + completed skill runs.
func (a *API) loadWork(ctx context.Context) ([]WorkItem, error) {
	if a.Pool == nil {
		return nil, errors.New("no pool")
	}
	const perCol = 10
	out := make([]WorkItem, 0, perCol*4)

	// ── queued: upcoming crons ────────────────────────────────────────
	cronQRows, err := a.Pool.Query(ctx, `
		SELECT id::text, name, schedule_natural, schedule, next_run_at
		FROM mem_crons
		WHERE enabled = TRUE AND next_run_at IS NOT NULL AND next_run_at > NOW()
		ORDER BY next_run_at ASC
		LIMIT $1
	`, perCol)
	if err == nil {
		for cronQRows.Next() {
			var (
				id, name             string
				natural, schedule    *string
				nextRunAt            time.Time
			)
			if err := cronQRows.Scan(&id, &name, &natural, &schedule, &nextRunAt); err != nil {
				cronQRows.Close()
				return nil, err
			}
			sub := "scheduled"
			if natural != nil && *natural != "" {
				sub = "scheduled · " + *natural
			} else if schedule != nil && *schedule != "" {
				sub = "scheduled · " + *schedule
			}
			nra := nextRunAt
			out = append(out, WorkItem{
				ID:           "cron-q-" + id,
				Kind:         "cron_run",
				Title:        name,
				Subtitle:     sub,
				Column:       "queued",
				ScheduledFor: &nra,
				DetailHref:   "/cron",
			})
		}
		cronQRows.Close()
	}

	// Voyager skill proposals waiting for verifier / decision.
	propRows, err := a.Pool.Query(ctx, `
		SELECT id::text, name, COALESCE(parent_skill, '')
		FROM mem_skill_proposals
		WHERE status = 'candidate'
		ORDER BY created_at DESC
		LIMIT $1
	`, perCol)
	if err == nil {
		for propRows.Next() {
			var id, name, parent string
			if err := propRows.Scan(&id, &name, &parent); err != nil {
				propRows.Close()
				return nil, err
			}
			title := "Voyager: verify " + name
			sub := "verifier queued"
			if parent != "" {
				title = "Voyager: patch " + parent
				sub = "GEPA proposal · " + name
			}
			out = append(out, WorkItem{
				ID:         "vop-" + id,
				Kind:       "voyager_opt",
				Title:      title,
				Subtitle:   sub,
				Column:     "queued",
				DetailHref: "/skills",
			})
		}
		propRows.Close()
	}

	// ── running: sentinels (always-on) + in-flight skill runs ─────────
	sentRows, err := a.Pool.Query(ctx, `
		SELECT id::text, name, watch_type, cooldown_seconds
		FROM mem_sentinels
		WHERE enabled = TRUE
		ORDER BY created_at DESC
		LIMIT $1
	`, perCol)
	if err == nil {
		for sentRows.Next() {
			var (
				id, name, watch string
				cooldown        int
			)
			if err := sentRows.Scan(&id, &name, &watch, &cooldown); err != nil {
				sentRows.Close()
				return nil, err
			}
			out = append(out, WorkItem{
				ID:         "sent-" + id,
				Kind:       "sentinel",
				Title:      "Sentinel: " + name,
				Subtitle:   watch + " · cooldown " + humanSeconds(cooldown),
				Column:     "running",
				DetailHref: "/cron",
			})
		}
		sentRows.Close()
	}

	// Skill runs that started but haven't ended yet (in-flight). Cap at
	// last hour so a crashed never-ended row doesn't leak into the UI
	// forever.
	skillRunningRows, err := a.Pool.Query(ctx, `
		SELECT id::text, skill_name, trigger_source, started_at
		FROM mem_skill_runs
		WHERE ended_at IS NULL AND started_at >= NOW() - INTERVAL '1 hour'
		ORDER BY started_at DESC
		LIMIT $1
	`, perCol)
	if err == nil {
		for skillRunningRows.Next() {
			var id, name, trigger string
			var startedAt time.Time
			if err := skillRunningRows.Scan(&id, &name, &trigger, &startedAt); err != nil {
				skillRunningRows.Close()
				return nil, err
			}
			s := startedAt
			out = append(out, WorkItem{
				ID:         "srun-" + id,
				Kind:       "skill_run",
				Title:      "Skill: " + name,
				Subtitle:   "via " + trigger,
				Column:     "running",
				StartedAt:  &s,
				DetailHref: "/skills",
			})
		}
		skillRunningRows.Close()
	}

	// ── awaiting: trust contracts + code proposals ────────────────────
	trustRows, err := a.Pool.Query(ctx, `
		SELECT id::text, title, action_spec, risk_level, created_at
		FROM mem_trust_contracts
		WHERE status = 'pending'
		ORDER BY created_at DESC
		LIMIT $1
	`, perCol)
	if err == nil {
		for trustRows.Next() {
			var (
				id, title, risk string
				actionRaw       []byte
				created         time.Time
			)
			if err := trustRows.Scan(&id, &title, &actionRaw, &risk, &created); err != nil {
				trustRows.Close()
				return nil, err
			}
			var action map[string]any
			_ = json.Unmarshal(actionRaw, &action)
			tool, _ := action["tool"].(string)
			sub := "needs approval"
			if tool != "" {
				sub = tool + " · " + risk
			} else if risk != "" {
				sub = risk + " · needs approval"
			}
			c := created
			out = append(out, WorkItem{
				ID:         "trust-" + id,
				Kind:       "trust",
				Title:      title,
				Subtitle:   sub,
				Column:     "awaiting",
				StartedAt:  &c,
				DetailHref: "/trust",
			})
		}
		trustRows.Close()
	}

	codeRows, err := a.Pool.Query(ctx, `
		SELECT id::text, title, target_path, risk_level, created_at
		FROM mem_code_proposals
		WHERE status = 'candidate'
		ORDER BY created_at DESC
		LIMIT $1
	`, perCol)
	if err == nil {
		for codeRows.Next() {
			var (
				id, title, risk string
				targetPath      *string
				created         time.Time
			)
			if err := codeRows.Scan(&id, &title, &targetPath, &risk, &created); err != nil {
				codeRows.Close()
				return nil, err
			}
			path := ""
			if targetPath != nil {
				path = *targetPath
			}
			sub := risk
			if path != "" {
				sub = path + " · " + risk
			}
			c := created
			out = append(out, WorkItem{
				ID:         "code-" + id,
				Kind:       "code_proposal",
				Title:      title,
				Subtitle:   sub,
				Column:     "awaiting",
				StartedAt:  &c,
				DetailHref: "/code-proposals",
			})
		}
		codeRows.Close()
	}

	// ── done: today's completed cron + skill runs ────────────────────
	cronDoneRows, err := a.Pool.Query(ctx, `
		SELECT id::text, name, last_run_at, last_run_status, last_run_duration_ms
		FROM mem_crons
		WHERE last_run_at IS NOT NULL
		  AND last_run_at >= date_trunc('day', NOW())
		ORDER BY last_run_at DESC
		LIMIT $1
	`, perCol)
	if err == nil {
		for cronDoneRows.Next() {
			var (
				id, name      string
				lastRunAt     time.Time
				status        *string
				durationMs    *int
			)
			if err := cronDoneRows.Scan(&id, &name, &lastRunAt, &status, &durationMs); err != nil {
				cronDoneRows.Close()
				return nil, err
			}
			sub := "completed"
			if status != nil && *status != "" {
				sub = *status
			}
			fa := lastRunAt
			item := WorkItem{
				ID:         "cron-d-" + id,
				Kind:       "cron_run",
				Title:      name,
				Subtitle:   sub,
				Column:     "done",
				FinishedAt: &fa,
				DetailHref: "/cron",
			}
			if durationMs != nil {
				d := *durationMs
				item.DurationMs = &d
			}
			out = append(out, item)
		}
		cronDoneRows.Close()
	}

	skillDoneRows, err := a.Pool.Query(ctx, `
		SELECT id::text, skill_name, ended_at, duration_ms, success
		FROM mem_skill_runs
		WHERE ended_at IS NOT NULL
		  AND ended_at >= date_trunc('day', NOW())
		ORDER BY ended_at DESC
		LIMIT $1
	`, perCol)
	if err == nil {
		for skillDoneRows.Next() {
			var (
				id, name   string
				endedAt    time.Time
				durationMs int
				success    bool
			)
			if err := skillDoneRows.Scan(&id, &name, &endedAt, &durationMs, &success); err != nil {
				skillDoneRows.Close()
				return nil, err
			}
			sub := "succeeded"
			if !success {
				sub = "failed"
			}
			fa := endedAt
			d := durationMs
			out = append(out, WorkItem{
				ID:         "srun-d-" + id,
				Kind:       "skill_run",
				Title:      "Skill: " + name,
				Subtitle:   sub,
				Column:     "done",
				FinishedAt: &fa,
				DurationMs: &d,
				DetailHref: "/skills",
			})
		}
		skillDoneRows.Close()
	}

	return out, nil
}

// humanSeconds renders a small seconds value as "Ns" / "Nm" / "Nh".
// Used for sentinel cooldown subtitles.
func humanSeconds(s int) string {
	switch {
	case s <= 0:
		return "0s"
	case s < 60:
		return strconv.Itoa(s) + "s"
	case s < 3600:
		return strconv.Itoa(s/60) + "m"
	default:
		return strconv.Itoa(s/3600) + "h"
	}
}

// activityKindFromFinding maps heartbeat finding kinds to the dashboard's
// activity kinds. `security` and `outcome` are alerts; everything else
// is memory-ish or generic.
func activityKindFromFinding(k string) string {
	switch k {
	case "security", "outcome":
		return "alert"
	case "surprise":
		return "completed"
	case "self_heal":
		return "completed"
	case "curiosity", "pattern":
		return "memory"
	default:
		return "memory"
	}
}

// splitTitleBody extracts a short title from a longer critique string —
// first sentence or first 80 chars, whichever's shorter.
func splitTitleBody(text string) (title, body string) {
	t := strings.TrimSpace(text)
	if t == "" {
		return "", ""
	}
	if i := strings.IndexAny(t, ".!?"); i > 0 && i < 200 {
		return strings.TrimSpace(t[:i+1]), strings.TrimSpace(t[i+1:])
	}
	if len(t) <= 80 {
		return t, ""
	}
	return t[:78] + "…", t[78:]
}

// ── helpers ───────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
