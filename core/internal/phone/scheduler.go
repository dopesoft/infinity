// scheduler.go — one-shot scheduled calls + business-hours snapping + retry.
//
// Cron is recurring-only, so a precise "call the dentist tomorrow at 3" gets
// its own fire-once poller (migration 174), modeled on watch.Poller: a
// status-guarded UPDATE guarantees exactly one dispatch even across restarts.
// A scheduled call reuses the identical brief + dial path as an immediate
// one, so it is byte-for-byte the same call, just later.
package phone

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"
)

// bossTZ is the boss's timezone (CST, per memory). Business-hours snapping
// keeps scheduled calls and retries inside a sensible window.
const (
	callHourOpen  = 8  // 8am
	callHourClose = 21 // 9pm
	maxRetries    = 3
)

func bossLocation() *time.Location {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		return time.UTC
	}
	return loc
}

// nextCallableTime snaps a target time into the calling window (8am-9pm boss
// local). Before 8am -> 8am same day; at/after 9pm -> 8am next day; inside
// the window -> unchanged. Applied to SCHEDULED calls and retries only, never
// to an immediate boss-commissioned call (his asking now is the intent).
func nextCallableTime(t time.Time) time.Time {
	loc := bossLocation()
	lt := t.In(loc)
	h := lt.Hour()
	if h >= callHourOpen && h < callHourClose {
		return t
	}
	day := lt
	if h >= callHourClose {
		day = lt.AddDate(0, 0, 1)
	}
	open := time.Date(day.Year(), day.Month(), day.Day(), callHourOpen, 0, 0, 0, loc)
	return open
}

// ScheduleCall inserts a one-shot scheduled call, snapped into calling hours.
func (m *Manager) ScheduleCall(ctx context.Context, brief *Brief, at time.Time, note string) (time.Time, error) {
	fireAt := nextCallableTime(at)
	payload, _ := json.Marshal(brief)
	_, err := m.pool.Exec(ctx, `
		INSERT INTO mem_scheduled_calls (payload, fire_at, note)
		VALUES ($1::jsonb, $2, $3)
	`, string(payload), fireAt.UTC(), note)
	return fireAt, err
}

// StartScheduler runs the due-call poller for the process lifetime.
func (m *Manager) StartScheduler(ctx context.Context) {
	if m == nil || m.pool == nil {
		return
	}
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.fireDueCalls(ctx)
			}
		}
	}()
	infoLog.Printf("phone: scheduled-call poller started")
}

func (m *Manager) fireDueCalls(ctx context.Context) {
	rows, err := m.pool.Query(ctx, `
		SELECT id::text FROM mem_scheduled_calls
		WHERE status = 'pending' AND fire_at <= NOW() LIMIT 5
	`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	for _, id := range ids {
		// Status-guard: only the winner of this UPDATE dispatches, so a
		// restart or a second poller tick can never double-fire.
		var payload string
		if err := m.pool.QueryRow(ctx, `
			UPDATE mem_scheduled_calls SET status='firing', updated_at=NOW()
			WHERE id=$1 AND status='pending' RETURNING payload
		`, id).Scan(&payload); err != nil {
			continue
		}
		var brief Brief
		if json.Unmarshal([]byte(payload), &brief) != nil || brief.To == "" {
			_, _ = m.pool.Exec(ctx, `UPDATE mem_scheduled_calls SET status='failed', updated_at=NOW() WHERE id=$1`, id)
			continue
		}
		briefID := uuid.NewString()
		status := "fired"
		if err := m.storeBrief(ctx, briefID, &brief); err != nil {
			status = "failed"
		} else if _, err := m.createTwilioCall(ctx, brief.To, briefID); err != nil {
			status = "failed"
			log.Printf("phone: scheduled call to %s failed to dial: %v", brief.To, err)
		} else {
			infoLog.Printf("phone: fired scheduled call to %s (brief=%s)", brief.To, briefID)
		}
		_, _ = m.pool.Exec(ctx, `UPDATE mem_scheduled_calls SET status=$2, updated_at=NOW() WHERE id=$1`, id, status)
	}
}

// enqueueRetry schedules a retry of a failed-to-connect call, capped at
// maxRetries, backing off and snapping into calling hours. Best-effort.
func (m *Manager) enqueueRetry(ctx context.Context, briefID string, backoff time.Duration) {
	if m.pool == nil || briefID == "" {
		return
	}
	raw, err := m.loadBriefRaw(ctx, briefID)
	if err != nil || raw == "" {
		return
	}
	var brief Brief
	if json.Unmarshal([]byte(raw), &brief) != nil {
		return
	}
	// Count prior retries for this number today to honor the cap.
	var priorToday int
	_ = m.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mem_scheduled_calls
		WHERE payload->>'to' = $1 AND note LIKE 'retry%' AND created_at > NOW() - INTERVAL '12 hours'
	`, brief.To).Scan(&priorToday)
	if priorToday >= maxRetries {
		return
	}
	_, _ = m.ScheduleCall(ctx, &brief, time.Now().Add(backoff), "retry after no-answer")
	infoLog.Printf("phone: queued retry for %s in %s", brief.To, backoff)
}
