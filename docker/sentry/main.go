// sentry — Infinity's EXTERNAL deploy watchdog.
//
// Why this exists: the in-core `post-deploy-verify` cron is a self-referential
// watchdog — if a self-improve push bricks core badly enough, the very process
// meant to detect + revert it is the one that's down. sentry closes that blind
// spot. It is a separate Railway service with ZERO dependency on core's code
// (its own go.mod, stdlib only), so no core change can break it. It watches
// core from the outside and, on a sustained OUTAGE, heals autonomously.
//
// What it does every poll (default 60s):
//  1. GET core's /readyz (deep: process up AND Postgres reachable + running SHA).
//  2. Classify: healthy (200) | db_degraded (503, process up but DB down) |
//     down (unreachable / transport error / process-level 5xx).
//  3. On `down` sustained for SENTRY_TRIP_AFTER (default 5m) → SELF-HEAL:
//     a. Railway rollback to the previous good deployment (instant), best-effort.
//     b. git revert the commits since the last-known-healthy SHA + push
//     (authoritative — fixes main so the next deploy doesn't re-break).
//     c. notify the boss (webhook + loud stderr).
//     One action per incident, then it waits for recovery before re-arming;
//     if still down after acting, it escalates ONCE and stops (no revert loop).
//
// Safety: it does NOT revert on `db_degraded` — that's infra, not a code
// regression, and reverting good code wouldn't fix it (notify only). It only
// reverts when it knows a last-healthy SHA to return to. Everything is gated by
// env so unconfigured pieces degrade to notify-only.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ── config ──────────────────────────────────────────────────────────────────

type config struct {
	port        string
	readyURL    string
	poll        time.Duration
	tripAfter   time.Duration
	bootGrace   time.Duration
	alertHook   string
	githubToken string
	repoOwner   string
	repoName    string
	repoBranch  string
	gitUser     string
	gitEmail    string

	// Railway API (optional — instant rollback). Without a token, recovery is
	// git-revert only (slower but authoritative). Two token types are supported:
	//   • RAILWAY_PROJECT_TOKEN  — scoped to this project+env (RECOMMENDED, least
	//     privilege). Sent as the `Project-Access-Token` header.
	//   • RAILWAY_API_TOKEN      — account/workspace token. Sent as `Authorization: Bearer`.
	railwayToken     string
	railwayProjToken string
	railwayProject   string
	railwaySvc       string
	railwayEnv       string
}

// railwayConfigured reports whether instant Railway rollback can run: a token
// (either type) plus the three IDs the deployments query needs.
func (c config) railwayConfigured() bool {
	return (c.railwayToken != "" || c.railwayProjToken != "") &&
		c.railwayProject != "" && c.railwaySvc != "" && c.railwayEnv != ""
}

func loadConfig() config {
	return config{
		port:        envDefault("PORT", "8080"),
		readyURL:    envDefault("SENTRY_CORE_READY_URL", "http://core.railway.internal:8080/readyz"),
		poll:        envDuration("SENTRY_POLL_INTERVAL", 60*time.Second),
		tripAfter:   envDuration("SENTRY_TRIP_AFTER", 5*time.Minute),
		bootGrace:   envDuration("SENTRY_BOOT_GRACE", 2*time.Minute),
		alertHook:   strings.TrimSpace(os.Getenv("SENTRY_ALERT_WEBHOOK")),
		githubToken: strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
		repoOwner:   envDefault("INFINITY_REPO_OWNER", "DopeSoft"),
		repoName:    envDefault("INFINITY_REPO_NAME", "infinity"),
		repoBranch:  envDefault("INFINITY_REPO_BRANCH", "main"),
		gitUser:     envDefault("GIT_USER_NAME", "Jarvis Sentry"),
		gitEmail:    envDefault("GIT_USER_EMAIL", "jarvis@dopesoft.io"),

		railwayToken:     strings.TrimSpace(os.Getenv("RAILWAY_API_TOKEN")),
		railwayProjToken: strings.TrimSpace(os.Getenv("RAILWAY_PROJECT_TOKEN")),
		railwayProject:   strings.TrimSpace(os.Getenv("RAILWAY_PROJECT_ID")),
		railwaySvc:       strings.TrimSpace(os.Getenv("RAILWAY_CORE_SERVICE_ID")),
		railwayEnv:       strings.TrimSpace(os.Getenv("RAILWAY_ENVIRONMENT_ID")),
	}
}

// infoLog → stdout (Railway tags as info); errors use the default logger (stderr).
var infoLog = log.New(os.Stdout, "", log.LstdFlags)

func main() {
	cfg := loadConfig()
	configureGit(cfg)
	go serveHealth(cfg.port)

	infoLog.Printf("sentry: watching %s every %s, trip after %s (rollback=%v, git-revert=%v)",
		cfg.readyURL, cfg.poll, cfg.tripAfter, cfg.railwayConfigured(), cfg.githubToken != "")

	w := &watchdog{cfg: cfg, bootAt: time.Now()}
	w.run(context.Background())
}

// ── watchdog ─────────────────────────────────────────────────────────────────

type healthState string

const (
	stHealthy  healthState = "healthy"
	stDBDegrad healthState = "db_degraded"
	stDown     healthState = "down"
)

type watchdog struct {
	cfg    config
	bootAt time.Time

	lastHealthyAt  time.Time
	lastHealthySHA string
	downSince      time.Time // zero when not currently down
	acted          bool      // acted on the current incident
	escalated      bool      // escalated on the current incident
}

func (w *watchdog) run(ctx context.Context) {
	t := time.NewTicker(w.cfg.poll)
	defer t.Stop()
	// First probe immediately so we capture a baseline healthy SHA fast.
	w.tick()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick()
		}
	}
}

func (w *watchdog) tick() {
	state, commit := w.probe()
	now := time.Now()

	switch state {
	case stHealthy:
		w.lastHealthyAt = now
		if commit != "" {
			w.lastHealthySHA = commit
		}
		if w.acted || !w.downSince.IsZero() {
			infoLog.Printf("sentry: core recovered (commit=%s)", short(commit))
			w.notify("recovered", fmt.Sprintf("core is healthy again (commit %s)", short(commit)))
		}
		w.downSince, w.acted, w.escalated = time.Time{}, false, false

	case stDBDegrad:
		// Process is up; Postgres is unreachable. This is infra, NOT a code
		// regression — reverting good code wouldn't fix it. Notify only, once.
		if w.downSince.IsZero() {
			w.downSince = now
		}
		if now.Sub(w.downSince) >= w.cfg.tripAfter && !w.escalated {
			log.Printf("sentry: core /readyz DB-degraded for %s — infra issue, NOT reverting", w.cfg.tripAfter)
			w.notify("db_degraded", "core is up but its database is unreachable for "+w.cfg.tripAfter.String()+". This looks like infrastructure, not a bad deploy — sentry will NOT revert. Check the DB.")
			w.escalated = true
		}

	case stDown:
		if now.Sub(w.bootAt) < w.cfg.bootGrace {
			return // settle window after sentry's own boot
		}
		if w.downSince.IsZero() {
			w.downSince = now
			log.Printf("sentry: core unreachable — arming (will act in %s if it stays down)", w.cfg.tripAfter)
			return
		}
		downFor := now.Sub(w.downSince)
		if !w.acted && downFor >= w.cfg.tripAfter {
			w.selfHeal(downFor)
			w.acted = true
			return
		}
		// Acted already; if still down well past, escalate once and stop.
		if w.acted && !w.escalated && downFor >= 2*w.cfg.tripAfter {
			log.Printf("sentry: core STILL down %s after self-heal — escalating, no further auto-action", downFor)
			w.notify("escalate", "sentry self-healed but core is STILL down after "+downFor.Round(time.Minute).String()+". Manual intervention needed.")
			w.escalated = true
		}
	}
}

// probe hits /readyz and classifies the result. Transport errors / non-JSON /
// process-level 5xx → down. A clean 503 with a DB error body → db_degraded.
func (w *watchdog) probe() (healthState, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.cfg.readyURL, nil)
	if err != nil {
		return stDown, ""
	}
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		return stDown, "" // unreachable: connection refused, DNS, timeout
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	var parsed struct {
		Status string `json:"status"`
		Commit string `json:"commit"`
		DB     string `json:"db"`
	}
	_ = json.Unmarshal(body, &parsed)

	if resp.StatusCode == http.StatusOK {
		return stHealthy, parsed.Commit
	}
	// A structured 503 from /readyz with a DB error = process alive, DB down.
	if resp.StatusCode == http.StatusServiceUnavailable && strings.HasPrefix(parsed.DB, "error") {
		return stDBDegrad, parsed.Commit
	}
	// Any other 4xx (incl. 404 from a core build that predates /readyz) means
	// the HTTP server is up and routing — core is ALIVE, just doesn't have this
	// endpoint. Never self-heal a responding server. Treat as healthy liveness.
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return stHealthy, parsed.Commit
	}
	// 5xx without our DB shape, empty, or garbage = process genuinely broken.
	return stDown, parsed.Commit
}

// selfHeal: instant Railway rollback (best-effort) + authoritative git-revert + notify.
func (w *watchdog) selfHeal(downFor time.Duration) {
	log.Printf("sentry: SELF-HEAL — core down for %s, last-healthy commit=%s", downFor.Round(time.Second), short(w.lastHealthySHA))

	rollback := w.railwayRollback()

	revert := "skipped"
	if w.cfg.githubToken == "" {
		revert = "skipped (no GITHUB_TOKEN)"
	} else if w.lastHealthySHA == "" {
		revert = "skipped (no known-good SHA yet)"
	} else if err := w.gitRevertToGood(); err != nil {
		revert = "FAILED: " + err.Error()
		log.Printf("sentry: git revert failed: %v", err)
	} else {
		revert = "reverted main to " + short(w.lastHealthySHA) + " and pushed"
	}

	msg := fmt.Sprintf("🚨 sentry self-heal fired: core unreachable %s.\n• railway rollback: %s\n• git: %s",
		downFor.Round(time.Minute), rollback, revert)
	log.Print("sentry: " + strings.ReplaceAll(msg, "\n", " "))
	w.notify("self_heal", msg)
}

// gitRevertToGood clones the repo, reverts every commit after the last-healthy
// SHA on the branch, and pushes. Linear-history assumption; on any error it
// bails without pushing a partial state (notify handles escalation).
func (w *watchdog) gitRevertToGood() error {
	dir, err := os.MkdirTemp("", "sentry-revert-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	repoURL := fmt.Sprintf("https://github.com/%s/%s.git", w.cfg.repoOwner, w.cfg.repoName)
	repoDir := filepath.Join(dir, "repo")
	if out, err := w.git(dir, "clone", "--branch", w.cfg.repoBranch, repoURL, repoDir); err != nil {
		return fmt.Errorf("clone: %v (%s)", err, out)
	}

	// Are there commits to revert? range = <good>..HEAD
	rangeSpec := w.lastHealthySHA + "..HEAD"
	out, err := w.git(repoDir, "rev-list", "--count", rangeSpec)
	if err != nil {
		return fmt.Errorf("rev-list: %v (%s)", err, out)
	}
	if strings.TrimSpace(out) == "0" {
		return fmt.Errorf("HEAD is already at the last-healthy commit — outage is not from a recent push (infra?); not reverting")
	}

	if out, err := w.git(repoDir, "revert", "--no-edit", rangeSpec); err != nil {
		return fmt.Errorf("revert %s: %v (%s)", rangeSpec, err, out)
	}
	if out, err := w.git(repoDir, "push", "origin", w.cfg.repoBranch); err != nil {
		return fmt.Errorf("push: %v (%s)", err, out)
	}
	return nil
}

func (w *watchdog) git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// ── Railway rollback (best-effort, optional) ─────────────────────────────────

// railwayRollback finds the previous successful, rollback-able deployment for
// the core service and rolls back to it for instant recovery. Returns a status
// string for the notification. Any failure is non-fatal: git-revert is the
// authoritative recovery; this just shortens downtime. Verified API shape:
// backboard.railway.com/graphql/v2 · deployments(input,first) · deploymentRollback(id).
func (w *watchdog) railwayRollback() string {
	if !w.cfg.railwayConfigured() {
		return "skipped (Railway API not configured)"
	}
	id, err := w.railwayPreviousGoodDeployment()
	if err != nil {
		return "skipped (" + err.Error() + ")"
	}
	if id == "" {
		return "skipped (no rollback-able previous deployment found)"
	}
	q := map[string]any{
		"query":     "mutation($id: String!) { deploymentRollback(id: $id) }",
		"variables": map[string]any{"id": id},
	}
	if _, err := w.railwayGQL(q); err != nil {
		return "FAILED: " + err.Error()
	}
	return "rolled back to deployment " + short(id)
}

func (w *watchdog) railwayPreviousGoodDeployment() (string, error) {
	q := map[string]any{
		"query": `query($input: DeploymentListInput!, $first: Int) {
			deployments(input: $input, first: $first) {
				edges { node { id status canRollback createdAt } }
			}
		}`,
		// No status filter: DeploymentStatusInput is {in,notIn} of status enums,
		// and older good deployments show as REMOVED (superseded) — filtering to
		// SUCCESS-only would hide every valid rollback target. We fetch recent and
		// pick by canRollback in Go instead.
		"variables": map[string]any{
			"first": 10,
			"input": map[string]any{
				"projectId":     w.cfg.railwayProject,
				"serviceId":     w.cfg.railwaySvc,
				"environmentId": w.cfg.railwayEnv,
			},
		},
	}
	raw, err := w.railwayGQL(q)
	if err != nil {
		return "", err
	}
	var resp struct {
		Data struct {
			Deployments struct {
				Edges []struct {
					Node struct {
						ID          string `json:"id"`
						Status      string `json:"status"`
						CanRollback bool   `json:"canRollback"`
					} `json:"node"`
				} `json:"edges"`
			} `json:"deployments"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("decode deployments: %v", err)
	}
	// Edges are newest-first. The newest successful deployment is the current
	// (now-broken-at-runtime) one; pick the next rollback-able one.
	skippedCurrent := false
	for _, e := range resp.Data.Deployments.Edges {
		if !skippedCurrent {
			skippedCurrent = true
			continue
		}
		if e.Node.CanRollback {
			return e.Node.ID, nil
		}
	}
	return "", nil
}

func (w *watchdog) railwayGQL(payload map[string]any) ([]byte, error) {
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://backboard.railway.com/graphql/v2", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Project tokens (scoped, recommended) use a dedicated header; account/
	// workspace tokens use Bearer. Prefer the project token when both are set.
	if w.cfg.railwayProjToken != "" {
		req.Header.Set("Project-Access-Token", w.cfg.railwayProjToken)
	} else {
		req.Header.Set("Authorization", "Bearer "+w.cfg.railwayToken)
	}
	resp, err := (&http.Client{Timeout: 18 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("railway %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	if bytes.Contains(raw, []byte(`"errors"`)) {
		return nil, fmt.Errorf("railway graphql error: %s", truncate(string(raw), 300))
	}
	return raw, nil
}

// ── notify ────────────────────────────────────────────────────────────────────

func (w *watchdog) notify(kind, message string) {
	if w.cfg.alertHook == "" {
		return // stderr log already carries it
	}
	body, _ := json.Marshal(map[string]any{
		"source":  "sentry",
		"kind":    kind,
		"message": message,
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.cfg.alertHook, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		log.Printf("sentry: alert webhook failed: %v", err)
		return
	}
	resp.Body.Close()
}

// ── plumbing ──────────────────────────────────────────────────────────────────

// serveHealth gives Railway something to healthcheck on the sentry service itself.
func serveHealth(port string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"sentry"}`))
	})
	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("sentry: health server: %v", err)
	}
}

func configureGit(cfg config) {
	_ = exec.Command("git", "config", "--global", "user.name", cfg.gitUser).Run()
	_ = exec.Command("git", "config", "--global", "user.email", cfg.gitEmail).Run()
	_ = exec.Command("git", "config", "--global", "safe.directory", "*").Run()
	if cfg.githubToken != "" {
		helper := fmt.Sprintf(`!f() { echo "username=oauth2"; echo "password=%s"; }; f`, cfg.githubToken)
		_ = exec.Command("git", "config", "--global", "credential.https://github.com.helper", helper).Run()
	}
}

func envDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
