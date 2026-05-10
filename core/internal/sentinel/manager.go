package sentinel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Manager owns the registered sentinels and the dispatch path. The watcher
// runtime for each WatchType plugs in via Driver; the v1 cut implements
// webhook (HTTP-driven, fired by Trigger), and stubs the rest behind feature
// flags so the schema and manifest stay stable.
type Manager struct {
	pool       *pgxpool.Pool
	dispatcher Dispatcher

	mu        sync.Mutex
	sentinels map[string]Sentinel
}

// Dispatcher executes the action chain. Implementations live alongside the
// agent loop / skill runner so this package stays free of those imports.
type Dispatcher interface {
	Dispatch(ctx context.Context, s Sentinel, payload map[string]any) error
}

func NewManager(p *pgxpool.Pool, d Dispatcher) *Manager {
	return &Manager{pool: p, dispatcher: d, sentinels: map[string]Sentinel{}}
}

// Reload refreshes the in-memory cache from the database.
func (m *Manager) Reload(ctx context.Context) error {
	if m == nil || m.pool == nil {
		return nil
	}
	rows, err := m.pool.Query(ctx, `
		SELECT id::text, name, watch_type, watch_config, action_chain,
		       cooldown_seconds, last_triggered_at, fire_count, enabled, created_at
		  FROM mem_sentinels
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := make(map[string]Sentinel)
	for rows.Next() {
		var s Sentinel
		var watch string
		var cfg, action []byte
		if err := rows.Scan(&s.ID, &s.Name, &watch, &cfg, &action,
			&s.CooldownSeconds, &s.LastTriggeredAt, &s.FireCount, &s.Enabled, &s.CreatedAt); err != nil {
			return err
		}
		s.WatchType = WatchType(watch)
		s.WatchConfig = json.RawMessage(cfg)
		s.ActionChain = json.RawMessage(action)
		out[s.ID] = s
	}
	m.mu.Lock()
	m.sentinels = out
	m.mu.Unlock()
	return rows.Err()
}

func (m *Manager) Get(id string) (Sentinel, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sentinels[id]
	return s, ok
}

func (m *Manager) List() []Sentinel {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Sentinel, 0, len(m.sentinels))
	for _, s := range m.sentinels {
		out = append(out, s)
	}
	return out
}

// Trigger is the entrypoint for a sentinel firing. Webhook handlers call this
// directly. Memory-event/file-change/threshold watchers (Phase 6b) will call
// this from their respective goroutines.
func (m *Manager) Trigger(ctx context.Context, id string, payload map[string]any) error {
	s, ok := m.Get(id)
	if !ok {
		return errors.New("sentinel not found")
	}
	if !s.Enabled {
		return errors.New("sentinel disabled")
	}
	if s.LastTriggeredAt != nil &&
		time.Since(*s.LastTriggeredAt) < time.Duration(s.CooldownSeconds)*time.Second {
		return errors.New("cooldown")
	}
	if m.dispatcher == nil {
		return errors.New("no dispatcher configured")
	}
	if err := m.dispatcher.Dispatch(ctx, s, payload); err != nil {
		return err
	}
	now := time.Now().UTC()
	if m.pool != nil {
		_, _ = m.pool.Exec(ctx, `
			UPDATE mem_sentinels
			   SET last_triggered_at = $2, fire_count = fire_count + 1
			 WHERE id = $1::uuid
		`, id, now)
	}
	m.mu.Lock()
	s.LastTriggeredAt = &now
	s.FireCount++
	m.sentinels[id] = s
	m.mu.Unlock()
	return nil
}

// Upsert creates or updates a sentinel. Returns the assigned ID.
func (m *Manager) Upsert(ctx context.Context, s Sentinel) (string, error) {
	if m == nil || m.pool == nil {
		return "", errors.New("manager unconfigured")
	}
	if !s.WatchType.Valid() {
		return "", fmt.Errorf("invalid watch_type %q", s.WatchType)
	}
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.WatchConfig == nil || len(s.WatchConfig) == 0 {
		s.WatchConfig = []byte("{}")
	}
	if s.ActionChain == nil || len(s.ActionChain) == 0 {
		s.ActionChain = []byte("[]")
	}
	_, err := m.pool.Exec(ctx, `
		INSERT INTO mem_sentinels (id, name, watch_type, watch_config, action_chain,
		                           cooldown_seconds, enabled)
		VALUES ($1::uuid, $2, $3, $4::jsonb, $5::jsonb, $6, $7)
		ON CONFLICT (name) DO UPDATE SET
		  watch_type = EXCLUDED.watch_type,
		  watch_config = EXCLUDED.watch_config,
		  action_chain = EXCLUDED.action_chain,
		  cooldown_seconds = EXCLUDED.cooldown_seconds,
		  enabled = EXCLUDED.enabled
	`, s.ID, s.Name, string(s.WatchType), s.WatchConfig, s.ActionChain,
		s.CooldownSeconds, s.Enabled)
	if err != nil {
		return "", err
	}
	return s.ID, m.Reload(ctx)
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	if m == nil || m.pool == nil {
		return nil
	}
	if _, err := m.pool.Exec(ctx, `DELETE FROM mem_sentinels WHERE id = $1::uuid`, id); err != nil {
		return err
	}
	return m.Reload(ctx)
}
