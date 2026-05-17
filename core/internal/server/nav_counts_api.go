package server

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Nav counts API. Powers the numeric badges on every top-level tab in
// Studio so the boss can see at a glance where unresolved work lives
// instead of hunting through pages. One round-trip on app mount; the
// useNavCounts hook subscribes to WS events for realtime invalidation
// and polls every 30s as a fallback.
//
// Counts are intentionally cheap (one SELECT COUNT per source). When
// the pool is unavailable everything returns zero so the UI renders
// without badges rather than erroring.

type navOverflowCounts struct {
	Lab       int `json:"lab"`        // open curiosity + heartbeat findings + code proposals
	Heartbeat int `json:"heartbeat"`  // open heartbeat findings (subset of Lab; used for the per-tab pill inside the overflow menu)
	Logs      int `json:"logs"`       // recent error-status turns
}

type navCounts struct {
	Dashboard int               `json:"dashboard"` // approvals + unread follow-ups + agent work items pending
	Chat      int               `json:"chat"`      // trust contracts awaiting approval
	Memory    int               `json:"memory"`    // always 0; browse-only
	Skills    int               `json:"skills"`    // candidate skill proposals
	Overflow  navOverflowCounts `json:"overflow"`
}

func (s *Server) handleNavCounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	c := navCounts{}
	if s.cfg.Pool != nil {
		ctx := r.Context()
		c.Dashboard = countDashboardNeedsYou(ctx, s.cfg.Pool)
		c.Chat = countTrustPending(ctx, s.cfg.Pool)
		c.Skills = countSkillCandidates(ctx, s.cfg.Pool)
		c.Overflow.Heartbeat = countOpen(ctx, s.cfg.Pool, "mem_heartbeat_findings")
		c.Overflow.Lab = countLabOpenIssues(ctx, s.cfg.Pool)
		c.Overflow.Logs = countLogsErrors(ctx, s.cfg.Pool)
	}
	writeJSON(w, http.StatusOK, c)
}

func countDashboardNeedsYou(ctx context.Context, pool *pgxpool.Pool) int {
	// Boss-actionable surfaces: pending trust contracts (they always
	// need attention) + unread follow-ups (if the table exists). Kept
	// permissive: if a sub-query errors we ignore it rather than 500
	// the whole nav.
	var trust, followups int
	_ = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mem_trust_contracts WHERE status = 'pending'
	`).Scan(&trust)
	_ = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mem_followups WHERE COALESCE(status,'open') = 'open'
	`).Scan(&followups)
	return trust + followups
}

func countTrustPending(ctx context.Context, pool *pgxpool.Pool) int {
	var n int
	_ = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mem_trust_contracts WHERE status = 'pending'
	`).Scan(&n)
	return n
}

func countSkillCandidates(ctx context.Context, pool *pgxpool.Pool) int {
	var n int
	_ = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mem_skill_proposals WHERE status = 'candidate'
	`).Scan(&n)
	return n
}

func countLabOpenIssues(ctx context.Context, pool *pgxpool.Pool) int {
	// Sum of open curiosity questions, open heartbeat findings, and
	// pending code proposals - the three sources Lab Fix-this surfaces.
	var q, f, c int
	_ = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mem_curiosity_questions WHERE status = 'open'
	`).Scan(&q)
	_ = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mem_heartbeat_findings WHERE status = 'open'
	`).Scan(&f)
	_ = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mem_code_proposals WHERE status = 'pending'
	`).Scan(&c)
	return q + f + c
}

func countLogsErrors(ctx context.Context, pool *pgxpool.Pool) int {
	// Recent failing turns. mem_turn_traces.status = 'errored' is the
	// canonical signal /logs surfaces. Kept narrow to the last 24h so
	// the badge doesn't get pinned forever by a long-ago failure.
	var n int
	_ = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mem_turn_traces
		 WHERE COALESCE(status,'') = 'errored'
		   AND created_at > NOW() - INTERVAL '24 hours'
	`).Scan(&n)
	return n
}

func countOpen(ctx context.Context, pool *pgxpool.Pool, table string) int {
	var n int
	// SAFETY: only call with table names that are compile-time
	// constants in this file. Don't accept table names from request
	// input.
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM `+table+` WHERE status = 'open'`).Scan(&n)
	return n
}
