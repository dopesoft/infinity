package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// deploy_api.go — "is the running binary behind main?" detection.
//
// Railway injects RAILWAY_GIT_COMMIT_SHA at deploy time — that's the SHA the
// running binary was built from. We poll GitHub's main HEAD periodically
// and compare. The Studio Files panel surfaces the gap so Jarvis (and the
// boss) know when a fresher build is on its way.

type deployStatus struct {
	RunningSHA    string    `json:"running_sha"`
	LatestSHA     string    `json:"latest_sha"`
	Behind        bool      `json:"behind"`
	CommitsBehind int       `json:"commits_behind"`
	Branch        string    `json:"branch"`
	Repo          string    `json:"repo"`
	CheckedAt     time.Time `json:"checked_at"`
}

type deployTracker struct {
	mu     sync.RWMutex
	status deployStatus
	owner  string
	repo   string
	branch string
}

var globalDeployTracker = newDeployTracker()

func newDeployTracker() *deployTracker {
	owner := strings.TrimSpace(os.Getenv("INFINITY_REPO_OWNER"))
	if owner == "" {
		owner = "DopeSoft"
	}
	repo := strings.TrimSpace(os.Getenv("INFINITY_REPO_NAME"))
	if repo == "" {
		repo = "infinity"
	}
	branch := strings.TrimSpace(os.Getenv("INFINITY_REPO_BRANCH"))
	if branch == "" {
		branch = "main"
	}
	running := strings.TrimSpace(os.Getenv("RAILWAY_GIT_COMMIT_SHA"))
	return &deployTracker{
		owner:  owner,
		repo:   repo,
		branch: branch,
		status: deployStatus{
			RunningSHA: running,
			Repo:       owner + "/" + repo,
			Branch:     branch,
		},
	}
}

// snapshot returns a copy safe to serialise.
func (t *deployTracker) snapshot() deployStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.status
}

// refresh hits GitHub's REST API once and updates the cached status.
// No auth needed for public repos at 60 req/hr per IP — we poll every
// 5 min so we use 12/hr.
func (t *deployTracker) refresh(ctx context.Context) error {
	url := "https://api.github.com/repos/" + t.owner + "/" + t.repo + "/commits/" + t.branch
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "infinity-deploy-tracker")
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil // soft-fail: don't churn on rate limit / transient 5xx
	}
	var body struct {
		SHA    string `json:"sha"`
		Commit struct {
			Author struct {
				Date time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}
	if body.SHA == "" {
		return nil
	}

	// Compute commits_behind via the compare endpoint, but only when the
	// SHAs actually differ. Compare API returns `ahead_by` (commits on
	// branch ahead of running) which is what we want surfaced.
	var commitsBehind int
	t.mu.RLock()
	running := t.status.RunningSHA
	t.mu.RUnlock()
	if running != "" && running != body.SHA {
		commitsBehind = t.fetchCompareAheadBy(ctx, running, body.SHA)
	}

	t.mu.Lock()
	t.status.LatestSHA = body.SHA
	t.status.CheckedAt = time.Now().UTC()
	t.status.Behind = running != "" && running != body.SHA
	t.status.CommitsBehind = commitsBehind
	t.mu.Unlock()
	return nil
}

// fetchCompareAheadBy returns how many commits `head` is ahead of `base`.
// Returns 0 on any error or rate-limit — the "behind" boolean is still
// authoritative; this is just for the count.
func (t *deployTracker) fetchCompareAheadBy(ctx context.Context, base, head string) int {
	url := "https://api.github.com/repos/" + t.owner + "/" + t.repo + "/compare/" + base + "..." + head
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "infinity-deploy-tracker")
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	var body struct {
		AheadBy int `json:"ahead_by"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0
	}
	return body.AheadBy
}

// startDeployPoller fires an initial refresh then ticks every 5 min for
// the lifetime of ctx. Survives transient errors; never blocks the caller.
func startDeployPoller(ctx context.Context) {
	go func() {
		_ = globalDeployTracker.refresh(ctx)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = globalDeployTracker.refresh(ctx)
			}
		}
	}()
}

func (s *Server) handleDeployStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, globalDeployTracker.snapshot())
}
