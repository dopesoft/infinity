package sentinel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

type runtimeState struct {
	LastKey string
	LastAt  time.Time
}

type fileConfig struct {
	Path            string `json:"path"`
	IntervalSeconds int    `json:"interval_seconds"`
}

type memoryConfig struct {
	HookName        string `json:"hook_name"`
	Query           string `json:"query"`
	MinImportance   int    `json:"min_importance"`
	SinceSeconds    int    `json:"since_seconds"`
	IntervalSeconds int    `json:"interval_seconds"`
}

type pollConfig struct {
	URL             string            `json:"url"`
	Method          string            `json:"method"`
	Headers         map[string]string `json:"headers"`
	Body            string            `json:"body"`
	Contains        string            `json:"contains"`
	Equals          string            `json:"equals"`
	FireOnChange    bool              `json:"fire_on_change"`
	IntervalSeconds int               `json:"interval_seconds"`
}

type thresholdConfig struct {
	SQL             string  `json:"sql"`
	Operator        string  `json:"operator"`
	Value           float64 `json:"value"`
	IntervalSeconds int     `json:"interval_seconds"`
}

type goalEventConfig struct {
	// Optional - empty = watch every goal.
	GoalID string `json:"goal_id"`
	// Optional - empty = watch every entity. When set, narrows to goals
	// that point at this world-model entity.
	EntityID string `json:"entity_id"`
	// Optional - fire only when the goal's current status is in this list.
	// Empty = fire on any change.
	OnStatusIn      []string `json:"on_status_in"`
	IntervalSeconds int      `json:"interval_seconds"`
}

// Start launches the non-webhook watcher runtimes. Webhooks still enter via
// Trigger directly; every other watch type polls cheaply and reuses Trigger so
// cooldowns, action chains, and fire_count stay in one place.
func (m *Manager) Start(ctx context.Context) {
	if m == nil || m.pool == nil {
		return
	}
	go m.runtimeLoop(ctx)
}

func (m *Manager) runtimeLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	state := m.loadRuntimeState(ctx)
	var mu sync.Mutex
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, s := range m.List() {
				if !s.Enabled || s.WatchType == WatchWebhook {
					continue
				}
				mu.Lock()
				st := state[s.ID]
				mu.Unlock()
				next, payload, err := m.checkRuntime(ctx, s, st)
				if err != nil {
					// A swallowed check error is a sentinel going silently blind:
					// a dropped DB query / renamed column / broken poll looks
					// identical to "nothing matched", so the watch stops firing
					// and nobody is told. Log it loud (stderr → Railway error) so
					// a broken watch is visible instead of just dormant.
					fmt.Fprintf(os.Stderr, "sentinel %q (%s) check failed: %v\n", s.Name, s.WatchType, err)
				} else if payload != nil {
					_ = m.Trigger(ctx, s.ID, payload)
				}
				if next.LastKey != st.LastKey || !next.LastAt.Equal(st.LastAt) {
					mu.Lock()
					state[s.ID] = next
					mu.Unlock()
					// Persist the baseline only when the KEY moves (LastAt
					// ticks every due poll; writing it would hammer the DB
					// for nothing). The key is what makes fire_on_change /
					// file_change survive a restart without re-baselining.
					if next.LastKey != st.LastKey {
						m.persistRuntimeState(ctx, s.ID, next.LastKey)
					}
				}
			}
		}
	}
}

// loadRuntimeState hydrates the poll baselines persisted by
// persistRuntimeState so fire_on_change / file_change sentinels keep their
// "last seen" key across restarts. Before migration 167 the baseline lived
// only in this process's map, so every deploy silently re-baselined and a
// change that happened while the process was down never fired.
func (m *Manager) loadRuntimeState(ctx context.Context) map[string]runtimeState {
	state := map[string]runtimeState{}
	rows, err := m.pool.Query(ctx, `
		SELECT id, COALESCE(runtime_state->>'last_key', '')
		FROM mem_sentinels
		WHERE runtime_state <> '{}'::jsonb
	`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sentinel: load runtime state: %v\n", err)
		return state
	}
	defer rows.Close()
	for rows.Next() {
		var id, lastKey string
		if err := rows.Scan(&id, &lastKey); err != nil {
			continue
		}
		if lastKey != "" {
			state[id] = runtimeState{LastKey: lastKey}
		}
	}
	return state
}

// persistRuntimeState writes a sentinel's poll baseline through to
// mem_sentinels.runtime_state. Best-effort: a failed write costs one
// restart's worth of baseline, and the loud log keeps it from failing
// silently forever.
func (m *Manager) persistRuntimeState(ctx context.Context, id, lastKey string) {
	payload, _ := json.Marshal(map[string]string{"last_key": lastKey})
	if _, err := m.pool.Exec(ctx, `
		UPDATE mem_sentinels SET runtime_state = $2::jsonb WHERE id = $1
	`, id, string(payload)); err != nil {
		fmt.Fprintf(os.Stderr, "sentinel %s: persist runtime state: %v\n", id, err)
	}
}

func (m *Manager) checkRuntime(ctx context.Context, s Sentinel, st runtimeState) (runtimeState, map[string]any, error) {
	if !due(st, intervalFor(s)) {
		return st, nil, nil
	}
	st.LastAt = time.Now().UTC()
	switch s.WatchType {
	case WatchFileChange:
		return m.checkFileChange(s, st)
	case WatchMemoryEvent:
		return m.checkMemoryEvent(ctx, s, st)
	case WatchExternalPoll:
		return m.checkExternalPoll(ctx, s, st)
	case WatchThreshold:
		return m.checkThreshold(ctx, s, st)
	case WatchGoalEvent:
		return m.checkGoalEvent(ctx, s, st)
	default:
		return st, nil, nil
	}
}

func intervalFor(s Sentinel) time.Duration {
	var raw map[string]any
	_ = json.Unmarshal(s.WatchConfig, &raw)
	if v, ok := raw["interval_seconds"].(float64); ok && v > 0 {
		return time.Duration(v) * time.Second
	}
	return time.Minute
}

func due(st runtimeState, every time.Duration) bool {
	if st.LastAt.IsZero() {
		return true
	}
	return time.Since(st.LastAt) >= every
}

func (m *Manager) checkFileChange(s Sentinel, st runtimeState) (runtimeState, map[string]any, error) {
	var cfg fileConfig
	if err := json.Unmarshal(s.WatchConfig, &cfg); err != nil {
		return st, nil, err
	}
	if strings.TrimSpace(cfg.Path) == "" {
		return st, nil, errors.New("file_change: path required")
	}
	info, err := os.Stat(cfg.Path)
	if err != nil {
		return st, nil, err
	}
	key := fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
	if st.LastKey == "" {
		st.LastKey = key
		return st, nil, nil
	}
	if st.LastKey == key {
		return st, nil, nil
	}
	st.LastKey = key
	return st, map[string]any{
		"watch_type": "file_change",
		"path":       cfg.Path,
		"size":       info.Size(),
		"mod_time":   info.ModTime().UTC(),
	}, nil
}

func (m *Manager) checkMemoryEvent(ctx context.Context, s Sentinel, st runtimeState) (runtimeState, map[string]any, error) {
	var cfg memoryConfig
	if err := json.Unmarshal(s.WatchConfig, &cfg); err != nil {
		return st, nil, err
	}
	if cfg.SinceSeconds <= 0 {
		cfg.SinceSeconds = 300
	}
	var id, hook, raw string
	var importance int
	err := m.pool.QueryRow(ctx, `
		SELECT id::text, hook_name, COALESCE(raw_text,''), importance
		  FROM mem_observations
		 WHERE created_at >= NOW() - make_interval(secs => $1)
		   AND ($2 = '' OR hook_name = $2)
		   AND ($3 = '' OR raw_text ILIKE '%' || $3 || '%')
		   AND importance >= $4
		 ORDER BY created_at DESC
		 LIMIT 1
	`, cfg.SinceSeconds, strings.TrimSpace(cfg.HookName), strings.TrimSpace(cfg.Query), cfg.MinImportance).Scan(&id, &hook, &raw, &importance)
	if err != nil {
		// No matching observation is the legitimate empty result; ANY other
		// error (dropped connection, renamed column, pool exhausted) is a real
		// failure that must NOT masquerade as "no trigger" — propagate it so the
		// runtime loop logs the watch as broken instead of silently dormant.
		if errors.Is(err, pgx.ErrNoRows) {
			return st, nil, nil
		}
		return st, nil, err
	}
	if id == "" || id == st.LastKey {
		return st, nil, nil
	}
	st.LastKey = id
	return st, map[string]any{
		"watch_type":     "memory_event",
		"observation_id": id,
		"hook_name":      hook,
		"raw_text":       raw,
		"importance":     importance,
	}, nil
}

func (m *Manager) checkExternalPoll(ctx context.Context, s Sentinel, st runtimeState) (runtimeState, map[string]any, error) {
	var cfg pollConfig
	if err := json.Unmarshal(s.WatchConfig, &cfg); err != nil {
		return st, nil, err
	}
	if strings.TrimSpace(cfg.URL) == "" {
		return st, nil, errors.New("external_api_poll: url required")
	}
	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, cfg.URL, bytes.NewBufferString(cfg.Body))
	if err != nil {
		return st, nil, err
	}
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return st, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	text := string(body)
	key := strconv.Itoa(resp.StatusCode) + ":" + text
	matched := false
	switch {
	case cfg.Equals != "":
		matched = strings.TrimSpace(text) == cfg.Equals
	case cfg.Contains != "":
		matched = strings.Contains(text, cfg.Contains)
	case cfg.FireOnChange:
		matched = st.LastKey != "" && st.LastKey != key
	default:
		matched = resp.StatusCode >= 400
	}
	st.LastKey = key
	if !matched {
		return st, nil, nil
	}
	return st, map[string]any{
		"watch_type":  "external_api_poll",
		"url":         cfg.URL,
		"status_code": resp.StatusCode,
		"body":        clip(text, 4000),
	}, nil
}

func (m *Manager) checkThreshold(ctx context.Context, s Sentinel, st runtimeState) (runtimeState, map[string]any, error) {
	var cfg thresholdConfig
	if err := json.Unmarshal(s.WatchConfig, &cfg); err != nil {
		return st, nil, err
	}
	sql := strings.TrimSpace(cfg.SQL)
	if !strings.HasPrefix(strings.ToLower(sql), "select ") || strings.Contains(sql, ";") {
		return st, nil, errors.New("threshold: sql must be a single SELECT")
	}
	var actual float64
	if err := m.pool.QueryRow(ctx, sql).Scan(&actual); err != nil {
		return st, nil, err
	}
	if !compare(actual, cfg.Operator, cfg.Value) {
		st.LastKey = fmt.Sprintf("%f", actual)
		return st, nil, nil
	}
	key := fmt.Sprintf("%s:%f:%s:%f", sql, actual, cfg.Operator, cfg.Value)
	if st.LastKey == key {
		return st, nil, nil
	}
	st.LastKey = key
	return st, map[string]any{
		"watch_type": "threshold",
		"sql":        sql,
		"actual":     actual,
		"operator":   cfg.Operator,
		"value":      cfg.Value,
	}, nil
}

func compare(actual float64, op string, expected float64) bool {
	switch strings.TrimSpace(op) {
	case ">", "gt":
		return actual > expected
	case ">=", "gte", "":
		return actual >= expected
	case "<", "lt":
		return actual < expected
	case "<=", "lte":
		return actual <= expected
	case "=", "==", "eq":
		return actual == expected
	case "!=", "<>", "ne":
		return actual != expected
	default:
		return false
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (m *Manager) checkGoalEvent(ctx context.Context, s Sentinel, st runtimeState) (runtimeState, map[string]any, error) {
	var cfg goalEventConfig
	if err := json.Unmarshal(s.WatchConfig, &cfg); err != nil {
		return st, nil, err
	}
	// LastKey holds the most recent updated_at we've seen for this sentinel
	// in RFC3339Nano form. First tick stamps the cursor without firing.
	cursor := time.Time{}
	if st.LastKey != "" {
		if t, err := time.Parse(time.RFC3339Nano, st.LastKey); err == nil {
			cursor = t
		}
	}
	// Pull the latest row matching the filter that has been updated AFTER
	// the cursor. One row is enough - the next tick will catch the next one.
	var (
		id, title, status, blocker string
		updatedAt                  time.Time
	)
	err := m.pool.QueryRow(ctx, `
		SELECT id::text, title, status, blocker, updated_at
		  FROM mem_agent_goals
		 WHERE updated_at > $1
		   AND ($2 = '' OR id::text = $2)
		   AND ($3 = '' OR entity_id::text = $3)
		 ORDER BY updated_at ASC
		 LIMIT 1
	`, cursor, strings.TrimSpace(cfg.GoalID), strings.TrimSpace(cfg.EntityID)).Scan(&id, &title, &status, &blocker, &updatedAt)
	if err != nil {
		// pgx.ErrNoRows or schema not applied. First tick still needs to
		// stamp the cursor so we don't refire on history.
		if cursor.IsZero() {
			st.LastKey = time.Now().UTC().Format(time.RFC3339Nano)
		}
		return st, nil, nil
	}
	st.LastKey = updatedAt.Format(time.RFC3339Nano)
	if len(cfg.OnStatusIn) > 0 && !containsStr(cfg.OnStatusIn, status) {
		return st, nil, nil
	}
	return st, map[string]any{
		"watch_type": "goal_event",
		"goal_id":    id,
		"title":      title,
		"status":     status,
		"blocker":    blocker,
		"updated_at": updatedAt.UTC(),
	}, nil
}

func containsStr(set []string, want string) bool {
	for _, s := range set {
		if strings.EqualFold(strings.TrimSpace(s), want) {
			return true
		}
	}
	return false
}
