package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/dopesoft/infinity/core/internal/bridge"
)

// GET /api/bridge/workspace/git-status
//
// "Is the cloud workspace's checkout behind main on GitHub?" - same
// shape question the deploy-status endpoint answers for Core's binary,
// but pointed at the cloud workspace's working tree. Studio uses this
// to render a staleness banner on the Files column when the Cloud
// bridge is active and the workspace volume is N commits behind.
//
// Flow:
//
//  1. Ask the Cloud bridge for the working tree's HEAD SHA + current
//     branch via /bash (cheap; one-shot `git rev-parse HEAD`).
//  2. Ask GitHub's REST API for the remote branch's HEAD SHA - reuses
//     the same /commits/<branch> call the deploy tracker uses.
//  3. Diff the two; if they don't match, ask /compare for the count.
//
// Returns 503 if the Cloud bridge is unreachable or not configured,
// so Studio can fall through quietly.

// cloudSelfRepoDir is where the cloud workspace keeps Jarvis's own infinity
// checkout after the layout split (see docker/workspace bootstrapLayout). The
// volume root is a neutral container; the self-repo lives in this subdir.
const cloudSelfRepoDir = "/workspace/infinity"

type workspaceGitStatus struct {
	Branch        string `json:"branch"`
	LocalSHA      string `json:"local_sha"`
	RemoteSHA     string `json:"remote_sha"`
	Behind        bool   `json:"behind"`
	CommitsBehind int    `json:"commits_behind"`
	Ahead         bool   `json:"ahead"`
	CommitsAhead  int    `json:"commits_ahead"`
	Repo          string `json:"repo"`
}

func (s *Server) handleBridgeWorkspaceGitStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if s.bridgeRouter == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "bridge router not configured"})
		return
	}
	st := s.bridgeRouter.Snapshot()
	if !st.CloudHealthy {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cloud bridge offline"})
		return
	}
	// Use the snapshot directly to find the Cloud bridge.
	// (The Router doesn't expose individual bridges; we go through
	// `For(PrefCloud)` to fetch it.)
	cloud, _, err := s.bridgeRouter.For(r.Context(), bridge.PrefCloud)
	if err != nil || cloud == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cloud bridge unavailable"})
		return
	}

	// ?path= selects WHICH checkout to report on. Empty = Jarvis's self-repo
	// (/workspace/infinity), compared against DopeSoft/infinity main via the
	// deploy tracker's GitHub client. A project path = an arbitrary repo under
	// /workspace/projects, reported against ITS OWN upstream with local git
	// (fetch + rev-list) so the badge is correct per-project, no hardcoding.
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path != "" {
		writeJSON(w, http.StatusOK, projectAheadBehind(r.Context(), cloud, path))
		return
	}

	// 1. Local: rev-parse HEAD on the self-repo.
	localSHA, branch := readWorkspaceHead(r.Context(), cloud, cloudSelfRepoDir)
	if localSHA == "" {
		writeJSON(w, http.StatusOK, workspaceGitStatus{Behind: false})
		return
	}

	// 2. Remote: reuse the deploy tracker's GitHub API client by
	// borrowing its compare endpoint logic. The branch from rev-parse
	// is authoritative - we ask GitHub about THAT branch, not 'main',
	// so a session checkout of a feature branch reports against its
	// own upstream.
	remote := fetchRemoteHead(r.Context(), branch)
	out := workspaceGitStatus{
		Branch:    branch,
		LocalSHA:  localSHA,
		RemoteSHA: remote,
		Repo:      globalDeployTracker.owner + "/" + globalDeployTracker.repo,
	}
	if remote != "" && remote != localSHA {
		out.Behind = true
		out.CommitsBehind = globalDeployTracker.fetchCompareAheadBy(r.Context(), localSHA, remote)
	}
	writeJSON(w, http.StatusOK, out)
}

// projectAheadBehind reports ahead/behind for an arbitrary project repo against
// its OWN upstream, using local git on the cloud bridge (a quiet fetch then
// rev-list). No GitHub API, no remote-URL parsing — works for any repo with a
// tracked upstream. Returns a zeroed status (Behind/Ahead false) when the dir
// isn't a repo or has no upstream, so the UI just shows "in sync".
func projectAheadBehind(ctx context.Context, cloud interface {
	Post(ctx context.Context, path string, body any) ([]byte, int, bool)
}, dir string) workspaceGitStatus {
	cmd := "cd " + shellQuote(dir) + " && " +
		"git fetch --quiet 2>/dev/null; " +
		"git rev-parse --abbrev-ref HEAD 2>/dev/null; " +
		"git rev-list --left-right --count '@{u}...HEAD' 2>/dev/null"
	body, status, ok := cloud.Post(ctx, "/bash", map[string]any{"cmd": cmd, "timeout_sec": 30})
	if !ok || status >= 300 {
		return workspaceGitStatus{}
	}
	out := strings.TrimSpace(extractJSONField(string(body), "output"))
	lines := strings.Split(out, "\n")
	res := workspaceGitStatus{}
	if len(lines) >= 1 {
		res.Branch = strings.TrimSpace(lines[0])
	}
	// rev-list --left-right --count @{u}...HEAD → "<behind>\t<ahead>"
	// (left = upstream-only = behind; right = HEAD-only = ahead).
	if len(lines) >= 2 {
		fields := strings.Fields(lines[1])
		if len(fields) == 2 {
			if b, err := strconv.Atoi(fields[0]); err == nil && b > 0 {
				res.Behind = true
				res.CommitsBehind = b
			}
			if a, err := strconv.Atoi(fields[1]); err == nil && a > 0 {
				res.Ahead = true
				res.CommitsAhead = a
			}
		}
	}
	return res
}

// POST /api/bridge/workspace/git-pull
//
// One-shot fast-forward pull on the cloud workspace, called when the
// boss taps the refresh icon on the staleness banner. We use
// `git pull --ff-only` so a workspace that has drifted (rebase in
// progress, local commits) refuses rather than silently merging - the
// banner then continues to surface staleness and the agent can be
// asked to reconcile. On a clean ephemeral checkout (the steady
// state) it just advances HEAD and the banner disappears on the
// follow-up status read.
//
// Returns the fresh status alongside the bash output so Studio can
// update the row without a second roundtrip.
func (s *Server) handleBridgeWorkspaceGitPull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.bridgeRouter == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "bridge router not configured"})
		return
	}
	st := s.bridgeRouter.Snapshot()
	if !st.CloudHealthy {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "cloud bridge offline"})
		return
	}
	cloud, _, err := s.bridgeRouter.For(r.Context(), bridge.PrefCloud)
	if err != nil || cloud == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "cloud bridge unavailable"})
		return
	}

	body, status, ok := cloud.Post(r.Context(), "/bash", map[string]any{
		"cmd":         "cd " + shellQuote(cloudSelfRepoDir) + " && git pull --ff-only",
		"timeout_sec": 60,
	})
	pullOut := strings.TrimSpace(extractJSONField(string(body), "output"))
	if !ok || status >= 300 {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":     false,
			"output": pullOut,
			"error":  "pull failed",
		})
		return
	}

	// Re-read status so the UI updates in one roundtrip.
	localSHA, branch := readWorkspaceHead(r.Context(), cloud, cloudSelfRepoDir)
	remote := fetchRemoteHead(r.Context(), branch)
	fresh := workspaceGitStatus{
		Branch:    branch,
		LocalSHA:  localSHA,
		RemoteSHA: remote,
		Repo:      globalDeployTracker.owner + "/" + globalDeployTracker.repo,
	}
	if remote != "" && remote != localSHA {
		fresh.Behind = true
		fresh.CommitsBehind = globalDeployTracker.fetchCompareAheadBy(r.Context(), localSHA, remote)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"output": pullOut,
		"status": fresh,
	})
}

// readWorkspaceHead asks the cloud bridge for its working tree's HEAD
// SHA + current branch in one /bash call. We do this through /bash
// rather than adding a bespoke endpoint to the workspace service so
// the bridge stays primitive (Rule #1: building blocks, not bespoke).
func readWorkspaceHead(ctx context.Context, cloud interface {
	Post(ctx context.Context, path string, body any) ([]byte, int, bool)
}, dir string) (sha, branch string) {
	body, status, ok := cloud.Post(ctx, "/bash", map[string]any{
		"cmd":         "git -C " + shellQuote(dir) + " rev-parse HEAD && git -C " + shellQuote(dir) + " rev-parse --abbrev-ref HEAD",
		"timeout_sec": 10,
	})
	if !ok || status >= 300 {
		return "", ""
	}
	// Parse out the "output" field from the bridge's JSON response.
	// We do it with a small map decode rather than typed struct to
	// keep this file self-contained.
	out := strings.TrimSpace(extractJSONField(string(body), "output"))
	parts := strings.Split(out, "\n")
	if len(parts) >= 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	if len(parts) == 1 {
		return strings.TrimSpace(parts[0]), "main"
	}
	return "", ""
}

// fetchRemoteHead pulls the remote branch's HEAD SHA via GitHub's
// REST API. Reuses globalDeployTracker for repo/owner config.
func fetchRemoteHead(ctx context.Context, branch string) string {
	if branch == "" {
		branch = globalDeployTracker.branch
	}
	url := "https://api.github.com/repos/" + globalDeployTracker.owner + "/" + globalDeployTracker.repo + "/commits/" + branch
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "infinity-workspace-staleness")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	return strings.TrimSpace(extractJSONField(readAll(resp.Body, 16<<10), "sha"))
}

// extractJSONField is a minimal "give me the string value of this top-level
// key" helper that avoids pulling encoding/json for a single field. Returns
// "" on any parse anomaly.
func extractJSONField(raw, key string) string {
	idx := strings.Index(raw, "\""+key+"\"")
	if idx < 0 {
		return ""
	}
	colon := strings.Index(raw[idx:], ":")
	if colon < 0 {
		return ""
	}
	rest := raw[idx+colon+1:]
	rest = strings.TrimLeft(rest, " \t\n\r")
	if !strings.HasPrefix(rest, "\"") {
		return ""
	}
	rest = rest[1:]
	end := strings.Index(rest, "\"")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func readAll(r interface{ Read([]byte) (int, error) }, limit int) string {
	buf := make([]byte, limit)
	total := 0
	for total < limit {
		n, err := r.Read(buf[total:])
		if n > 0 {
			total += n
		}
		if err != nil {
			break
		}
	}
	return string(buf[:total])
}
