package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// One object, by kind and id. GET /api/object?kind=…&id=…
//
// The read side of /api/search. A search hit is a title and one line of meta;
// this is what sits behind it when the boss taps it, so a result can open
// where he is - a sheet on the dashboard, a drawer on his phone - instead of
// throwing him onto another page to look for the row he just found.
//
// GENERIC BY CONSTRUCTION. The response is a title, a context line, a body and
// a flat list of labelled fields. Go decides what this kind's fields are
// called and what its "Open in …" button says; Studio renders whatever it is
// handed and holds ZERO per-kind branches. That is the difference between a
// ninth searchable table costing one entry in searchSources() and it costing a
// query here, a widget there, and a switch in between that will drift.
//
// The detail projection lives on searchSource next to the search projection on
// purpose (Rule #1c). Two parallel lists would let a kind be added to one and
// forgotten in the other, and a hit that opens an empty sheet is exactly the
// half-built shape the rule exists to prevent.

type recordField struct {
	Label string `json:"label"`
	Value string `json:"value"`
	// ids, tiers, cron expressions, tool names - things that want mono.
	Mono bool `json:"mono,omitempty"`
}

type recordDetail struct {
	// The SEARCH kind, not a rendering hint. Studio seeds a "discuss this"
	// session with it, so it has to be the word that means something to the
	// agent ("memory", "skill", …).
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Body     string `json:"body"`
	// Only fields that HAVE a value; see (*recordDetail).add. §7's "only
	// sections with data render" is enforced by the producer so no renderer
	// has to re-derive it.
	Fields []recordField `json:"fields"`
	// Canonical page for this object, and the words on the button. Go owns the
	// label so Studio never keeps a second copy of the kind-to-page mapping.
	Href      string     `json:"href"`
	HrefLabel string     `json:"hrefLabel"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

// add appends a field, skipping anything empty. Every detail func builds its
// field list through this, which is what keeps empty rows out of the sheet
// without a single `if v != ""` at each call site.
func (d *recordDetail) add(label, value string) {
	v := strings.TrimSpace(value)
	if v == "" {
		return
	}
	d.Fields = append(d.Fields, recordField{Label: label, Value: v})
}

// addMono is add, for values that are machine strings.
func (d *recordDetail) addMono(label, value string) {
	v := strings.TrimSpace(value)
	if v == "" {
		return
	}
	d.Fields = append(d.Fields, recordField{Label: label, Value: v, Mono: true})
}

func (d *recordDetail) addTime(label string, t *time.Time) {
	if t == nil || t.IsZero() {
		return
	}
	d.add(label, t.Format("Mon 2 Jan 2006, 3:04pm"))
}

func (s *Server) handleObject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if kind == "" || id == "" {
		http.Error(w, "kind and id are required", http.StatusBadRequest)
		return
	}
	if s.cfg.Pool == nil {
		http.Error(w, "no database", http.StatusServiceUnavailable)
		return
	}

	var fn func(context.Context, *pgxpool.Pool, string) (*recordDetail, error)
	for _, src := range searchSources() {
		if src.kind == kind {
			fn = src.detail
			break
		}
	}
	if fn == nil {
		http.Error(w, "unknown kind", http.StatusNotFound)
		return
	}

	got, err := fn(r.Context(), s.cfg.Pool, id)
	if err != nil {
		// A row that is not there and a query that could not run are
		// different answers and must stay different. Collapsing the second
		// into a 404 is how a broken table starts reading as "the boss
		// deleted that", which is the bug class this codebase has a standing
		// rule about.
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		log.Printf("object %s/%s: %v", kind, id, err)
		http.Error(w, "could not load that record", http.StatusInternalServerError)
		return
	}
	got.Kind = kind
	got.ID = id
	writeJSON(w, http.StatusOK, got)
}

// ── per-kind projections ──────────────────────────────────────────────────

func detailMemory(ctx context.Context, pool *pgxpool.Pool, id string) (*recordDetail, error) {
	var (
		title, content, tier, project, status string
		importance                            int
		created, updated                      *time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(title, ''), COALESCE(content, ''), COALESCE(tier, ''),
		       COALESCE(project, ''), COALESCE(status, ''), COALESCE(importance, 0),
		       created_at, updated_at
		  FROM mem_memories WHERE id = $1::uuid
	`, id).Scan(&title, &content, &tier, &project, &status, &importance, &created, &updated)
	if err != nil {
		return nil, err
	}
	d := &recordDetail{
		Title:     firstNonEmpty(strings.TrimSpace(title), firstLine(content)),
		Subtitle:  memoryTierLabel(tier),
		Body:      content,
		Href:      "/memory?focus=" + id + "&kind=memory",
		HrefLabel: "Open in Memory",
		CreatedAt: created,
	}
	d.addMono("Tier", tier)
	d.add("Project", project)
	if importance > 0 {
		d.add("Importance", strconv.Itoa(importance))
	}
	d.add("Status", status)
	d.addTime("Last updated", updated)
	return d, nil
}

func detailSkill(ctx context.Context, pool *pgxpool.Pool, id string) (*recordDetail, error) {
	var (
		name, desc, risk, status, source, triggers string
		confidence                                 float64
		created, updated                           *time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT name, COALESCE(description, ''), COALESCE(risk_level, ''),
		       COALESCE(status, ''), COALESCE(source, ''),
		       COALESCE(trigger_phrases::text, '[]'), COALESCE(confidence, 0),
		       created_at, updated_at
		  FROM mem_skills WHERE name = $1
	`, id).Scan(&name, &desc, &risk, &status, &source, &triggers, &confidence, &created, &updated)
	if err != nil {
		return nil, err
	}
	d := &recordDetail{
		Title:     name,
		Subtitle:  "Skill",
		Body:      desc,
		Href:      "/skills?focus=" + id + "&kind=skill",
		HrefLabel: "Open in Skills",
		CreatedAt: created,
	}
	d.addMono("Risk", risk)
	d.add("Status", status)
	d.add("Learned from", source)
	if confidence > 0 {
		d.add("Confidence", fmt.Sprintf("%.0f%%", confidence*100))
	}
	d.add("Fires on", joinJSONStrings(triggers))
	d.addTime("Last updated", updated)
	return d, nil
}

func detailAutomation(ctx context.Context, pool *pgxpool.Pool, id string) (*recordDetail, error) {
	var (
		label, sub, expr, target, lastStatus string
		enabled                              bool
		lastAt, created                      *time.Time
	)
	// Same UNION the search side uses: schedules and watchers are one kind to
	// the boss, so they are one query rather than two he has to tell apart.
	err := pool.QueryRow(ctx, `
		SELECT label, sub, expr, target, last_status, enabled, last_at, created_at FROM (
		  SELECT id,
		         COALESCE(NULLIF(schedule_natural, ''), name) AS label,
		         'Runs on a schedule' AS sub,
		         COALESCE(schedule, '') AS expr,
		         COALESCE(target, '') AS target,
		         COALESCE(last_run_status, '') AS last_status,
		         enabled, last_run_at AS last_at, created_at
		    FROM mem_crons
		  UNION ALL
		  SELECT id, name, 'Watches for something', COALESCE(watch_type, ''),
		         COALESCE(action_chain::text, '[]'), '',
		         enabled, last_triggered_at, created_at
		    FROM mem_sentinels
		) t WHERE id = $1::uuid
	`, id).Scan(&label, &sub, &expr, &target, &lastStatus, &enabled, &lastAt, &created)
	if err != nil {
		return nil, err
	}
	d := &recordDetail{
		Title:     label,
		Subtitle:  sub,
		Href:      "/automations?focus=" + id + "&kind=automation",
		HrefLabel: "Open in Automations",
		CreatedAt: created,
	}
	d.addMono("Trigger", expr)
	d.addMono("Runs", target)
	d.add("On right now", yesNo(enabled))
	d.add("Last outcome", lastStatus)
	d.addTime("Last fired", lastAt)
	return d, nil
}

func detailSurfaced(ctx context.Context, pool *pgxpool.Pool, id string) (*recordDetail, error) {
	var (
		title, subtitle, body, source, kind, surface, url, status string
		importance                                               int
		created                                                  *time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT title, COALESCE(subtitle, ''), COALESCE(body, ''),
		       COALESCE(source, ''), COALESCE(kind, ''), COALESCE(surface, ''),
		       COALESCE(url, ''), COALESCE(status, ''), COALESCE(importance, 0),
		       created_at
		  FROM mem_surface_items WHERE id = $1::uuid
	`, id).Scan(&title, &subtitle, &body, &source, &kind, &surface, &url, &status, &importance, &created)
	if err != nil {
		return nil, err
	}
	d := &recordDetail{
		Title:     title,
		Subtitle:  firstNonEmpty(subtitle, "Surfaced by Jarvis"),
		Body:      body,
		Href:      "/?focus=" + id + "&kind=surfaced",
		HrefLabel: "Open on the dashboard",
		CreatedAt: created,
	}
	d.add("Where", surface)
	d.add("What", kind)
	d.add("From", source)
	if importance > 0 {
		d.add("Importance", strconv.Itoa(importance))
	}
	d.add("Status", status)
	d.add("Link", url)
	return d, nil
}

func detailSession(ctx context.Context, pool *pgxpool.Pool, id string) (*recordDetail, error) {
	var (
		name, kind, project     string
		inTokens, outTokens     int64
		started, lastRun, ended *time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(btrim(name), ''), 'Untitled'),
		       COALESCE(kind, ''), COALESCE(project, ''),
		       COALESCE(total_input_tokens, 0), COALESCE(total_output_tokens, 0),
		       started_at, last_run_at, ended_at
		  FROM mem_sessions WHERE id = $1::uuid AND deleted_at IS NULL
	`, id).Scan(&name, &kind, &project, &inTokens, &outTokens, &started, &lastRun, &ended)
	if err != nil {
		return nil, err
	}
	d := &recordDetail{
		Title:     name,
		Subtitle:  "Conversation",
		Href:      "/live?session=" + id,
		HrefLabel: "Open the conversation",
		CreatedAt: started,
	}
	d.add("Started by", sessionKindLabel(kind))
	d.add("Project", project)
	if tot := inTokens + outTokens; tot > 0 {
		d.add("Tokens used", strconv.FormatInt(tot, 10))
	}
	d.addTime("Started", started)
	d.addTime("Last active", firstTime(lastRun, ended))
	return d, nil
}

func detailLesson(ctx context.Context, pool *pgxpool.Pool, id string) (*recordDetail, error) {
	var (
		kind, critique, lessons, sessionID string
		quality, importance                float64
		created                            *time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(kind, 'reflection'), COALESCE(critique, ''),
		       COALESCE(lessons::text, '[]'), COALESCE(session_id::text, ''),
		       COALESCE(quality_score, 0), COALESCE(importance, 0), created_at
		  FROM mem_reflections WHERE id = $1::uuid
	`, id).Scan(&kind, &critique, &lessons, &sessionID, &quality, &importance, &created)
	if err != nil {
		return nil, err
	}
	d := &recordDetail{
		Title:     firstLine(critique),
		Subtitle:  "Something he learned",
		Body:      critique,
		Href:      "/memory?view=lessons&focus=" + id + "&kind=lesson",
		HrefLabel: "Open in Memory",
		CreatedAt: created,
	}
	d.addMono("Kind", kind)
	d.add("Took away", joinJSONStrings(lessons))
	if quality > 0 {
		d.add("Quality", fmt.Sprintf("%.2f", quality))
	}
	if importance > 0 {
		d.add("Importance", fmt.Sprintf("%.0f", importance))
	}
	d.addMono("Session", sessionID)
	return d, nil
}

func detailPrediction(ctx context.Context, pool *pgxpool.Pool, id string) (*recordDetail, error) {
	var (
		tool, expected, actual, toolInput string
		matched                           *bool
		surprise                          float64
		created, resolved                 *time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(tool_name, ''), COALESCE(expected, ''), COALESCE(actual, ''),
		       COALESCE(tool_input::text, '{}'), matched, COALESCE(surprise_score, 0),
		       created_at, resolved_at
		  FROM mem_predictions WHERE id = $1::uuid
	`, id).Scan(&tool, &expected, &actual, &toolInput, &matched, &surprise, &created, &resolved)
	if err != nil {
		return nil, err
	}
	d := &recordDetail{
		Title:     firstNonEmpty(firstLine(expected), tool),
		Subtitle:  "What he expected to happen",
		Body:      expected,
		Href:      "/memory?view=wrong&focus=" + id + "&kind=prediction",
		HrefLabel: "Open in Memory",
		CreatedAt: created,
	}
	d.addMono("Tool", tool)
	d.add("What actually happened", actual)
	if matched != nil {
		d.add("Called it right", yesNo(*matched))
	}
	if surprise > 0 {
		d.add("Surprise", fmt.Sprintf("%.2f", surprise))
	}
	d.addTime("Resolved", resolved)
	return d, nil
}

func detailObservation(ctx context.Context, pool *pgxpool.Pool, id string) (*recordDetail, error) {
	var (
		raw, hook, sessionID string
		created              *time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(raw_text, ''), COALESCE(hook_name, ''),
		       COALESCE(session_id::text, ''), created_at
		  FROM mem_observations WHERE id = $1::uuid
	`, id).Scan(&raw, &hook, &sessionID, &created)
	if err != nil {
		return nil, err
	}
	d := &recordDetail{
		Title:     firstLine(raw),
		Subtitle:  "Something he saw",
		Body:      raw,
		Href:      "/memory?view=seen&focus=" + id + "&kind=observation",
		HrefLabel: "Open in Memory",
		CreatedAt: created,
	}
	d.addMono("Captured by", hook)
	d.addMono("Session", sessionID)
	return d, nil
}

// ── small helpers ─────────────────────────────────────────────────────────

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstTime(vals ...*time.Time) *time.Time {
	for _, v := range vals {
		if v != nil && !v.IsZero() {
			return v
		}
	}
	return nil
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// joinJSONStrings renders a jsonb string array as a readable list. Anything
// that is not a string array (an object, a malformed value) yields "" so the
// field is skipped rather than printing raw JSON at the boss.
func joinJSONStrings(raw string) string {
	var arr []string
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return ""
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return strings.Join(out, ", ")
}

// sessionKindLabel says who opened a conversation in words rather than in the
// enum the scheduler writes.
func sessionKindLabel(kind string) string {
	switch kind {
	case "", "user":
		return "You"
	case "cron":
		return "A schedule"
	case "sentinel":
		return "A watcher"
	case "heartbeat":
		return "His heartbeat"
	default:
		return strings.ToUpper(kind[:1]) + kind[1:]
	}
}
