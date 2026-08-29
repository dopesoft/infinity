package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

type healthResponse struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	Commit   string `json:"commit,omitempty"` // RAILWAY_GIT_COMMIT_SHA the binary was built from
	UptimeMS int64  `json:"uptime_ms"`
	Time     string `json:"time"`
}

// runningCommit is the git SHA the live binary was built from. Railway injects
// it at deploy time. Surfaced so the external sentry watchdog can reason about
// "is the latest push actually live?" without guessing.
func runningCommit() string {
	return strings.TrimSpace(os.Getenv("RAILWAY_GIT_COMMIT_SHA"))
}

// handleHealth is the LIVENESS probe — cheap, no dependencies, always 200 when
// the process is up. Railway's healthcheck hits this; keeping it dependency-free
// means a transient DB blip during a deploy doesn't fail the healthcheck and
// strand the new build. Deep checks live in /readyz.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	resp := healthResponse{
		Status:   "ok",
		Version:  s.cfg.Version,
		Commit:   runningCommit(),
		UptimeMS: time.Since(s.started).Milliseconds(),
		Time:     time.Now().UTC().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type readyResponse struct {
	Status   string `json:"status"` // ok | degraded
	Commit   string `json:"commit,omitempty"`
	DB       string `json:"db"` // ok | <error>
	UptimeMS int64  `json:"uptime_ms"`
	Time     string `json:"time"`
	// SchemaVersion is the newest migration THIS BINARY carries; Pending lists
	// the ones the live database has not recorded. Surfaced because "are the
	// migrations applied?" previously had no answer short of running the
	// migrator, and guessing at it stranded 011-014 in production for weeks.
	//
	// Drift does NOT make the service degraded. Applying migrations is the
	// boss's explicit act, and failing readiness on a migration he has not run
	// yet would hand the watchdog a reason to roll core back for something
	// that is not a regression. It is reported, loudly, and not acted on.
	SchemaVersion  string   `json:"schema_version,omitempty"`
	Pending        []string `json:"pending_migrations,omitempty"`
	MigrationState string   `json:"migrations"` // current | N pending | unknown: <why>
}

// handleReadyz is the READINESS probe the external sentry watchdog polls. Unlike
// /health it actually pings Postgres, so a build that booted and serves HTTP but
// can't reach its database (a common "boots-but-broken" regression that /health
// would wave through) returns 503 here — giving the watchdog a real signal to
// roll core back. NOT wired to Railway's healthcheck on purpose (we don't want a
// DB outage to block deploys); it's a deeper signal for the off-box watcher.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	resp := readyResponse{
		Status:         "ok",
		Commit:         runningCommit(),
		DB:             "ok",
		UptimeMS:       time.Since(s.started).Milliseconds(),
		Time:           time.Now().UTC().Format(time.RFC3339),
		SchemaVersion:  db.SchemaVersion(),
		MigrationState: "unknown: no database",
	}
	code := http.StatusOK
	if s.pool != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.pool.Ping(ctx); err != nil {
			resp.Status = "degraded"
			resp.DB = "error: " + err.Error()
			resp.MigrationState = "unknown: database unreachable"
			code = http.StatusServiceUnavailable
		} else {
			resp.Pending, resp.MigrationState = migrationState(ctx, s.pool)
		}
	} else {
		resp.DB = "no pool"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(resp)
}

// migrationState reports which embedded migrations the live database has not
// recorded. "unknown" is a distinct answer from "current": a probe that could
// not run must never read as a schema that is up to date, which is the exact
// confusion that let 011-014 sit unapplied for weeks while everything looked
// fine (CLAUDE.md → "Migrations").
func migrationState(ctx context.Context, pool *pgxpool.Pool) ([]string, string) {
	pending, err := db.Pending(ctx, pool)
	if err != nil {
		return nil, "unknown: " + err.Error()
	}
	if len(pending) == 0 {
		return nil, "current"
	}
	return pending, fmt.Sprintf("%d pending — run `infinity migrate`", len(pending))
}
