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
	"fmt"
	htmlpkg "html"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/connectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MessageFetcher retrieves the full rendered content (HTML + plain text +
// attachment metadata) of a single follow-up message. Implemented by
// connectors.MessageFetcher and injected in serve.go; kept as an interface so
// the API stays nil-safe (when unset, the message endpoint degrades to empty).
type MessageFetcher interface {
	FetchMessage(ctx context.Context, source, accountHint, messageID string) (html, text string, attachments []connectors.Attachment, err error)
}

type API struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	// Fetcher is the lazy email-body retriever for GET /api/followups/message.
	// Optional; nil degrades the Message pane to preview-only.
	Fetcher MessageFetcher
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
	mux.HandleFunc("/api/followups/dismiss", a.handleFollowupDismiss)
	mux.HandleFunc("/api/followups/message", a.handleFollowupMessage)
	// Boss-facing todo writes. The UI lands rows in the SAME mem_tasks
	// contract Jarvis reads/writes through his task_* tools - a manual
	// todo is therefore visible to the agent the moment it's created, no
	// separate store, no client-only state.
	mux.HandleFunc("/api/tasks/create", a.handleTaskCreate)
	mux.HandleFunc("/api/tasks/update", a.handleTaskUpdate)
	// The active plan for a chat session - powers the pinned chat dock so it
	// renders the same plan the dashboard Agent Work board shows (one substrate,
	// two synced views). The board itself carries plans inline via loadWork.
	mux.HandleFunc("/api/plans/active", a.handlePlanActive)
}

// Response is the single payload returned to Studio. Each section is a
// (possibly nil) array - nil means "backend doesn't serve this yet, use
// your local mock," empty array means "backend has nothing to surface."
type Response struct {
	Pursuits       []Pursuit       `json:"pursuits"`
	Todos          []Todo          `json:"todos"`
	CalendarEvents []CalendarEvent `json:"calendarEvents"`
	FollowUps      []FollowUp      `json:"followUps"`
	Saved          []Saved         `json:"saved"`
	Artifacts      []Artifact      `json:"artifacts"`
	Approvals      []Approval      `json:"approvals"`
	Reflection     *Reflection     `json:"reflection,omitempty"`
	Activity       []ActivityEvent `json:"activity"`
	Work           []WorkItem      `json:"work"`
	MemoryStats    *MemoryStats    `json:"memoryStats,omitempty"`
	// SurfaceItems is the generic surface contract: mem_surface_items
	// grouped by `surface` key. Studio renders each group with one
	// generic SurfaceCard.
	SurfaceItems map[string][]SurfaceItem `json:"surfaceItems"`
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
	CreatedAt    *time.Time `json:"createdAt,omitempty"`
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

// EventAttendee mirrors the calendar package shape one-for-one but
// stays in the dashboard DTO package so the studio fetcher doesn't have
// to import the calendar internal types.
type EventAttendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"displayName,omitempty"`
	ResponseStatus string `json:"responseStatus,omitempty"`
	Self           bool   `json:"self,omitempty"`
	Organizer      bool   `json:"organizer,omitempty"`
	Optional       bool   `json:"optional,omitempty"`
}

// EventOrganizer is the event organizer block surfaced to the modal.
type EventOrganizer struct {
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Self        bool   `json:"self,omitempty"`
}

// EventConferenceEntryPoint - one Join-link row (Meet, Zoom, dial-in).
type EventConferenceEntryPoint struct {
	Type  string `json:"type"`
	URI   string `json:"uri"`
	Label string `json:"label,omitempty"`
}

// EventConference - the conference data block for the modal's video row.
type EventConference struct {
	SolutionName string                      `json:"solutionName,omitempty"`
	IconURI      string                      `json:"iconUri,omitempty"`
	EntryPoints  []EventConferenceEntryPoint `json:"entryPoints,omitempty"`
}

type CalendarEvent struct {
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	StartsAt       time.Time       `json:"startsAt"`
	EndsAt         *time.Time      `json:"endsAt,omitempty"`
	AllDay         bool            `json:"allDay,omitempty"`
	Location       string          `json:"location,omitempty"`
	Description    string          `json:"description,omitempty"`
	Attendees      []EventAttendee `json:"attendees,omitempty"`
	Organizer      EventOrganizer  `json:"organizer,omitempty"`
	Conference     EventConference `json:"conference,omitempty"`
	Recurrence     []string        `json:"recurrence,omitempty"`
	HTMLLink       string          `json:"htmlLink,omitempty"`
	HangoutLink    string          `json:"hangoutLink,omitempty"`
	ResponseStatus string          `json:"responseStatus,omitempty"`
	AccountID      string          `json:"accountId,omitempty"`
	// AccountLabel is the human-readable label for the owning account
	// (alias if set, else discovered identity email, else empty). Lets
	// the Upcoming card render an "kai@dopesoft.io" chip without the
	// client having to load /api/calendar/accounts separately.
	AccountLabel   string     `json:"accountLabel,omitempty"`
	Classification string     `json:"classification"`
	Prep           []PrepItem `json:"prep"`
}

type FollowUp struct {
	ID      string `json:"id"`
	Source  string `json:"source"`
	Account string `json:"account,omitempty"`
	From    string `json:"from"`
	Subject string `json:"subject,omitempty"`
	Preview string `json:"preview"`
	Body    string `json:"body,omitempty"`
	// Summary is the agent's triage summary (urgency + what it's about),
	// rendered in the ObjectViewer's "Context" pane ABOVE the email. For
	// surface-origin rows this is the agent-authored body; connector-poll
	// rows have none (raw mail only) unless a recipe attached one.
	Summary   string `json:"summary,omitempty"`
	ThreadURL string `json:"threadUrl,omitempty"`
	Draft     string `json:"draft,omitempty"`
	// SentReply is the exact reply text the boss sent (persisted to metadata by
	// the send_reply action the moment Send is tapped). When present the viewer
	// renders a read-only "Response" section instead of the editable draft box.
	SentReply  string    `json:"sentReply,omitempty"`
	Unread     bool      `json:"unread"`
	ReceivedAt time.Time `json:"receivedAt"`
	// Origin discriminates which table this row came from so the dismiss
	// endpoint hits the right store. "followup" → mem_followups, "surface"
	// → mem_surface_items (the agent surfaced it via surface_item with
	// surface='followups' / 'inbox' / 'email').
	Origin string `json:"origin"`
	// Metadata is the generic chip carrier (account / intent / mode /
	// classification / anything else the agent attached). Studio renders
	// chips for known keys when present; unknown keys are ignored at the
	// chip layer but still visible in the ObjectViewer.
	Metadata map[string]any `json:"metadata,omitempty"`
	// Actions are the boss-tappable controls for this email (surface-origin
	// rows only — they carry the surface item's `actions`). Tapping POSTs to
	// /api/surface/action keyed by this row's ID (which IS the surface item id
	// for origin="surface"), which runs the action's intent as an agent turn.
	// Intent stays server-side; the client only needs id/label/style to render.
	Actions []FollowUpAction `json:"actions,omitempty"`
}

// FollowUpAction is the client-facing shape of a surface Action on a follow-up
// (id + label + style; the intent lives server-side and is resolved by
// /api/surface/action from the stored item, so it's never shipped to the UI).
type FollowUpAction struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Style string `json:"style,omitempty"`
}

// SurfaceItem mirrors core/internal/surface.Item - one row of the generic
// dashboard surface contract (mem_surface_items). The dashboard groups
// these by Surface and renders each group with the same generic card.
type SurfaceItem struct {
	ID               string         `json:"id"`
	Surface          string         `json:"surface"`
	Kind             string         `json:"kind"`
	Source           string         `json:"source"`
	ExternalID       string         `json:"externalId,omitempty"`
	Title            string         `json:"title"`
	Subtitle         string         `json:"subtitle,omitempty"`
	Body             string         `json:"body,omitempty"`
	URL              string         `json:"url,omitempty"`
	Importance       *int           `json:"importance,omitempty"`
	ImportanceReason string         `json:"importanceReason,omitempty"`
	Metadata         map[string]any `json:"metadata"`
	Status           string         `json:"status"`
	CreatedAt        time.Time      `json:"createdAt"`
	// UpdatedAt is when this card last SAID something new. Rolling cards (one
	// per cron, keyed on external_id) keep their original created_at forever,
	// so tonight's failure would otherwise render with the date the card was
	// first opened — months stale. The UI times and sorts on this.
	UpdatedAt time.Time `json:"updatedAt"`
	// Actions are the boss-tappable controls (Run it again / Not a routine /
	// Review proposal). They live on every surface item, not just follow-ups:
	// without them a card can be read but never cleared, which is how the
	// inbox silts up. The server-side `intent` never leaves the server.
	Actions []FollowUpAction `json:"actions,omitempty"`
}

type Saved struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	Title          string    `json:"title"`
	Body           string    `json:"body,omitempty"`
	URL            string    `json:"url,omitempty"`
	Source         string    `json:"source,omitempty"`
	ReadingMinutes *int      `json:"readingMinutes,omitempty"`
	SavedAt        time.Time `json:"savedAt"`
}

// Artifact mirrors studio/lib/dashboard/types.ts Artifact. A curated view of
// mem_artifacts (the canonical "things I've made" store) surfaced on the Saved
// card's "Made by Jarvis" segment. The storage fields drive the viewer's Open
// routing; this is read-only — generators still write rows via artifact_save.
type Artifact struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	VirtualPath string    `json:"virtualPath"`
	StorageKind string    `json:"storageKind"`
	StoragePath string    `json:"storagePath,omitempty"`
	StorageMime string    `json:"storageMime,omitempty"`
	Bridge      string    `json:"bridge,omitempty"`
	GithubURL   string    `json:"githubUrl,omitempty"`
	SourceTool  string    `json:"sourceTool,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	// Document-preview fields (from metadata JSONB) so the viewer can open a
	// generated doc straight into a fully-rendered canvas tab — not a bare
	// download card. Mirror the DocMeta shape the canvas store consumes.
	Format   string `json:"format,omitempty"`
	Bytes    int64  `json:"bytes,omitempty"`
	PDFPath  string `json:"pdfPath,omitempty"`
	HTMLPath string `json:"htmlPath,omitempty"`
	Markdown string `json:"markdown,omitempty"`
	// Origin session — lets a Saved click reopen the doc in its own chat (with
	// history) when that chat still exists, or start a fresh one with the doc
	// dropped in when it was deleted. SessionAlive is false when the source
	// session is gone (soft-deleted) or was never set.
	SourceSessionID string `json:"sourceSessionId,omitempty"`
	SessionAlive    bool   `json:"sessionAlive"`
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
	Kind      string    `json:"kind"` // trust_bash | trust_edit | trust_write | code_proposal | curiosity
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
	Kind   string    `json:"kind"` // scheduled | completed | alert | memory | reflection | system
	Title  string    `json:"title"`
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
	Future bool      `json:"future,omitempty"`
	// Dismiss is populated only for rows that the boss can clear from
	// their detail view (currently the folded operational "system" notes,
	// origin="surface"). Heartbeat findings / reflections leave it nil and
	// keep their "Open in Lab" deep-link instead.
	Dismiss *ActivityDismiss `json:"dismiss,omitempty"`
}

// ActivityDismiss carries the table + id an activity row maps back to so
// the dashboard can dismiss it through the existing dismiss endpoint
// without inventing a per-kind route.
type ActivityDismiss struct {
	Origin string `json:"origin"` // "surface"
	ID     string `json:"id"`
}

// WorkItem mirrors studio/lib/dashboard/types.ts WorkItem. Each row maps
// onto a column in the agent-work Kanban: queued (scheduled for later),
// running (in-flight), awaiting (needs boss decision), done (finished
// today).
type WorkItem struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // cron_run|voyager_opt|sentinel|skill_run|trust|code_proposal|curiosity|memory_op|reflection
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Column   string `json:"column"` // queued|running|awaiting|done
	// Summary is the run's narrative ("what it did / how it went / outcome"),
	// sourced from mem_runs.result_summary. Populated for finished cron runs so
	// the Done card and the ObjectViewer show a report instead of a bare "ok".
	Summary string `json:"summary,omitempty"`
	// Engine names the subsystem behind the item ("Voyager", "GEPA", "Schedule",
	// "Sentinel", "Skill", "Approval", "Code") so the card title can stay plain
	// and readable while the detail view shows what's actually driving it.
	Engine string `json:"engine,omitempty"`
	// Ref is the raw technical identifier (auto-generated skill name, cron
	// schedule, watch type) kept off the card title but shown in the detail for
	// anyone who needs the exact handle.
	Ref string `json:"ref,omitempty"`
	// SessionID is the agent turn behind a running/awaiting item, when known.
	// Carried so the Stop button can cancel the live turn (loop.CancelSession)
	// and close the right runs. Empty for items with no agent session (queued
	// crons, trust rows, code proposals).
	SessionID    string     `json:"sessionId,omitempty"`
	ScheduledFor *time.Time `json:"scheduledFor,omitempty"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	DurationMs   *int       `json:"durationMs,omitempty"`
	DetailHref   string     `json:"detailHref,omitempty"`
	// WorkflowSteps is populated only for Kind == "workflow" - the run's
	// step state-machine, carried inline so tapping the Kanban card opens
	// the drawer with the full workflow without a second fetch.
	WorkflowSteps []WorkflowStep `json:"workflowSteps,omitempty"`
	// PlanSteps is populated only for Kind == "plan" - the durable plan's
	// ordered steps with their status/verification, carried inline so the
	// Kanban card opens the full step timeline in ObjectViewer without a
	// second fetch (mirrors WorkflowSteps). DoneCount/TotalCount drive the
	// "4/7" progress bar rendered on the running card.
	PlanSteps  []PlanStep `json:"planSteps,omitempty"`
	DoneCount  *int       `json:"doneCount,omitempty"`
	TotalCount *int       `json:"totalCount,omitempty"`
	// Skills lists the skill(s) this item runs. For plans it's what the agent
	// actually invoked (from the session's skills_invoke calls); for crons it's
	// what the job is set to invoke (parsed from its instruction). Either way the
	// card shows the ingredients under the job headline.
	Skills []string `json:"skills,omitempty"`
	// Instruction is the plain-English "what this job does" — a cron's target
	// prompt, or a plan's goal. Surfaced so a queued/running item explains itself
	// in the detail without a trip to /cron.
	Instruction string `json:"instruction,omitempty"`
	// MandateCriteria is populated only for Kind == "mandate" — the binary
	// acceptance criteria with their pass/fail status + evidence, carried inline
	// so the Kanban card opens the full definition-of-done in ObjectViewer
	// (mirrors PlanSteps). DoneCount/TotalCount drive the same progress bar.
	MandateCriteria []MandateCriterionDTO `json:"mandateCriteria,omitempty"`
	// Crosscheck is the verification verdict for a high-stakes mandate (overall,
	// confidence, auditing model, notes), shown in the ObjectViewer. Empty until
	// mandate_verify has run.
	Crosscheck map[string]any `json:"crosscheck,omitempty"`
}

// MandateCriterionDTO is one acceptance criterion of a mandate WorkItem.
type MandateCriterionDTO struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Status   string `json:"status"` // pending | pass | fail
	Evidence string `json:"evidence,omitempty"`
}

// WorkflowStep is one step of a workflow run, surfaced inside a workflow
// WorkItem so the ObjectViewer can render the state-machine.
type WorkflowStep struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`   // tool | skill | agent | checkpoint
	Status string `json:"status"` // pending | running | done | failed | skipped | awaiting
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
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
	start := time.Now()
	resp := Response{}

	// Each section is an independent DB read. Running them sequentially meant
	// 11 round-trips to the Supabase pooler back-to-back = 5-7s of stacked
	// network latency for a dashboard that should paint instantly. Fan them
	// out: every loader runs concurrently, writes its own field under a mutex,
	// and total time collapses to the slowest single query. One slow/failed
	// section never blocks the rest (Studio falls back to mock for missing
	// pieces).
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	run := func(name string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				a.Logger.Warn("dashboard: "+name, "err", err)
			}
		}()
	}

	run("pursuits", func() error {
		v, err := a.loadPursuits(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		resp.Pursuits = v
		mu.Unlock()
		return nil
	})
	run("todos", func() error {
		v, err := a.loadTodos(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		resp.Todos = v
		mu.Unlock()
		return nil
	})
	run("calendar", func() error {
		v, err := a.loadCalendar(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		resp.CalendarEvents = v
		mu.Unlock()
		return nil
	})
	run("followups", func() error {
		v, err := a.loadFollowUps(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		resp.FollowUps = v
		mu.Unlock()
		return nil
	})
	run("saved", func() error {
		v, err := a.loadSaved(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		resp.Saved = v
		mu.Unlock()
		return nil
	})
	run("artifacts", func() error {
		v, err := a.loadArtifacts(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		resp.Artifacts = v
		mu.Unlock()
		return nil
	})
	run("memory_stats", func() error {
		v, err := a.loadMemoryStats(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		resp.MemoryStats = v
		mu.Unlock()
		return nil
	})
	run("reflection", func() error {
		v, err := a.loadReflection(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		resp.Reflection = v
		mu.Unlock()
		return nil
	})
	run("approvals", func() error {
		v, err := a.loadApprovals(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		resp.Approvals = v
		mu.Unlock()
		return nil
	})
	run("activity", func() error {
		v, err := a.loadActivity(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		resp.Activity = v
		mu.Unlock()
		return nil
	})
	run("work", func() error {
		v, err := a.loadWork(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		resp.Work = v
		mu.Unlock()
		return nil
	})
	run("surface", func() error {
		v, err := a.loadSurface(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		resp.SurfaceItems = v
		mu.Unlock()
		return nil
	})

	wg.Wait()
	// Ground-truth timing for the "dashboard spins 5-10s" report. The loaders
	// fan out concurrently, so this is the real server-side cost of the whole
	// payload. Exposed as a Server-Timing header (visible in the browser's
	// Network tab) AND logged so Railway has it - if this is consistently <1s,
	// the latency the boss feels is client-side (auth/getSession or network),
	// not the backend.
	dur := time.Since(start)
	w.Header().Set("Server-Timing", fmt.Sprintf("dashboard;dur=%d", dur.Milliseconds()))
	if dur > 1500*time.Millisecond {
		a.Logger.Warn("dashboard: slow assembly", "ms", dur.Milliseconds())
	} else {
		a.Logger.Info("dashboard: assembled", "ms", dur.Milliseconds())
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
		       current_value, target_value, unit, due_at, status, created_at
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
			&p.CurrentValue, &p.TargetValue, &p.Unit, &p.DueAt, &status, &p.CreatedAt,
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
	// One-shot read of the connector identity + alias maps so we can
	// label each event with its owning account's email/alias instead
	// of the opaque ca_xxx id. Both are stored as JSON blobs in
	// infinity_meta keyed by accountId. Either query failing leaves
	// the map empty; downstream just emits AccountLabel = "" which the
	// dashboard tolerates.
	idents := loadAccountLabels(ctx, a.Pool)

	// Forward-looking window: 6 months from today, plus events still
	// active right now (ends_at >= now). Past events are excluded -
	// the dashboard surface is "what's coming," not history.
	rows, err := a.Pool.Query(ctx, `
		SELECT id, title, starts_at, ends_at, COALESCE(all_day, FALSE), location,
		       COALESCE(description, ''),
		       COALESCE(attendees, '[]'::jsonb), classification,
		       COALESCE(prep, '[]'::jsonb),
		       COALESCE(organizer, '{}'::jsonb),
		       COALESCE(conference, '{}'::jsonb),
		       COALESCE(recurrence, '[]'::jsonb),
		       COALESCE(html_link, ''), COALESCE(hangout_link, ''),
		       COALESCE(response_status, ''),
		       COALESCE(account_id, '')
		FROM mem_calendar_events
		WHERE (ends_at IS NULL AND starts_at >= NOW() - INTERVAL '1 day')
		   OR (COALESCE(all_day, FALSE) AND ends_at >= NOW() - INTERVAL '1 day')
		   OR (NOT COALESCE(all_day, FALSE) AND ends_at >= NOW())
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
		var (
			attendeesRaw, prepRaw, organizerRaw,
			conferenceRaw, recurrenceRaw []byte
		)
		if err := rows.Scan(
			&e.ID, &e.Title, &e.StartsAt, &e.EndsAt, &e.AllDay, &e.Location,
			&e.Description,
			&attendeesRaw, &e.Classification, &prepRaw,
			&organizerRaw, &conferenceRaw, &recurrenceRaw,
			&e.HTMLLink, &e.HangoutLink, &e.ResponseStatus, &e.AccountID,
		); err != nil {
			return nil, err
		}
		e.Attendees = decodeAttendees(attendeesRaw)
		_ = json.Unmarshal(prepRaw, &e.Prep)
		_ = json.Unmarshal(organizerRaw, &e.Organizer)
		_ = json.Unmarshal(conferenceRaw, &e.Conference)
		_ = json.Unmarshal(recurrenceRaw, &e.Recurrence)
		if e.Prep == nil {
			e.Prep = []PrepItem{}
		}
		if e.AccountID != "" {
			e.AccountLabel = idents[e.AccountID]
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// loadAccountLabels reads connectors_aliases + connectors_identities
// from infinity_meta and returns one merged map of accountID -> label.
// Alias wins (boss's chosen human name), identity (oauth email) is the
// fallback. Empty map on any error - downstream renders accountLabel=""
// which the dashboard handles gracefully.
func loadAccountLabels(ctx context.Context, pool *pgxpool.Pool) map[string]string {
	out := map[string]string{}
	if pool == nil {
		return out
	}
	for _, key := range []string{"connectors_identities", "connectors_aliases"} {
		var raw string
		if err := pool.QueryRow(ctx, `SELECT value FROM infinity_meta WHERE key = $1`, key).Scan(&raw); err != nil {
			continue
		}
		if raw == "" {
			continue
		}
		m := map[string]string{}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			continue
		}
		for k, v := range m {
			if v != "" {
				out[k] = v // aliases load second and overwrite identities, by design
			}
		}
	}
	return out
}

// decodeAttendees handles both shapes the column may carry:
//
//   - legacy: ["alice@x", "bob@y"] (string array - pre-058 polls)
//   - native: [{email, displayName, responseStatus, …}] (post-058 sync)
//
// String entries become EventAttendee{Email: s}. The native sync
// always writes objects so over time the legacy shape fades out.
func decodeAttendees(raw []byte) []EventAttendee {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	// Try the rich shape first.
	var objs []EventAttendee
	if err := json.Unmarshal(raw, &objs); err == nil {
		return objs
	}
	// Fall back to string list.
	var strs []string
	if err := json.Unmarshal(raw, &strs); err == nil {
		out := make([]EventAttendee, 0, len(strs))
		for _, s := range strs {
			if s == "" {
				continue
			}
			out = append(out, EventAttendee{Email: s})
		}
		return out
	}
	return nil
}

// followupSurfaceKeys are the surface names that route an agent-surfaced
// item into the Follow-ups card instead of spawning its own generic
// SurfaceCard. The dashboard treats these as semantically equivalent
// ("the boss has a message waiting on them"), and loadSurface excludes
// the same set from the generic-card rendering path so we never
// double-render.
var followupSurfaceKeys = []string{"followups", "inbox", "email"}

func isFollowupSurface(key string) bool {
	for _, k := range followupSurfaceKeys {
		if k == key {
			return true
		}
	}
	return false
}

func (a *API) loadFollowUps(ctx context.Context) ([]FollowUp, error) {
	// Resolve connector account IDs (ca_xxx) to friendly labels (alias > oauth
	// email) so the Follow-ups chip reads "mr khaya" / "dopesoft" the same way
	// the calendar does, instead of leaking the opaque connected_account_id.
	labels := loadAccountLabels(ctx, a.Pool)

	// (1) Connector-fed rows from mem_followups (Gmail poller, etc).
	rows, err := a.Pool.Query(ctx, `
		SELECT id, source, account, from_name, subject, preview, body, thread_url,
		       draft, unread, received_at, COALESCE(metadata, '{}'::jsonb)
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
		var (
			f       FollowUp
			metaRaw []byte
		)
		if err := rows.Scan(
			&f.ID, &f.Source, &f.Account, &f.From, &f.Subject,
			&f.Preview, &f.Body, &f.ThreadURL, &f.Draft, &f.Unread, &f.ReceivedAt,
			&metaRaw,
		); err != nil {
			return nil, err
		}
		f.Origin = "followup"
		if len(metaRaw) > 0 {
			_ = json.Unmarshal(metaRaw, &f.Metadata)
		}
		f.SentReply = strFromMeta(f.Metadata, "sent_reply")
		f.Account = displayAccount(f.Account, labels)
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	// (2) Surfaced items the agent OR a connector dropped into
	// surface='followups' (or aliases inbox/email). Two hard rules the boss
	// set, enforced HERE so it can't regress no matter how a row is filed:
	//   • EMAILS ONLY. `kind = 'email'` is required. A non-email row in this
	//     surface (a status note, an alert, a 'finding') never renders in
	//     Follow-ups. The boss does not want non-emails in this card, period.
	//   • Author-agnostic. Source doesn't matter - Jarvis's own triage output
	//     (source='agent') is as much an email-waiting as a connector poll, so
	//     no source filter. loadSurface excludes this surface set from the
	//     generic-card path and loadActivity no longer ingests it, so there's
	//     no double-render and nothing spills into Activity.
	srows, err := a.Pool.Query(ctx, followupSurfaceSQL, followupSurfaceKeys)

	if err != nil {
		// Best-effort: connector rows alone are still useful.
		a.Logger.Warn("dashboard: followups surface merge", "err", err)
		sortFollowUpsNewest(out)
		return out, nil
	}
	defer srows.Close()
	for srows.Next() {
		var (
			id, surface, kind, source, extID string
			title, subtitle, body, url       string
			metaRaw, actionsRaw              []byte
			createdAt                        time.Time
		)
		if err := srows.Scan(&id, &surface, &kind, &source, &extID,
			&title, &subtitle, &body, &url, &metaRaw, &actionsRaw, &createdAt); err != nil {
			return out, err
		}
		var meta map[string]any
		if len(metaRaw) > 0 {
			_ = json.Unmarshal(metaRaw, &meta)
		}
		if meta == nil {
			meta = map[string]any{}
		}
		// Pull display fields out of metadata when the producer set them;
		// otherwise derive from title/source so a row always renders.
		from := strFromMeta(meta, "from", "from_name", "sender")
		if from == "" {
			from = title
		}
		account := displayAccount(strFromMeta(meta, "account", "mailbox"), labels)
		f := FollowUp{
			ID:      id,
			Source:  sourceFromMeta(source, kind, meta),
			Account: account,
			From:    from,
			Subject: firstNonEmpty(strFromMeta(meta, "subject"), title),
			Preview: firstNonEmpty(subtitle, strFromMeta(meta, "preview")),
			// The agent-authored body IS the triage summary - it goes in the
			// "Context" pane. The full email is fetched lazily on open via
			// /api/followups/message (keyed by this row's external_id), so
			// Body stays empty here and the dashboard payload stays lean.
			Summary:    body,
			ThreadURL:  firstNonEmpty(url, strFromMeta(meta, "thread_url", "url")),
			Draft:      strFromMeta(meta, "draft"),
			SentReply:  strFromMeta(meta, "sent_reply"),
			Unread:     true,
			ReceivedAt: createdAt,
			Origin:     "surface",
			Metadata:   meta,
		}
		// Surface the item's tappable actions (Draft reply / Archive / Snooze,
		// etc.) so the boss can act on an email in one tap. Intent stays
		// server-side; only id/label/style reach the client.
		if len(actionsRaw) > 0 {
			var acts []FollowUpAction
			if err := json.Unmarshal(actionsRaw, &acts); err == nil {
				f.Actions = acts
			}
		}
		out = append(out, f)
	}
	if err := srows.Err(); err != nil {
		return out, err
	}
	sortFollowUpsNewest(out)
	return out, nil
}

const followupSurfaceSQL = `
		SELECT id::text, surface, kind, source, COALESCE(external_id,''),
		       title, subtitle, body, COALESCE(url,''),
		       COALESCE(metadata, '{}'::jsonb), COALESCE(actions, '[]'::jsonb), created_at
		FROM mem_surface_items
		WHERE surface = ANY($1::text[])
		  AND kind = 'email'
		  AND (status = 'open' OR (status = 'snoozed' AND snoozed_until < NOW()))
		-- Follow-ups are an inbox, not a leaderboard. If we rank before the
		-- LIMIT, old high-importance rows can hide brand-new triage results
		-- (the 2026-06-26 inbox run wrote 10 open emails, but only two reached
		-- the card because stale Railway alerts occupied the top-importance 50).
		ORDER BY created_at DESC
		LIMIT 50
	`

func sortFollowUpsNewest(items []FollowUp) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].ReceivedAt.After(items[j].ReceivedAt)
	})
}

// displayAccount turns a raw account hint (connected_account_id like
// "ca_xxx", or a bare email) into the friendly label the boss configured —
// alias first, oauth email second, falling back to the original value when
// it's already human-readable (e.g. an email). For unresolved ca_xxx ids
// we drop the chip entirely rather than leak the opaque id into the UI;
// this mirrors what the Calendar surface does via accountLabel.
func displayAccount(raw string, labels map[string]string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if v, ok := labels[raw]; ok && v != "" {
		return v
	}
	// Already an email or other human-readable handle - keep it.
	if strings.Contains(raw, "@") {
		return raw
	}
	// Opaque connected_account_id with no label - hide it.
	if strings.HasPrefix(raw, "ca_") {
		return ""
	}
	return raw
}

// strFromMeta returns the first string-valued key from a metadata map.
// Robust to JSON unmarshalling quirks (json.Number, nested null).
func strFromMeta(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// sourceFromMeta picks the best "source" label for the FollowUp shape.
// Prefer explicit metadata.source ("gmail"), else derive from the
// surface-item.source ("gmail-triage" → "gmail"), else fall back to
// the agent-supplied kind ("email" → "email"), else "other".
func sourceFromMeta(source, kind string, meta map[string]any) string {
	if s := strFromMeta(meta, "source"); s != "" {
		return s
	}
	if source != "" {
		// gmail-triage / slack-poll → gmail / slack
		if i := indexAny(source, "-_"); i > 0 {
			return source[:i]
		}
		return source
	}
	if kind == "email" {
		return "gmail"
	}
	if kind != "" {
		return kind
	}
	return "other"
}

func indexAny(s, chars string) int {
	for i, r := range s {
		for _, c := range chars {
			if r == c {
				return i
			}
		}
	}
	return -1
}

// loadSurface reads the generic surface contract (mem_surface_items) and
// groups visible items by their `surface` key. Studio renders each group
// with one generic SurfaceCard - a new surface the agent invents appears
// on the dashboard with zero backend changes.
func (a *API) loadSurface(ctx context.Context) (map[string][]SurfaceItem, error) {
	// Two surface classes never reach the unified "Surfaced by Jarvis"
	// card:
	//   1. follow-up-aliased surfaces (followups/inbox/email) - folded
	//      into the Follow-ups card by loadFollowUps.
	//   2. operational agent notes - the agent's own status observations
	//      (source='agent' in a follow-up surface) plus any literal
	//      surface='system'. These are log entries, not action items, so
	//      loadActivity folds them into the Activity stream instead. We
	//      simply exclude them here.
	// Everything else (alerts, insights, digest, briefing, health,
	// agenda, …) flows through unchanged - a new surface key the agent
	// invents still appears on the dashboard with zero backend changes.
	rows, err := a.Pool.Query(ctx, `
		SELECT id::text, surface,
		       kind, source, COALESCE(external_id,''),
		       title, subtitle, body, COALESCE(url,''), importance,
		       importance_reason, metadata, status, created_at,
		       updated_at, COALESCE(actions, '[]'::jsonb)
		FROM mem_surface_items
		WHERE (status = 'open' OR (status = 'snoozed' AND snoozed_until < NOW()))
		  AND surface <> ALL($1::text[])
		  AND surface <> 'system'
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY surface, importance DESC NULLS LAST, created_at DESC
		LIMIT 200
	`, followupSurfaceKeys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]SurfaceItem{}
	for rows.Next() {
		var (
			it         SurfaceItem
			imp        *int16
			metaRaw    []byte
			actionsRaw []byte
		)
		if err := rows.Scan(
			&it.ID, &it.Surface, &it.Kind, &it.Source, &it.ExternalID,
			&it.Title, &it.Subtitle, &it.Body, &it.URL, &imp,
			&it.ImportanceReason, &metaRaw, &it.Status, &it.CreatedAt,
			&it.UpdatedAt, &actionsRaw,
		); err != nil {
			return nil, err
		}
		if imp != nil {
			v := int(*imp)
			it.Importance = &v
		}
		if len(metaRaw) > 0 {
			_ = json.Unmarshal(metaRaw, &it.Metadata)
		}
		if it.Metadata == nil {
			it.Metadata = map[string]any{}
		}
		if len(actionsRaw) > 0 {
			var acts []FollowUpAction
			if err := json.Unmarshal(actionsRaw, &acts); err == nil {
				it.Actions = acts
			}
		}
		out[it.Surface] = append(out[it.Surface], it)
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

// loadArtifacts reads a curated view of mem_artifacts for the Saved card's
// "Made by Jarvis" segment. Curated, not the firehose: substantive deliverables
// (projects/apps, documents, datasets, other) only — raw generated media
// (image/audio/video) and internal 'memory' artifacts stay in the canvas
// Library so the dashboard glance reads as "things Jarvis shipped." Read-only:
// generators still own the writes via artifact_save.
func (a *API) loadArtifacts(ctx context.Context) ([]Artifact, error) {
	rows, err := a.Pool.Query(ctx, `
		SELECT id::text, kind, name, COALESCE(description,''), virtual_path, storage_kind,
		       COALESCE(storage_path,''), COALESCE(storage_mime,''), COALESCE(bridge,''),
		       COALESCE(github_url,''), COALESCE(source_tool,''), created_at,
		       COALESCE(metadata->>'format',''), COALESCE(storage_size,0),
		       COALESCE(metadata->>'pdf_path',''), COALESCE(metadata->>'html_path',''),
		       COALESCE(metadata->>'markdown',''),
		       COALESCE(source_session_id::text,''),
		       EXISTS (SELECT 1 FROM mem_sessions s
		                WHERE s.id = mem_artifacts.source_session_id AND s.deleted_at IS NULL) AS session_alive
		FROM mem_artifacts
		WHERE deleted_at IS NULL
		  AND kind IN ('project', 'document', 'dataset', 'other')
		ORDER BY created_at DESC
		LIMIT 24
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.Kind, &a.Name, &a.Description, &a.VirtualPath, &a.StorageKind,
			&a.StoragePath, &a.StorageMime, &a.Bridge, &a.GithubURL, &a.SourceTool, &a.CreatedAt,
			&a.Format, &a.Bytes, &a.PDFPath, &a.HTMLPath, &a.Markdown,
			&a.SourceSessionID, &a.SessionAlive); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// loadMemoryStats reads the same numbers shown in the dashboard's
// footer strip - daily memory growth + procedural count + a streak
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
		// "no rows" is the normal empty-DB case - not an error.
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

// loadApprovals unions three "needs you" surfaces - trust contracts,
// code proposals, curiosity questions - into the same Approval shape
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
				// Some callers store the full action minus the routing tag -
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
	//
	// Only proposals that are a genuine BOSS DECISION reach the inbox: a
	// concrete change to a real file from the source extractor's file-fight
	// evidence. Reflection-fix candidates are the agent's own self-critique
	// from a failed session (origin='reflection_fix', quality<0.5) — an
	// internal self-improve backlog item, not a decision the boss can make on a
	// vague target with no diff. Surfacing them here dumped low-quality "code
	// issue" cards into the inbox at high weight, which is the pile-up the boss
	// flagged. They remain in mem_code_proposals and the /code-proposals tab;
	// they just no longer ambush the main inbox.
	codeRows, err := a.Pool.Query(ctx, `
		SELECT id::text, title, target_path, rationale, proposed_change,
		       risk_level, created_at
		FROM mem_code_proposals
		WHERE status = 'candidate'
		  AND COALESCE(evidence->>'origin', '') <> 'reflection_fix'
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

	// status='open' filter: post-migration 034 the heartbeat marks
	// findings 'resolved' when their underlying condition clears or
	// when a fresh finding under the same source_tag supersedes them,
	// and 'dismissed' when the boss explicitly closes them via the
	// heartbeat-findings dismiss endpoint. The dashboard activity feed
	// only ever wants the live ones; full history still lives at
	// /heartbeat for archaeology.
	hbRows, err := a.Pool.Query(ctx, `
		SELECT id::text, kind, title, detail, created_at
		FROM (
			SELECT DISTINCT ON (kind, title)
			       id::text, kind, title, detail, created_at
			  FROM mem_heartbeat_findings
			 WHERE status = 'open'
			 ORDER BY kind, title, created_at DESC
		) recent_unique
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
		SELECT id::text, critique, lessons, created_at
		FROM mem_reflections
		ORDER BY created_at DESC
		LIMIT 10
	`)
	if err == nil {
		for refRows.Next() {
			var (
				id, critique string
				lessonsRaw   []byte
				createdAt    time.Time
			)
			if err := refRows.Scan(&id, &critique, &lessonsRaw, &createdAt); err != nil {
				refRows.Close()
				return nil, err
			}
			title, _ := splitTitleBody(critique)
			// Body = the FULL critique (prose) + lessons as a labeled block, so
			// the expanded card is rich and uniform with every other kind (which
			// all set Detail). Previously the body was discarded → empty card.
			detail := strings.TrimSpace(critique)
			if ls := joinLessons(lessonsRaw); ls != "" {
				detail += "\n\nlessons: " + ls
			}
			out = append(out, ActivityEvent{
				ID:     "ref-" + id,
				Kind:   "reflection",
				Title:  "Reflection: " + title,
				Detail: detail,
				At:     createdAt,
			})
		}
		refRows.Close()
	}

	// Genuine operational agent notes - ONLY a literal surface='system'.
	// These are status observations ("inbox triage blocker on primary
	// Gmail"), not messages, so they belong in the log. Email-shaped items
	// (surface=followups/inbox/email) are deliberately NOT pulled here -
	// they live in the Follow-ups card, period. They keep a dismiss handle
	// so the boss can clear a stale one from its detail view.
	sysRows, err := a.Pool.Query(ctx, `
		SELECT id::text, title, COALESCE(NULLIF(subtitle,''), importance_reason, ''), created_at
		FROM mem_surface_items
		WHERE (status = 'open' OR (status = 'snoozed' AND snoozed_until < NOW()))
		  AND surface = 'system'
		ORDER BY created_at DESC
		LIMIT 20
	`)
	if err == nil {
		for sysRows.Next() {
			var (
				id, title, detail string
				createdAt         time.Time
			)
			if err := sysRows.Scan(&id, &title, &detail, &createdAt); err != nil {
				sysRows.Close()
				return nil, err
			}
			out = append(out, ActivityEvent{
				ID:      "sys-" + id,
				Kind:    "system",
				Title:   title,
				Detail:  detail,
				At:      createdAt,
				Dismiss: &ActivityDismiss{Origin: "surface", ID: id},
			})
		}
		sysRows.Close()
	}

	// Uniform titles: every activity card gets a clean one-liner header — no
	// runaway sentences (a reflection's first sentence can run long). The full
	// content always lives in Detail (the body), never the title.
	for i := range out {
		out[i].Title = capTitle(out[i].Title, 80)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].At.After(out[j].At)
	})
	if len(out) > 40 {
		out = out[:40]
	}
	return out, nil
}

// capTitle collapses newlines and trims a title to a clean single line of at
// most max runes (rune-safe so it never splits a multibyte char).
func capTitle(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max-1])) + "…"
}

// joinLessons flattens a reflection's lessons jsonb (array of strings or
// {lesson|text} objects) into a " · "-separated string for the activity body.
func joinLessons(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var arr []any
	if err := json.Unmarshal(raw, &arr); err != nil {
		return ""
	}
	parts := make([]string, 0, len(arr))
	for _, v := range arr {
		switch x := v.(type) {
		case string:
			if s := strings.TrimSpace(x); s != "" {
				parts = append(parts, s)
			}
		case map[string]any:
			for _, k := range []string{"lesson", "text", "summary"} {
				if s, _ := x[k].(string); strings.TrimSpace(s) != "" {
					parts = append(parts, strings.TrimSpace(s))
					break
				}
			}
		}
	}
	return strings.Join(parts, " · ")
}

// loadWork unions live cron/sentinel/skill-run/trust/code-proposal rows
// into the Kanban shape. One small query per source - no joins - keeps
// the payload predictable and each column independently fail-safe.
//
// Column policy:
//   - queued    → enabled crons with next_run_at > now, plus voyager
//     skill proposals still awaiting verifier decision.
//   - running   → enabled sentinels (always watching), plus any mem_runs row
//     still in-flight (cron agent turns, skill invokes, heartbeat, voyager).
//   - awaiting  → pending trust contracts + candidate code proposals.
//     These also appear in Approvals - that's intentional;
//     the Kanban is a *status board*, the Approvals card is
//     the decision surface.
//   - done      → today's completed cron runs + completed skill runs.
//
// runWorkKind maps a mem_runs.kind to the Kanban WorkItem kind, its detail
// link, and a human engine label. Keeps the generic "running" query (which
// reads mem_runs of any kind) rendering correct icons + deep links per source.
func runWorkKind(kind string) (workKind, href, engine string) {
	switch kind {
	case "cron":
		return "cron_run", "/cron", "Schedule"
	case "skill":
		return "skill_run", "/skills", "Skill"
	case "sentinel":
		return "sentinel", "/cron", "Sentinel"
	case "voyager":
		return "voyager_opt", "/skills", "Voyager"
	case "heartbeat":
		return "memory_op", "/heartbeat", "Heartbeat"
	// An errand the boss gave by phone is an AGENT TASK like any other: the full
	// loop, his tools, his memory. It belongs on the board next to the crons and
	// the skills, not hidden inside the Phone card.
	case "phone.ask":
		return "phone_errand", "/", "Phone"
	case "phone.call":
		return "phone_call", "/", "Phone"
	default:
		return "skill_run", "/logs", "Agent"
	}
}

func (a *API) loadWork(ctx context.Context) ([]WorkItem, error) {
	if a.Pool == nil {
		return nil, errors.New("no pool")
	}
	const perCol = 10
	out := make([]WorkItem, 0, perCol*4)

	// Sessions that produced a plan: their cron/skill/agent run cards fold into
	// the canonical plan card so one job is one card, not two (see
	// planSessionIDs). Best-effort — a query error just means no collapsing.
	planSessions, _ := a.planSessionIDs(ctx)

	// ── queued: upcoming crons ────────────────────────────────────────
	cronQRows, err := a.Pool.Query(ctx, `
		SELECT id::text, name, schedule_natural, schedule, next_run_at, COALESCE(target, '')
		FROM mem_crons
		WHERE enabled = TRUE AND next_run_at IS NOT NULL AND next_run_at > NOW()
		ORDER BY next_run_at ASC
		LIMIT $1
	`, perCol)
	if err == nil {
		for cronQRows.Next() {
			var (
				id, name          string
				natural, schedule *string
				nextRunAt         time.Time
				target            string
			)
			if err := cronQRows.Scan(&id, &name, &natural, &schedule, &nextRunAt, &target); err != nil {
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
				Title:        humanizeName(name),
				Subtitle:     sub,
				Engine:       "Schedule",
				Ref:          name,
				Column:       "queued",
				ScheduledFor: &nra,
				DetailHref:   "/cron",
				// Self-explaining queued card: what the job actually does (its
				// instruction) and which skill(s) it's set to invoke — so the boss
				// reads it here, not in /cron.
				Instruction: strings.TrimSpace(target),
				Skills:      cronInvokedSkills(target),
			})
		}
		cronQRows.Close()
	}

	// Voyager skill proposals waiting for verifier / decision. We pull the
	// candidate's own description + reasoning so the card actually explains what
	// it IS ("Skill-worthy session detected — used 5 tools across 81s") instead
	// of the meaningless "verify session_pattern_1780423167 / verifier queued".
	propRows, err := a.Pool.Query(ctx, `
		SELECT id::text, name, COALESCE(parent_skill, ''),
		       COALESCE(description, ''), COALESCE(reasoning, '')
		FROM mem_skill_proposals
		WHERE status = 'candidate'
		ORDER BY created_at DESC
		LIMIT $1
	`, perCol)
	if err == nil {
		for propRows.Next() {
			var id, name, parent, description, reasoning string
			if err := propRows.Scan(&id, &name, &parent, &description, &reasoning); err != nil {
				propRows.Close()
				return nil, err
			}
			// Title = the human-authored description when there is one (that's
			// what the candidate is); fall back to a plain label. The engine
			// (Voyager vs GEPA) and the raw auto-generated name live in
			// Engine/Ref for the detail, never the title.
			engine := "Voyager"
			title := "New skill to review"
			if parent != "" {
				title = "Improve skill: " + humanizeName(parent)
				engine = "GEPA"
			}
			if description != "" {
				title = description
			}
			out = append(out, WorkItem{
				ID:         "vop-" + id,
				Kind:       "voyager_opt",
				Title:      title,
				Subtitle:   "awaiting review",
				Summary:    reasoning, // the specifics: which session, tools, failures
				Engine:     engine,
				Ref:        name,
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
				Title:      humanizeName(name),
				Subtitle:   "watching · cooldown " + humanSeconds(cooldown),
				Engine:     "Sentinel",
				Ref:        watch,
				Column:     "running",
				DetailHref: "/cron",
			})
		}
		sentRows.Close()
	}

	// In-flight work of EVERY kind — cron agent turns, skill invokes, heartbeat
	// scans, voyager runs. mem_runs is the generic run tracker (a row sits at
	// status='running' from Begin until Finish), so it catches everything, not
	// just skills. The old query read mem_skill_runs ONLY, which missed cron /
	// agent runs entirely (they book mem_runs, never mem_skill_runs) — that's
	// why the Running column always looked empty even while a cron was firing.
	// 1h cap so a row orphaned by a crash (swept to 'error' at boot anyway)
	// can't linger.
	runningRows, err := a.Pool.Query(ctx, `
		SELECT id::text, kind, COALESCE(NULLIF(label,''), kind) AS label, source, started_at,
		       COALESCE(meta->>'session_id', '') AS session_id
		FROM mem_runs
		WHERE status = 'running' AND started_at >= NOW() - INTERVAL '1 hour'
		ORDER BY started_at DESC
		LIMIT $1
	`, perCol)
	if err == nil {
		for runningRows.Next() {
			var id, kind, label, source, sessionID string
			var startedAt time.Time
			if err := runningRows.Scan(&id, &kind, &label, &source, &startedAt, &sessionID); err != nil {
				runningRows.Close()
				return nil, err
			}
			// plan.step rows are the per-step spinners that ride INSIDE a plan
			// card's timeline — never a standalone job. And any run whose session
			// produced a plan folds into that plan card. Either way, skip it here.
			if kind == "plan.step" || planSessions[sessionID] {
				continue
			}
			wkind, href, engine := runWorkKind(kind)
			s := startedAt
			out = append(out, WorkItem{
				ID:         "run-" + id,
				Kind:       wkind,
				Title:      humanizeName(label),
				Subtitle:   "running · via " + source,
				Engine:     engine,
				Ref:        label,
				Column:     "running",
				SessionID:  sessionID,
				StartedAt:  &s,
				DetailHref: href,
			})
		}
		runningRows.Close()
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
				Engine:     "Approval",
				Ref:        tool,
				Column:     "awaiting",
				StartedAt:  &c,
				DetailHref: "/trust",
			})
		}
		trustRows.Close()
	}

	// Reflection-fix candidates are internal self-improve backlog, not boss-
	// facing work "awaiting" a decision — same reasoning as loadApprovals. They
	// stay in the /code-proposals tab; they don't clutter the work board.
	codeRows, err := a.Pool.Query(ctx, `
		SELECT id::text, title, target_path, rationale, proposed_change, risk_level, created_at
		FROM mem_code_proposals
		WHERE status = 'candidate'
		  AND COALESCE(evidence->>'origin', '') <> 'reflection_fix'
		ORDER BY created_at DESC
		LIMIT $1
	`, perCol)
	if err == nil {
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
			sub := risk
			if path != "" {
				sub = path + " · " + risk
			}
			c := created
			out = append(out, WorkItem{
				ID:    "code-" + id,
				Kind:  "code_proposal",
				Title: title,
				// Subtitle stays the short "file · risk" status; Summary carries
				// the actual context (what Jarvis noticed + what it proposes + the
				// ask) so the card isn't a contextless title. Without this the
				// dashboard read only the title and dropped the whole draft.
				Subtitle:   sub,
				Summary:    codeProposalReport(rationale, change),
				Engine:     "Code",
				Ref:        path,
				Column:     "awaiting",
				StartedAt:  &c,
				DetailHref: "/code-proposals",
			})
		}
		codeRows.Close()
	}

	// ── done: today's completed cron + skill runs ────────────────────
	// LEFT JOIN LATERAL the cron's most recent finished mem_runs row to pull the
	// narrative the executor wrote (result_summary). That's what turns the Done
	// card from "ok" into "Reflected 3 sessions, compressed 41 observations…" /
	// the agent's own closing words.
	cronDoneRows, err := a.Pool.Query(ctx, `
		SELECT c.id::text, c.name, c.last_run_at, c.last_run_status, c.last_run_duration_ms,
		       COALESCE(r.result_summary, ''), COALESCE(r.session_id, ''), COALESCE(c.target, '')
		FROM mem_crons c
		LEFT JOIN LATERAL (
			SELECT result_summary, meta->>'session_id' AS session_id
			FROM mem_runs
			WHERE kind = 'cron' AND target_id = c.id::text AND ended_at IS NOT NULL
			ORDER BY started_at DESC
			LIMIT 1
		) r ON TRUE
		WHERE c.last_run_at IS NOT NULL
		  AND c.last_run_at >= date_trunc('day', NOW())
		ORDER BY c.last_run_at DESC
		LIMIT $1
	`, perCol)
	if err == nil {
		for cronDoneRows.Next() {
			var (
				id, name   string
				lastRunAt  time.Time
				status     *string
				durationMs *int
				summary    string
				sessionID  string
				target     string
			)
			if err := cronDoneRows.Scan(&id, &name, &lastRunAt, &status, &durationMs, &summary, &sessionID, &target); err != nil {
				cronDoneRows.Close()
				return nil, err
			}
			// If this cron's run built a plan, the plan card already represents
			// the job (with its steps + real outcome) — don't draw a second card.
			if planSessions[sessionID] {
				continue
			}
			sub := "completed"
			if status != nil && *status != "" {
				sub = *status
			}
			fa := lastRunAt
			item := WorkItem{
				ID:          "cron-d-" + id,
				Kind:        "cron_run",
				Title:       humanizeName(name),
				Subtitle:    sub,
				Summary:     summary,
				Engine:      "Schedule",
				Ref:         name,
				Column:      "done",
				FinishedAt:  &fa,
				DetailHref:  "/cron",
				Instruction: strings.TrimSpace(target),
				Skills:      cronInvokedSkills(target),
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
		SELECT id::text, skill_name, ended_at, duration_ms, success, COALESCE(session_id, '')
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
				sessionID  string
			)
			if err := skillDoneRows.Scan(&id, &name, &endedAt, &durationMs, &success, &sessionID); err != nil {
				skillDoneRows.Close()
				return nil, err
			}
			// Same job, one card: if this skill ran inside a session that built a
			// plan, the plan card already represents it.
			if planSessions[sessionID] {
				continue
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
				Title:      humanizeName(name),
				Subtitle:   sub,
				Engine:     "Skill",
				Ref:        name,
				Column:     "done",
				FinishedAt: &fa,
				DurationMs: &d,
				DetailHref: "/skills",
			})
		}
		skillDoneRows.Close()
	}

	// ── done: errands the boss gave by phone ──────────────────────────
	// He rang, gave an instruction, hung up, and the full agent ran it. That is
	// an agent task and it belongs on the board: the card carries the REPORT as
	// its narrative (the same words the inbox card and the push carry), and its
	// session id, so one tap opens the work itself.
	phoneDoneRows, err := a.Pool.Query(ctx, `
		SELECT id::text, COALESCE(NULLIF(label,''), 'Errand from your call'), ended_at,
		       COALESCE(duration_ms, 0), status,
		       COALESCE(result_summary, ''), COALESCE(meta->>'session_id', '')
		FROM mem_runs
		WHERE kind = 'phone.ask'
		  AND ended_at IS NOT NULL
		  AND ended_at >= date_trunc('day', NOW())
		ORDER BY ended_at DESC
		LIMIT $1
	`, perCol)
	if err == nil {
		for phoneDoneRows.Next() {
			var (
				id, label, status string
				endedAt           time.Time
				durationMs        int
				summary           string
				sessionID         string
			)
			if err := phoneDoneRows.Scan(&id, &label, &endedAt, &durationMs, &status, &summary, &sessionID); err != nil {
				phoneDoneRows.Close()
				return nil, err
			}
			if planSessions[sessionID] {
				continue
			}
			sub := "done"
			if status == "error" {
				sub = "failed"
			}
			fa := endedAt
			d := durationMs
			out = append(out, WorkItem{
				ID:         "phone-d-" + id,
				Kind:       "phone_errand",
				Title:      label,
				Subtitle:   sub,
				Summary:    summary,
				Engine:     "Phone",
				Column:     "done",
				SessionID:  sessionID,
				FinishedAt: &fa,
				DurationMs: &d,
				DetailHref: "/",
			})
		}
		phoneDoneRows.Close()
	}

	// ── workflow runs - span columns by run status ────────────────────
	// pending→queued, running→running, paused→awaiting, terminal→done.
	// Each run carries its step list inline so tapping a Kanban card opens
	// the ObjectViewer drawer with the full step state-machine - the
	// Kanban IS the workflow view, no separate page.
	wfRows, err := a.Pool.Query(ctx, `
		SELECT id::text, workflow_name, status, current_step,
		       started_at, finished_at
		FROM mem_workflow_runs
		WHERE status IN ('pending', 'running', 'paused')
		   OR (status IN ('done', 'failed', 'cancelled')
		       AND COALESCE(finished_at, created_at) >= date_trunc('day', NOW()))
		ORDER BY created_at DESC
		LIMIT $1
	`, perCol*2)
	if err == nil {
		wfItems := make([]*WorkItem, 0, perCol*2)
		runIndex := map[string]*WorkItem{}
		for wfRows.Next() {
			var (
				id, name, status string
				currentStep      int
				startedAt        *time.Time
				finishedAt       *time.Time
			)
			if err := wfRows.Scan(&id, &name, &status, &currentStep, &startedAt, &finishedAt); err != nil {
				wfRows.Close()
				return nil, err
			}
			column := "queued"
			sub := "step " + strconv.Itoa(currentStep)
			switch status {
			case "running":
				column = "running"
				sub = "running · step " + strconv.Itoa(currentStep)
			case "paused":
				column = "awaiting"
				sub = "paused at checkpoint · step " + strconv.Itoa(currentStep)
			case "done":
				column = "done"
				sub = "completed"
			case "failed":
				column = "done"
				sub = "failed at step " + strconv.Itoa(currentStep)
			case "cancelled":
				column = "done"
				sub = "cancelled"
			}
			item := &WorkItem{
				ID:            "wf-" + id,
				Kind:          "workflow",
				Title:         humanizeName(name),
				Subtitle:      sub,
				Engine:        "Workflow",
				Ref:           name,
				Column:        column,
				StartedAt:     startedAt,
				FinishedAt:    finishedAt,
				WorkflowSteps: []WorkflowStep{},
			}
			wfItems = append(wfItems, item)
			runIndex[id] = item
		}
		wfRows.Close()

		// Batch-load every step for the surfaced runs in one query, then
		// attach to its run - no N+1, no separate endpoint.
		if len(runIndex) > 0 {
			runIDs := make([]string, 0, len(runIndex))
			for id := range runIndex {
				runIDs = append(runIDs, id)
			}
			stepRows, serr := a.Pool.Query(ctx, `
				SELECT run_id::text, step_index, name, kind, status, output, error
				FROM mem_workflow_steps
				WHERE run_id = ANY($1::uuid[])
				ORDER BY run_id, step_index
			`, runIDs)
			if serr == nil {
				for stepRows.Next() {
					var (
						runID, sName, sKind, sStatus, sOutput, sErr string
						stepIndex                                   int
					)
					if err := stepRows.Scan(&runID, &stepIndex, &sName, &sKind, &sStatus, &sOutput, &sErr); err != nil {
						stepRows.Close()
						return nil, err
					}
					if item, ok := runIndex[runID]; ok {
						// Trim long outputs - the drawer shows a preview.
						if len(sOutput) > 600 {
							sOutput = sOutput[:600] + "…"
						}
						item.WorkflowSteps = append(item.WorkflowSteps, WorkflowStep{
							Index:  stepIndex,
							Name:   sName,
							Kind:   sKind,
							Status: sStatus,
							Output: sOutput,
							Error:  sErr,
						})
					}
				}
				stepRows.Close()
			}
		}
		for _, item := range wfItems {
			out = append(out, *item)
		}
	}

	// ── plans ("the Cortex"): durable, verifiable plans the agent is working
	// through. A plan IS agent work, so it lives on this board (not a separate
	// page): active → Running with a step-progress bar, paused-at-checkpoint →
	// Awaiting you, finished-today → Done. Steps ride inline so the card opens
	// the full timeline in ObjectViewer, exactly like a workflow run.
	if planItems, perr := a.planWorkItems(ctx); perr == nil {
		out = append(out, planItems...)
	} else {
		a.Logger.Warn("dashboard: plan work items", "err", perr)
	}

	// Mandates — a definition of done is a unit of agent work; it rides the same
	// Kanban as plans (open→Running, verifying→Awaiting, done-today→Done), with
	// criteria inline. No separate dashboard card.
	if mandateItems, perr := a.mandateWorkItems(ctx); perr == nil {
		out = append(out, mandateItems...)
	} else {
		a.Logger.Warn("dashboard: mandate work items", "err", perr)
	}

	return out, nil
}

// humanizeName turns a machine identifier (kebab/snake case like
// "nightly-cognition" or "inbox_triage_hardened") into a readable card title
// ("Nightly cognition", "Inbox triage hardened"). A trailing all-digit token —
// the unix-stamp suffix on auto-generated voyager names like
// "session_pattern_1780423167" — is dropped because it reads like a science
// experiment and adds nothing on the card; the exact id is preserved on
// WorkItem.Ref for the detail view. Returns "" for empty input.
// codeProposalReport composes the human-readable narrative for a code-proposal
// work card: what Jarvis noticed, what it proposes, and the ask. Without it the
// card showed only a title + "path · risk" with zero context on what it is.
func codeProposalReport(rationale, change string) string {
	var b strings.Builder
	if r := strings.TrimSpace(rationale); r != "" {
		b.WriteString("What Jarvis noticed\n")
		b.WriteString(r)
	}
	if c := strings.TrimSpace(change); c != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Proposed change\n")
		b.WriteString(c)
	}
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString("This is a draft Jarvis flagged on its own — nothing changes until you approve it in Code Proposals.")
	return b.String()
}

// cronSkillRe captures the skill name out of a skills_invoke call embedded in a
// cron's instruction, e.g. skills_invoke({name:"inbox-triage"}). Tolerant of
// quote style and spacing so authored prompts parse regardless of formatting.
var cronSkillRe = regexp.MustCompile(`skills_invoke\s*\(\s*\{?\s*["']?name["']?\s*:\s*["']([^"']+)["']`)

// cronInvokedSkills returns the distinct skills a cron's instruction tells the
// agent to run, in first-seen order. This is the queued-card source of "what
// skill does this job invoke" — deterministic, no run required, so an upcoming
// cron explains itself before it has ever fired.
func cronInvokedSkills(target string) []string {
	if strings.TrimSpace(target) == "" {
		return nil
	}
	matches := cronSkillRe.FindAllStringSubmatch(target, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func humanizeName(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.NewReplacer("-", " ", "_", " ", ".", " ").Replace(s)
	fields := strings.Fields(s)
	// Drop a trailing pure-number token (timestamp/id noise).
	if n := len(fields); n > 1 && isAllDigits(fields[n-1]) {
		fields = fields[:n-1]
	}
	s = strings.Join(fields, " ")
	if s == "" {
		return ""
	}
	// Sentence case: capitalize the first letter, leave the rest as the author
	// wrote it (so "self improve" stays lowercase, acronyms keep their case).
	return strings.ToUpper(s[:1]) + s[1:]
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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

// splitTitleBody extracts a short title from a longer critique string -
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

// handleFollowupDismiss is the Studio-facing dismiss endpoint behind the
// "Dismiss" button on a follow-up. Body shape:
//
//	{ "id": "<row id>", "origin": "followup" | "surface" }
//
// origin discriminates the underlying table: "followup" → mem_followups
// (sets status='dismissed', dismissed_at=NOW()), "surface" → the generic
// surface store (mem_surface_items, status='dismissed' via the same
// shape the surface_update tool uses). Either way the row never comes
// back: the connector poller's ON CONFLICT DO NOTHING preserves the
// dismissed lifecycle on re-poll, and the surface store's upsert never
// overwrites status either.
func (a *API) handleFollowupDismiss(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.Pool == nil {
		writeErr(w, http.StatusServiceUnavailable, "no pool")
		return
	}
	var body struct {
		ID     string `json:"id"`
		Origin string `json:"origin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	body.ID = strings.TrimSpace(body.ID)
	body.Origin = strings.TrimSpace(strings.ToLower(body.Origin))
	if body.ID == "" {
		writeErr(w, http.StatusBadRequest, "id required")
		return
	}
	if body.Origin == "" {
		body.Origin = "followup"
	}
	ctx := r.Context()
	switch body.Origin {
	case "followup":
		if _, err := a.Pool.Exec(ctx, `
			UPDATE mem_followups
			   SET status       = 'dismissed',
			       unread       = false,
			       decided_at   = NOW(),
			       dismissed_at = NOW()
			 WHERE id::text = $1
		`, body.ID); err != nil {
			a.Logger.Warn("followup dismiss: followup row", "id", body.ID, "err", err)
			writeErr(w, http.StatusInternalServerError, "dismiss failed")
			return
		}
	case "surface":
		if _, err := a.Pool.Exec(ctx, `
			UPDATE mem_surface_items
			   SET status     = 'dismissed',
			       updated_at = NOW()
			 WHERE id::text = $1
		`, body.ID); err != nil {
			a.Logger.Warn("followup dismiss: surface row", "id", body.ID, "err", err)
			writeErr(w, http.StatusInternalServerError, "dismiss failed")
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "unknown origin")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// parseDueAt accepts an RFC3339 timestamp or a bare YYYY-MM-DD date (the
// shape an <input type="date"> produces). Empty string is a deliberate
// "no date" value; malformed non-empty strings fail loudly so a bad UI/tool
// payload never silently preserves or drops the wrong deadline.
func parseDueAt(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		d := time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, time.UTC)
		return &d, nil
	}
	return nil, fmt.Errorf("bad due_at: use RFC3339 or YYYY-MM-DD")
}

// normalizePriority clamps the priority field to the mem_tasks enum
// (low|med|high), defaulting to "med" for anything else.
func normalizePriority(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "low":
		return "low"
	case "high":
		return "high"
	default:
		return "med"
	}
}

// handleTaskCreate is the Studio-facing "Add todo" endpoint. Body shape:
//
//	{ "title": "...", "body": "...", "priority": "low|med|high", "due_at": "ISO|YYYY-MM-DD" }
//
// The row lands in mem_tasks with source='manual' so the dashboard's
// "agent" badge stays truthful (only Jarvis-filed rows are badged) while
// the agent still sees it via task_list. Returns {ok, id}.
func (a *API) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.Pool == nil {
		writeErr(w, http.StatusServiceUnavailable, "no pool")
		return
	}
	var body struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Priority string `json:"priority"`
		DueAt    string `json:"due_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	body.Title = strings.TrimSpace(body.Title)
	if body.Title == "" {
		writeErr(w, http.StatusBadRequest, "title required")
		return
	}
	dueAt, err := parseDueAt(body.DueAt)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id := uuid.New()
	if _, err := a.Pool.Exec(r.Context(), `
		INSERT INTO mem_tasks (id, title, body, source, priority, status, due_at, created_at, updated_at)
		VALUES ($1, $2, $3, 'manual', $4, 'open', $5, NOW(), NOW())
	`, id, body.Title, strings.TrimSpace(body.Body), normalizePriority(body.Priority), dueAt); err != nil {
		a.Logger.Warn("task create", "err", err)
		writeErr(w, http.StatusInternalServerError, "create failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id.String()})
}

// handleTaskUpdate is the Studio-facing todo mutation endpoint - it backs
// both the inline check-to-complete toggle and any future edit affordance.
// Body shape (all fields except id optional; only non-empty fields apply):
//
//	{ "id": "...", "title": "...", "body": "...", "priority": "...",
//	  "due_at": "ISO|YYYY-MM-DD", "status": "open|done|dropped" }
//
// Partial update via COALESCE so a status-only toggle leaves the other
// columns untouched. done_at is stamped the first time status flips to
// 'done' and cleared when it reopens. Returns {ok}.
func (a *API) handleTaskUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.Pool == nil {
		writeErr(w, http.StatusServiceUnavailable, "no pool")
		return
	}
	var body struct {
		ID       string  `json:"id"`
		Title    *string `json:"title"`
		Body     *string `json:"body"`
		Priority *string `json:"priority"`
		DueAt    *string `json:"due_at"`
		Status   *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	body.ID = strings.TrimSpace(body.ID)
	if body.ID == "" {
		writeErr(w, http.StatusBadRequest, "id required")
		return
	}

	var (
		title, bodyText, priority, status *string
		dueAt                             *time.Time
		setDueAt                          bool
	)
	if body.Title != nil && strings.TrimSpace(*body.Title) != "" {
		v := strings.TrimSpace(*body.Title)
		title = &v
	}
	if body.Body != nil {
		v := strings.TrimSpace(*body.Body)
		bodyText = &v
	}
	if body.Priority != nil && strings.TrimSpace(*body.Priority) != "" {
		v := normalizePriority(*body.Priority)
		priority = &v
	}
	if body.Status != nil {
		switch strings.ToLower(strings.TrimSpace(*body.Status)) {
		case "open", "done", "dropped":
			v := strings.ToLower(strings.TrimSpace(*body.Status))
			status = &v
		default:
			writeErr(w, http.StatusBadRequest, "bad status")
			return
		}
	}
	if body.DueAt != nil {
		var err error
		dueAt, err = parseDueAt(*body.DueAt)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		setDueAt = true
	}

	// done_at follows status: stamp on the first transition to done, clear
	// on reopen, leave alone when status isn't being changed.
	doneAtClause := "done_at"
	if status != nil {
		if *status == "done" {
			doneAtClause = "COALESCE(done_at, NOW())"
		} else {
			doneAtClause = "NULL"
		}
	}
	if _, err := a.Pool.Exec(r.Context(), `
		UPDATE mem_tasks
		   SET title    = COALESCE($2, title),
		       body     = COALESCE($3, body),
		       priority = COALESCE($4, priority),
		       status   = COALESCE($5, status),
		       due_at   = CASE WHEN $7 THEN $6 ELSE due_at END,
		       done_at  = `+doneAtClause+`,
		       updated_at = NOW()
		 WHERE id::text = $1
	`, body.ID, title, bodyText, priority, status, dueAt, setDueAt); err != nil {
		a.Logger.Warn("task update", "id", body.ID, "err", err)
		writeErr(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// resolveFollowupRef maps a follow-up (id, origin) to the (source,
// accountHint, messageID) the fetcher needs. Shared by the message + the
// attachment endpoints so the resolution stays in one place. Returns ok=false
// after writing the HTTP error.
// ResolveFollowupRefByID maps a follow-up (id, origin) to the (source,
// accountHint, messageID) the fetcher needs. Non-HTTP so both the message/
// attachment endpoints AND the seed/read_email paths share one resolver.
func (a *API) ResolveFollowupRefByID(ctx context.Context, id, origin string) (source, accountHint, messageID string, err error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", "", errors.New("id required")
	}
	origin = strings.TrimSpace(strings.ToLower(origin))
	if origin == "" {
		origin = "followup"
	}
	switch origin {
	case "followup":
		err = a.Pool.QueryRow(ctx, `
			SELECT source, COALESCE(account, ''),
			       COALESCE(source_ref->>'remote_id', '')
			  FROM mem_followups
			 WHERE id::text = $1
		`, id).Scan(&source, &accountHint, &messageID)
	case "surface":
		// Prefer the matching connector-poll row's account (a clean ca_...
		// connected_account_id) over the surface metadata hint, which may be
		// a bare email. With multiple Gmail accounts the ca_ id is the only
		// thing that resolves unambiguously upstream.
		err = a.Pool.QueryRow(ctx, `
			SELECT s.source,
			       COALESCE(NULLIF(mf.account, ''),
			                s.metadata->>'account',
			                s.metadata->>'connected_account_id', ''),
			       COALESCE(s.external_id, '')
			  FROM mem_surface_items s
			  LEFT JOIN mem_followups mf
			         ON COALESCE(s.external_id, '') <> ''
			        AND mf.source_ref->>'remote_id' = s.external_id
			 WHERE s.id::text = $1
		`, id).Scan(&source, &accountHint, &messageID)
	default:
		return "", "", "", errors.New("unknown origin")
	}
	return source, accountHint, messageID, err
}

// FullEmailText resolves a follow-up to its full email body as plain text -
// the text/plain part when present, else the HTML stripped to text. Capped so
// a giant marketing email can't blow the agent's context. Empty string (nil
// error) when there's no fetcher or no message to read. Shared by the Discuss
// seed enrichment and the read_email tool.
func (a *API) FullEmailText(ctx context.Context, id, origin string) (string, error) {
	if a == nil || a.Fetcher == nil {
		return "", nil
	}
	source, account, messageID, err := a.ResolveFollowupRefByID(ctx, id, origin)
	if err != nil {
		return "", err
	}
	if messageID == "" {
		return "", nil
	}
	html, text, _, err := a.Fetcher.FetchMessage(ctx, source, account, messageID)
	if err != nil {
		return "", err
	}
	body := strings.TrimSpace(text)
	if body == "" {
		body = html
	}
	// Composio's `text` field is often the raw HTML for HTML-only emails, so
	// strip whenever the body looks like markup (not just when text was empty).
	if looksHTML(body) {
		body = stripHTML(body)
	}
	body = strings.TrimSpace(body)
	const cap = 12000
	if len(body) > cap {
		body = body[:cap] + "\n…[truncated]"
	}
	return body, nil
}

// FullArtifactText resolves a generated document/artifact id to the best
// plain-text rendering we can put in front of the agent for a "Discuss with
// Jarvis" turn-1 hydration: the stored markdown for written docs, the
// HTML-preview path for spreadsheets (the caller flattens it through the
// workspace proxy with HTMLToText), and the raw storage path for office/PDF
// binaries the agent must open with its file tools. Mirrors FullEmailText —
// best-effort, all empties + nil error when the row carries nothing. Shared by
// the seed enrichment so a discussed document is actually IN context, not just
// named. No-row / bad-id surfaces as a non-nil error the caller degrades on.
func (a *API) FullArtifactText(ctx context.Context, id string) (markdown, htmlPath, storagePath, format string, err error) {
	if a == nil || a.Pool == nil || strings.TrimSpace(id) == "" {
		return "", "", "", "", nil
	}
	err = a.Pool.QueryRow(ctx, `
		SELECT COALESCE(metadata->>'markdown',''),
		       COALESCE(metadata->>'html_path',''),
		       COALESCE(storage_path,''),
		       COALESCE(metadata->>'format','')
		  FROM mem_artifacts
		 WHERE id = $1::uuid AND deleted_at IS NULL
	`, id).Scan(&markdown, &htmlPath, &storagePath, &format)
	if err != nil {
		return "", "", "", "", err
	}
	return strings.TrimSpace(markdown), strings.TrimSpace(htmlPath),
		strings.TrimSpace(storagePath), strings.TrimSpace(format), nil
}

// HTMLToText exposes the package's HTML→plain-text flattener (used for email
// bodies) so callers outside the package can render an HTML document preview —
// e.g. a spreadsheet's html_path — into legible text for the agent.
func HTMLToText(h string) string { return stripHTML(h) }

// looksHTML is a cheap check for whether a body is HTML markup rather than
// plain text, so we only run the tag-stripper when there's something to strip.
func looksHTML(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "<!doctype") || strings.Contains(l, "<html") ||
		strings.Contains(l, "</") || strings.Contains(l, "<br") ||
		strings.Contains(l, "<div") || strings.Contains(l, "<table") ||
		strings.Contains(l, "<p>") || strings.Contains(l, "<span")
}

// resolveFollowupRef is the HTTP wrapper around ResolveFollowupRefByID; it
// reads id/origin from the query and writes the appropriate error.
func (a *API) resolveFollowupRef(w http.ResponseWriter, r *http.Request) (source, accountHint, messageID string, ok bool) {
	source, accountHint, messageID, err := a.ResolveFollowupRefByID(
		r.Context(), r.URL.Query().Get("id"), r.URL.Query().Get("origin"))
	if err != nil {
		switch err.Error() {
		case "id required", "unknown origin":
			writeErr(w, http.StatusBadRequest, err.Error())
		default:
			a.Logger.Warn("followup ref", "err", err)
			writeErr(w, http.StatusNotFound, "not found")
		}
		return "", "", "", false
	}
	return source, accountHint, messageID, true
}

// stripHTML reduces an HTML email body to readable plain text: drops
// script/style blocks, strips tags, unescapes entities, collapses whitespace.
// Good enough to hand the agent a legible version of an HTML-only email.
func stripHTML(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return ""
	}
	// Remove <script>…</script> and <style>…</style> blocks entirely.
	for _, tag := range []string{"script", "style", "head"} {
		for {
			lo := strings.Index(strings.ToLower(h), "<"+tag)
			if lo < 0 {
				break
			}
			hi := strings.Index(strings.ToLower(h[lo:]), "</"+tag+">")
			if hi < 0 {
				h = h[:lo]
				break
			}
			h = h[:lo] + " " + h[lo+hi+len(tag)+3:]
		}
	}
	// Turn block-ish tags into newlines so the text doesn't run together.
	repl := strings.NewReplacer("</p>", "\n", "<br>", "\n", "<br/>", "\n", "<br />", "\n", "</div>", "\n", "</tr>", "\n", "</li>", "\n")
	h = repl.Replace(h)
	// Strip all remaining tags.
	var b strings.Builder
	depth := 0
	for _, r := range h {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	text := htmlpkg.UnescapeString(b.String())
	// Drop email preheader padding (zero-width + nbsp runs senders use to push
	// the preview text out) so the agent sees clean prose, not Unicode soup.
	text = strings.NewReplacer(
		"\u200c", "", "\u200b", "", "\ufeff", "", "\u00ad", "", "\u00a0", " ",
	).Replace(text)
	// Collapse runs of blank lines / spaces.
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// handleFollowupMessage lazily resolves and returns the full email body +
// attachment metadata for one follow-up. Nothing is fetched at poll time, so
// this is the ONLY place the (potentially large) HTML body is retrieved - and
// only when the boss actually opens the item.
// Response: { html, text, attachments: [{name, mimeType, id}] }.
//
//	GET /api/followups/message?id=<row id>&origin=followup|surface
func (a *API) handleFollowupMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.Pool == nil {
		writeErr(w, http.StatusServiceUnavailable, "no pool")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	origin := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("origin")))
	if origin == "" {
		origin = "followup"
	}

	// Serve the stored body FIRST. Email bodies are immutable, so once we've
	// captured one a live re-fetch is pure cost - and, crucially, it makes the
	// viewer revocation-proof: a connector going REVOKED later can never blank
	// an email we already rendered once.
	if html, text, ok := a.storedFollowupBody(r.Context(), id, origin); ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"html": html, "text": text, "attachments": []connectors.Attachment{},
		})
		return
	}

	source, accountHint, messageID, ok := a.resolveFollowupRef(w, r)
	if !ok {
		return
	}

	empty := map[string]any{"html": "", "text": "", "attachments": []connectors.Attachment{}}
	// No fetcher wired (no Composio) or no message id to fetch → empty bodies.
	// The UI falls back to the preview it already holds. Not an error.
	if a.Fetcher == nil || messageID == "" {
		writeJSON(w, http.StatusOK, empty)
		return
	}
	html, text, attachments, err := a.Fetcher.FetchMessage(r.Context(), source, accountHint, messageID)
	if err != nil {
		a.Logger.Warn("followup message: fetch", "source", source, "err", err)
		// We have no stored copy AND the live fetch failed (commonly a revoked
		// account with no reconnected twin yet). Return an explicit error flag
		// so the viewer shows an honest "couldn't load — reconnect" state
		// instead of silently rendering the stale list subtext.
		empty["error"] = "fetch_failed"
		writeJSON(w, http.StatusOK, empty)
		return
	}
	if attachments == nil {
		attachments = []connectors.Attachment{}
	}
	// Capture-on-fetch: persist the body so every later open is instant and
	// survives the connector being revoked. Best-effort - a cache write failure
	// must never block returning the email we just fetched.
	a.cacheFollowupBody(r.Context(), id, origin, html, text)
	writeJSON(w, http.StatusOK, map[string]any{"html": html, "text": text, "attachments": attachments})
}

// storedFollowupBody returns a previously-captured email body for a follow-up.
// ok=true only when there is a non-empty stored html OR text, so a row that
// merely has the columns present (NULL) still takes the live-fetch path.
func (a *API) storedFollowupBody(ctx context.Context, id, origin string) (html, text string, ok bool) {
	if a.Pool == nil || id == "" {
		return "", "", false
	}
	table := "mem_followups"
	if origin == "surface" {
		table = "mem_surface_items"
	}
	// table is from a fixed all-list above, never user input - safe to format.
	var h, t *string
	err := a.Pool.QueryRow(ctx,
		"SELECT cached_html, cached_text FROM "+table+" WHERE id::text = $1", id,
	).Scan(&h, &t)
	if err != nil {
		return "", "", false
	}
	if h != nil {
		html = *h
	}
	if t != nil {
		text = *t
	}
	if strings.TrimSpace(html) == "" && strings.TrimSpace(text) == "" {
		return "", "", false
	}
	return html, text, true
}

// cacheFollowupBody persists a freshly-fetched email body onto the follow-up
// row so future opens serve it without a live connector call. Best-effort:
// errors are logged, never surfaced - the caller already has the body to
// return.
func (a *API) cacheFollowupBody(ctx context.Context, id, origin, html, text string) {
	if a.Pool == nil || id == "" {
		return
	}
	if strings.TrimSpace(html) == "" && strings.TrimSpace(text) == "" {
		return
	}
	table := "mem_followups"
	if origin == "surface" {
		table = "mem_surface_items"
	}
	if _, err := a.Pool.Exec(ctx,
		"UPDATE "+table+" SET cached_html = $1, cached_text = $2 WHERE id::text = $3",
		html, text, id,
	); err != nil {
		a.Logger.Warn("followup message: cache body", "id", id, "origin", origin, "err", err)
	}
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
