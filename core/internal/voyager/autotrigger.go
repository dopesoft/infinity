package voyager

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// AutoTrigger watches mem_skill_runs for skills whose recent failure rate
// crosses a threshold, then auto-fires the GEPA optimizer for that skill.
// This is what closes the failure → curriculum → skill → optimization cycle
// Voyager originally promised but never had - RunOptimizer used to be HTTP
// only.
//
// Tunables (env):
//   • INFINITY_VOYAGER_AUTOTRIGGER       - off | on (default on when GEPA_URL set)
//   • INFINITY_VOYAGER_AUTOTRIGGER_EVERY - Go duration, default 30m
//   • INFINITY_VOYAGER_FAILURE_RATE      - float 0..1, default 0.3 (30%)
//   • INFINITY_VOYAGER_MIN_RUNS          - int, default 5 (window size)
//   • INFINITY_VOYAGER_COOLDOWN          - Go duration, default 6h per skill
//
// The ticker runs in a goroutine. Stop() cleans up. Concurrent fires for the
// same skill are guarded by the per-skill cooldown map.
type AutoTrigger struct {
	m         *Manager
	optimizer *Optimizer

	every       time.Duration
	failureRate float64
	minRuns     int
	cooldown    time.Duration

	mu       sync.Mutex
	stopCh   chan struct{}
	running  bool
	lastFire map[string]time.Time
}

func NewAutoTrigger(m *Manager, opt *Optimizer) *AutoTrigger {
	return &AutoTrigger{
		m:           m,
		optimizer:   opt,
		every:       envDuration("INFINITY_VOYAGER_AUTOTRIGGER_EVERY", 30*time.Minute),
		failureRate: envFloat("INFINITY_VOYAGER_FAILURE_RATE", 0.30),
		minRuns:     envInt("INFINITY_VOYAGER_MIN_RUNS", 5),
		cooldown:    envDuration("INFINITY_VOYAGER_COOLDOWN", 6*time.Hour),
		stopCh:      make(chan struct{}),
		lastFire:    map[string]time.Time{},
	}
}

// Enabled is true when both the optimizer is configured AND the user hasn't
// explicitly disabled autotrigger via env.
func (a *AutoTrigger) Enabled() bool {
	if a == nil || a.optimizer == nil || !a.optimizer.Enabled() {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(envValue("INFINITY_VOYAGER_AUTOTRIGGER")))
	if v == "0" || v == "false" || v == "no" || v == "off" {
		return false
	}
	return true
}

// Start begins the ticker loop. Safe to call once; subsequent calls no-op.
func (a *AutoTrigger) Start(ctx context.Context) {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return
	}
	a.running = true
	a.mu.Unlock()

	go a.loop(ctx)
}

func (a *AutoTrigger) Stop() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.running {
		return
	}
	close(a.stopCh)
	a.running = false
}

func (a *AutoTrigger) loop(ctx context.Context) {
	t := time.NewTicker(a.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-t.C:
			a.tick(ctx)
		}
	}
}

// tick scans for failing skills and fires the optimizer for any past the
// threshold (respecting per-skill cooldown).
func (a *AutoTrigger) tick(ctx context.Context) {
	if a == nil || a.m == nil || a.m.pool == nil {
		return
	}
	tctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	rows, err := a.m.pool.Query(tctx, `
		WITH recent AS (
			SELECT skill_name,
			       success,
			       ROW_NUMBER() OVER (PARTITION BY skill_name ORDER BY started_at DESC) AS rn
			  FROM mem_skill_runs
			 WHERE started_at > NOW() - INTERVAL '24 hours'
		)
		SELECT skill_name,
		       COUNT(*) FILTER (WHERE NOT success) AS fails,
		       COUNT(*) AS total
		  FROM recent
		 WHERE rn <= $1
		 GROUP BY skill_name
		HAVING COUNT(*) >= $1
	`, a.minRuns)
	if err != nil {
		fmt.Printf("[voyager.autotrigger] scan failed: %v\n", err)
		return
	}
	defer rows.Close()

	var targets []string
	for rows.Next() {
		var name string
		var fails, total int
		if err := rows.Scan(&name, &fails, &total); err != nil {
			continue
		}
		if total == 0 {
			continue
		}
		rate := float64(fails) / float64(total)
		if rate >= a.failureRate {
			targets = append(targets, name)
		}
	}
	if err := rows.Err(); err != nil {
		fmt.Printf("[voyager.autotrigger] iterate: %v\n", err)
	}

	for _, name := range targets {
		if !a.shouldFire(name) {
			continue
		}
		a.fire(ctx, name)
	}
}

func (a *AutoTrigger) shouldFire(skillName string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	last, ok := a.lastFire[skillName]
	if ok && time.Since(last) < a.cooldown {
		return false
	}
	a.lastFire[skillName] = time.Now()
	return true
}

func (a *AutoTrigger) fire(ctx context.Context, skillName string) {
	fctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	result, err := a.m.RunOptimizer(fctx, a.optimizer, skillName, 20)
	if err != nil {
		fmt.Printf("[voyager.autotrigger] optimize %s: %v\n", skillName, err)
		return
	}
	fmt.Printf("[voyager.autotrigger] fired %s - frontier_run=%s candidates=%d calls=%d\n",
		skillName, result.FrontierRunID, len(result.Candidates), result.Calls)
}
