// Package connectors - deterministic dashboard pollers.
//
// The Poller is the bridge between Composio's tools.execute REST surface
// and the dashboard substrate (mem_followups / mem_calendar_events from
// migration 014). It runs in three shapes:
//
//   • Inline tick from a connector_poll cron job   (most common)
//   • Manual fire via the cron_run_now agent tool   (debug / one-off)
//   • Direct call from a sentinel reaction          (future)
//
// The shape of a poll is defined by a PollConfig (the JSONB stored in
// mem_crons.target_config for connector_poll jobs). The Poller decodes
// that, calls Composio, transforms the response per `sink`, upserts to
// the dashboard table with dedupe-by-remote-id, and emits a hook so the
// observation also lands in mem_observations through the existing
// capture pipeline (privacy-stripped + provenance-linked).
//
// We deliberately keep transformation logic per-toolkit small. Gmail and
// Google Calendar are the two sinks shipped here; new toolkits add a
// `case` branch + a small projector. The alternative - a generic JSON
// path mapper - adds config-file weight for no real win when there are
// <10 toolkits in active rotation.

package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/hooks"
	"github.com/jackc/pgx/v5/pgxpool"
)

// infoLog routes connector poller success lines to stdout so Railway tags
// them as info, not error. See CLAUDE.md "Logging - severity must match
// reality" - `log.Printf` defaults to stderr.
var infoLog = log.New(os.Stdout, "", log.LstdFlags)

// PollConfig is the JSONB shape stored in mem_crons.target_config for a
// connector_poll job. It's deliberately narrow - toolkit + action + the
// connected_account_id + freeform arguments + sink.
type PollConfig struct {
	Toolkit            string         `json:"toolkit"`              // "gmail", "googlecalendar", ...
	Action             string         `json:"action"`               // "GMAIL_FETCH_EMAILS"
	ConnectedAccountID string         `json:"connected_account_id"` // "ca_..."
	Arguments          map[string]any `json:"arguments,omitempty"`
	Sink               string         `json:"sink"` // "followups" | "calendar"
}

// Validate checks for the required surface area before we hit Composio.
// Returns the first missing-field error or nil.
func (p PollConfig) Validate() error {
	if strings.TrimSpace(p.Action) == "" {
		return fmt.Errorf("target_config.action required")
	}
	if strings.TrimSpace(p.ConnectedAccountID) == "" {
		return fmt.Errorf("target_config.connected_account_id required")
	}
	switch p.Sink {
	case "followups", "calendar":
	default:
		return fmt.Errorf("target_config.sink must be 'followups' or 'calendar' (got %q)", p.Sink)
	}
	return nil
}

// Poller runs the connector → dashboard pipeline. Cheap to construct;
// callers usually keep one process-wide instance.
type Poller struct {
	pool     *pgxpool.Pool
	exec     *ExecuteClient
	pipeline *hooks.Pipeline
	logger   *slog.Logger
}

func NewPoller(pool *pgxpool.Pool, exec *ExecuteClient, pipeline *hooks.Pipeline) *Poller {
	return &Poller{
		pool:     pool,
		exec:     exec,
		pipeline: pipeline,
		logger:   slog.Default().With("component", "connector_poller"),
	}
}

// PollResult is what a Poll call returns. Counts are populated even on
// partial failure so cron's last_run_status string can carry visibility
// without forcing the caller to re-query.
type PollResult struct {
	JobName   string `json:"job_name"`
	Fetched   int    `json:"fetched"`   // items returned by Composio
	Written   int    `json:"written"`   // items upserted (new)
	Skipped   int    `json:"skipped"`   // duplicates (ON CONFLICT DO NOTHING)
	Errors    int    `json:"errors"`
	DurationMS int64 `json:"duration_ms"`
}

// Poll runs one full cycle for the given config. jobName is only used for
// logging / hook payload identification (set to the cron row's name from
// the executor, or "manual" for ad-hoc fires).
func (p *Poller) Poll(ctx context.Context, jobName string, cfg PollConfig) (*PollResult, error) {
	if p == nil || p.pool == nil || p.exec == nil {
		return nil, fmt.Errorf("poller not configured (pool/exec missing)")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	start := time.Now()
	resp, err := p.exec.Execute(ctx, ExecuteRequest{
		Slug:               cfg.Action,
		ConnectedAccountID: cfg.ConnectedAccountID,
		Arguments:          cfg.Arguments,
	})
	if err != nil {
		return &PollResult{JobName: jobName, Errors: 1, DurationMS: time.Since(start).Milliseconds()}, err
	}
	if !resp.Successful {
		return &PollResult{JobName: jobName, Errors: 1, DurationMS: time.Since(start).Milliseconds()},
			fmt.Errorf("composio reported failure: %s", resp.Error)
	}

	out := &PollResult{JobName: jobName}
	switch cfg.Sink {
	case "followups":
		out.Fetched, out.Written, out.Skipped, err = p.writeFollowups(ctx, cfg, resp.Data)
	case "calendar":
		out.Fetched, out.Written, out.Skipped, err = p.writeCalendar(ctx, cfg, resp.Data)
	default:
		err = fmt.Errorf("unknown sink %q", cfg.Sink)
	}
	out.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		out.Errors = 1
	}

	// Emit a hook so the capture pipeline records what happened. Even
	// when zero new items land, the run itself is observation-worthy -
	// "poll happened, nothing new" is signal for curiosity / reflection.
	if p.pipeline != nil {
		p.pipeline.Emit(hooks.Event{
			Name: hooks.PostToolUse,
			Text: fmt.Sprintf("connector_poll %s/%s: fetched=%d written=%d skipped=%d",
				cfg.Toolkit, cfg.Action, out.Fetched, out.Written, out.Skipped),
			Payload: map[string]any{
				"tool":     "connector_poll",
				"job_name": jobName,
				"toolkit":  cfg.Toolkit,
				"action":   cfg.Action,
				"result":   out,
			},
		})
	}

	infoLog.Printf("connector poll %s (%s): fetched=%d written=%d skipped=%d errors=%d",
		jobName, cfg.Action, out.Fetched, out.Written, out.Skipped, out.Errors)
	return out, err
}

// ── Followups (Gmail and friends) ─────────────────────────────────────────
//
// Composio's GMAIL_FETCH_EMAILS returns a payload roughly like:
//   { "messages": [ { "messageId": "...", "threadId": "...", "subject": "...",
//                     "sender": "Alice <a@b>", "preview": "...", "messageText": "...",
//                     "messageTimestamp": "..." } ], ... }
//
// We accept the loose shape with map[string]any so newer/older response
// versions don't break the writer; we extract what we can and degrade
// gracefully when a field is missing.

func (p *Poller) writeFollowups(ctx context.Context, cfg PollConfig, raw json.RawMessage) (fetched, written, skipped int, err error) {
	if len(raw) == 0 {
		return 0, 0, 0, nil
	}
	// Try the GMAIL_FETCH_EMAILS shape first.
	var envelope struct {
		Messages []map[string]any `json:"messages"`
		Items    []map[string]any `json:"items"` // alternate shape
		Results  []map[string]any `json:"results"` // alternate shape
	}
	if jerr := json.Unmarshal(raw, &envelope); jerr != nil {
		return 0, 0, 0, fmt.Errorf("decode envelope: %w", jerr)
	}
	items := envelope.Messages
	if len(items) == 0 {
		items = envelope.Items
	}
	if len(items) == 0 {
		items = envelope.Results
	}
	fetched = len(items)

	source := strings.ToLower(strings.TrimSpace(cfg.Toolkit))
	if source == "" {
		source = "gmail"
	}

	for _, m := range items {
		remoteID := firstString(m, "messageId", "id", "message_id", "remote_id")
		if remoteID == "" {
			continue
		}
		fromName := firstString(m, "sender", "from", "from_name")
		subject := firstString(m, "subject", "title")
		preview := firstString(m, "preview", "snippet")
		body := firstString(m, "messageText", "body", "text")
		threadURL := firstString(m, "threadUrl", "permalink", "url")
		// Best-effort received_at parse - falls back to NOW() on the
		// INSERT clause when nil.
		receivedAt := parseTimeAny(m, "messageTimestamp", "internalDate", "received_at", "timestamp")

		srcRef, _ := json.Marshal(map[string]any{
			"remote_id": remoteID,
			"thread_id": firstString(m, "threadId", "thread_id"),
			"account":   cfg.ConnectedAccountID,
		})

		tag, derr := p.pool.Exec(ctx, `
			INSERT INTO mem_followups
			    (source, account, from_name, subject, preview, body,
			     thread_url, source_ref, received_at, status, unread)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,COALESCE($9, NOW()),'open',TRUE)
			ON CONFLICT (source, ((source_ref->>'remote_id'))) DO NOTHING
		`, source, cfg.ConnectedAccountID, fromName, subject, preview, body, threadURL, string(srcRef), receivedAt)
		if derr != nil {
			p.logger.Warn("followup upsert", "remote_id", remoteID, "err", derr)
			continue
		}
		if tag.RowsAffected() > 0 {
			written++
		} else {
			skipped++
		}
	}
	return
}

// ── Calendar (Google Calendar and friends) ────────────────────────────────
//
// GOOGLECALENDAR_EVENTS_LIST returns:
//   { "items": [ { "id": "...", "summary": "...", "location": "...",
//                  "start": { "dateTime": "..." } / { "date": "..." },
//                  "end":   { ... }, "attendees": [...] } ] }

func (p *Poller) writeCalendar(ctx context.Context, cfg PollConfig, raw json.RawMessage) (fetched, written, skipped int, err error) {
	if len(raw) == 0 {
		return 0, 0, 0, nil
	}
	var envelope struct {
		Items   []map[string]any `json:"items"`
		Events  []map[string]any `json:"events"`
		Results []map[string]any `json:"results"`
	}
	if jerr := json.Unmarshal(raw, &envelope); jerr != nil {
		return 0, 0, 0, fmt.Errorf("decode envelope: %w", jerr)
	}
	items := envelope.Items
	if len(items) == 0 {
		items = envelope.Events
	}
	if len(items) == 0 {
		items = envelope.Results
	}
	fetched = len(items)

	source := strings.ToLower(strings.TrimSpace(cfg.Toolkit))
	if source == "" {
		source = "googlecalendar"
	}

	for _, ev := range items {
		remoteID := firstString(ev, "id", "remote_id", "event_id")
		if remoteID == "" {
			continue
		}
		title := firstString(ev, "summary", "title", "name")
		if title == "" {
			title = "(no title)"
		}
		location := firstString(ev, "location")
		startsAt, allDay := parseCalendarTime(ev, "start")
		endsAt, _ := parseCalendarTime(ev, "end")

		attendees, _ := json.Marshal(ev["attendees"])
		if string(attendees) == "null" {
			attendees = []byte("[]")
		}
		srcRef, _ := json.Marshal(map[string]any{
			"remote_id": remoteID,
			"account":   cfg.ConnectedAccountID,
			"html_link": firstString(ev, "htmlLink", "url"),
		})

		// Skip events with no start time - calendar table requires NOT NULL.
		if startsAt == nil {
			continue
		}

		tag, derr := p.pool.Exec(ctx, `
			INSERT INTO mem_calendar_events
			    (title, location, attendees, starts_at, ends_at, all_day,
			     classification, prep, source, source_ref)
			VALUES ($1,$2,$3::jsonb,$4,$5,$6,'other','[]'::jsonb,$7,$8::jsonb)
			ON CONFLICT (source, ((source_ref->>'remote_id'))) DO UPDATE SET
			    title = EXCLUDED.title,
			    location = EXCLUDED.location,
			    attendees = EXCLUDED.attendees,
			    starts_at = EXCLUDED.starts_at,
			    ends_at = EXCLUDED.ends_at,
			    all_day = EXCLUDED.all_day,
			    updated_at = NOW()
		`, title, location, string(attendees), *startsAt, endsAt, allDay, source, string(srcRef))
		if derr != nil {
			p.logger.Warn("calendar upsert", "remote_id", remoteID, "err", derr)
			continue
		}
		if tag.RowsAffected() > 0 {
			written++ // ON CONFLICT DO UPDATE returns 1; treat updates as writes
		} else {
			skipped++
		}
	}
	return
}

// ── tiny helpers ──────────────────────────────────────────────────────────

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// parseTimeAny tries each key in order; returns *time.Time so the INSERT
// can pass NULL through when nothing parsed (received_at COALESCEs to NOW()).
func parseTimeAny(m map[string]any, keys ...string) *time.Time {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			// Gmail's internalDate is sometimes a string of ms-since-epoch
			// inside a number-typed JSON value. Coerce.
			if n, ok := v.(float64); ok {
				t := time.UnixMilli(int64(n)).UTC()
				return &t
			}
			continue
		}
		for _, layout := range []string{time.RFC3339, time.RFC3339Nano, time.RFC1123Z, time.RFC1123} {
			if t, err := time.Parse(layout, s); err == nil {
				t = t.UTC()
				return &t
			}
		}
	}
	return nil
}

// parseCalendarTime decodes Google Calendar's `{ dateTime } | { date }` shape.
// Returns (timestamp, allDay). When parse fails, returns (nil, false).
func parseCalendarTime(ev map[string]any, key string) (*time.Time, bool) {
	raw, ok := ev[key].(map[string]any)
	if !ok || raw == nil {
		return nil, false
	}
	if dt, ok := raw["dateTime"].(string); ok && strings.TrimSpace(dt) != "" {
		if t, err := time.Parse(time.RFC3339, dt); err == nil {
			t = t.UTC()
			return &t, false
		}
	}
	if d, ok := raw["date"].(string); ok && strings.TrimSpace(d) != "" {
		if t, err := time.Parse("2006-01-02", d); err == nil {
			t = t.UTC()
			return &t, true
		}
	}
	return nil, false
}
