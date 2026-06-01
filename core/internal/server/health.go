package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
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
}

// handleReadyz is the READINESS probe the external sentry watchdog polls. Unlike
// /health it actually pings Postgres, so a build that booted and serves HTTP but
// can't reach its database (a common "boots-but-broken" regression that /health
// would wave through) returns 503 here — giving the watchdog a real signal to
// roll core back. NOT wired to Railway's healthcheck on purpose (we don't want a
// DB outage to block deploys); it's a deeper signal for the off-box watcher.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	resp := readyResponse{
		Status:   "ok",
		Commit:   runningCommit(),
		DB:       "ok",
		UptimeMS: time.Since(s.started).Milliseconds(),
		Time:     time.Now().UTC().Format(time.RFC3339),
	}
	code := http.StatusOK
	if s.pool != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.pool.Ping(ctx); err != nil {
			resp.Status = "degraded"
			resp.DB = "error: " + err.Error()
			code = http.StatusServiceUnavailable
		}
	} else {
		resp.DB = "no pool"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(resp)
}
