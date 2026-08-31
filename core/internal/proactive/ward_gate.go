package proactive

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dopesoft/infinity/core/internal/agent"
)

// WardGate is the agent.ToolGate that enforces structural PRIVACY zones.
//
// memory.StripSecrets redacts secrets at the capture boundary (what we store).
// It does nothing about what the agent may READ. A Ward (mem_wards) is a
// declared path pattern: a 'private' ward DENIES the read outright; a
// 'sensitive' ward routes it through the Trust queue. This is the load-bearing
// mechanic — enforced here in Go, not in skill prose the LLM can drop (Rule
// #1b). It inspects the path-bearing tools (claude_code__read/edit/write,
// filesystem__read_*, and any bash command that names a warded path).
//
// The credential/.env/key defaults ship seeded (migration 132) so the obvious
// secrets are protected from first boot; the boss manages the rest in Settings
// → Privacy.
type WardGate struct {
	trust *TrustStore
	pool  *pgxpool.Pool

	mu       sync.Mutex
	wards    []ward
	loadedAt time.Time
	ttl      time.Duration
}

type ward struct {
	glob  string
	level string // private | sensitive
}

func NewWardGate(trust *TrustStore, pool *pgxpool.Pool) *WardGate {
	return &WardGate{trust: trust, pool: pool, ttl: 60 * time.Second}
}

// loadWards refreshes the cached ward list at most once per ttl. Best-effort: a
// load error keeps the previous cache (fail-open on a DB hiccup, never on a
// match).
func (g *WardGate) loadWards(ctx context.Context) []ward {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.wards != nil && time.Since(g.loadedAt) < g.ttl {
		return g.wards
	}
	if g.pool == nil {
		return g.wards
	}
	rows, err := g.pool.Query(ctx, `SELECT glob, level FROM mem_wards`)
	if err != nil {
		return g.wards
	}
	defer rows.Close()
	var out []ward
	for rows.Next() {
		var wd ward
		if err := rows.Scan(&wd.glob, &wd.level); err != nil {
			continue
		}
		wd.glob = strings.TrimSpace(wd.glob)
		if wd.glob != "" {
			out = append(out, wd)
		}
	}
	g.wards = out
	g.loadedAt = time.Now()
	return g.wards
}

// matchPath returns the strictest ward level matched by an explicit path
// ("private" beats "sensitive"), or "" when none match. Matches the glob
// against the path's basename (path.Match) and, for wildcard-free globs, as a
// path substring (so '/.env' / '.credentials.json' anywhere is caught).
func matchPath(wards []ward, p string) (string, string) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", ""
	}
	base := path.Base(p)
	level := ""
	matchedGlob := ""
	for _, wd := range wards {
		hit := false
		if ok, _ := path.Match(wd.glob, base); ok {
			hit = true
		} else if !strings.Contains(wd.glob, "*") && strings.Contains(p, wd.glob) {
			hit = true
		} else if strings.HasPrefix(wd.glob, "*/") && base == strings.TrimPrefix(wd.glob, "*/") {
			hit = true
		}
		if hit {
			if wd.level == "private" {
				return "private", wd.glob
			}
			if level == "" {
				level = wd.level
				matchedGlob = wd.glob
			}
		}
	}
	return level, matchedGlob
}

// matchCommand scans a bash command string for any warded path, returning the
// strictest level. A ward's needle is its glob with wildcards stripped; if that
// substring appears in the command, the command touches a warded path.
func matchCommand(wards []ward, cmd string) (string, string) {
	cmd = strings.ToLower(cmd)
	if strings.TrimSpace(cmd) == "" {
		return "", ""
	}
	level := ""
	matchedGlob := ""
	for _, wd := range wards {
		needle := strings.ToLower(strings.Trim(strings.ReplaceAll(wd.glob, "*", ""), " /"))
		if needle == "" {
			continue
		}
		if strings.Contains(cmd, needle) {
			if wd.level == "private" {
				return "private", wd.glob
			}
			if level == "" {
				level = wd.level
				matchedGlob = wd.glob
			}
		}
	}
	return level, matchedGlob
}

// wardedPaths returns the explicit path(s) a tool wants to touch, and whether
// the tool is a bash-style command (scanned differently). Returns nil for tools
// that don't touch the filesystem.
func wardedPaths(toolName string, input map[string]any) (paths []string, command string, isFile bool) {
	name := strings.ToLower(toolName)
	switch {
	case name == "claude_code__bash", name == "bash_run":
		if c, _ := input["command"].(string); c != "" {
			return nil, c, true
		}
		if c, _ := input["cmd"].(string); c != "" {
			return nil, c, true
		}
		return nil, "", true
	case strings.HasPrefix(name, "claude_code__read"),
		strings.HasPrefix(name, "claude_code__edit"),
		strings.HasPrefix(name, "claude_code__write"),
		strings.HasPrefix(name, "filesystem__read"):
		for _, k := range []string{"file_path", "path", "filename"} {
			if v, ok := input[k].(string); ok && strings.TrimSpace(v) != "" {
				paths = append(paths, v)
			}
		}
		// filesystem__read_multiple_files passes an array.
		if arr, ok := input["paths"].([]any); ok {
			for _, p := range arr {
				if s, ok := p.(string); ok && strings.TrimSpace(s) != "" {
					paths = append(paths, s)
				}
			}
		}
		return paths, "", true
	}
	return nil, "", false
}

// Authorize implements agent.ToolGate. Only acts on path-bearing tools; passes
// everything else through.
func (g *WardGate) Authorize(ctx context.Context, sessionID, project, toolName string, input map[string]any) agent.GateDecision {
	if g == nil {
		return agent.GateDecision{Allow: true}
	}
	paths, command, isFile := wardedPaths(toolName, input)
	if !isFile {
		return agent.GateDecision{Allow: true}
	}
	wards := g.loadWards(ctx)
	if len(wards) == 0 {
		return agent.GateDecision{Allow: true}
	}

	level, glob := "", ""
	if command != "" {
		level, glob = matchCommand(wards, command)
	}
	for _, p := range paths {
		if level == "private" {
			break
		}
		if l, gl := matchPath(wards, p); l != "" {
			if l == "private" || level == "" {
				level, glob = l, gl
			}
		}
	}
	if level == "" {
		return agent.GateDecision{Allow: true}
	}

	if level == "private" {
		infoLog.Printf("WardGate: blocked %s on private ward %q", toolName, glob)
		return agent.GateDecision{
			Allow: false,
			Reason: fmt.Sprintf("That path is warded private (%q) — reading it is blocked for the boss's privacy. "+
				"If this is genuinely needed, ask the boss to lower the ward to 'sensitive' in Settings → Privacy.", glob),
		}
	}

	// sensitive → queue a Trust contract.
	if g.trust == nil {
		return agent.GateDecision{Allow: false, Reason: "trust store not configured; refusing to read a sensitive-warded path unattended"}
	}
	id, err := g.trust.Queue(ctx, &TrustContract{
		Title:     "Read a sensitive path",
		RiskLevel: "medium",
		Source:    "ward_gate",
		ActionSpec: map[string]any{
			"tool":       toolName,
			"input":      input,
			"session_id": sessionID,
			"ward":       glob,
		},
		Reasoning: fmt.Sprintf("Jarvis wants to read a path you marked sensitive (%q). Approve to allow this read.", glob),
		Preview:   buildPreview(ctx, toolName, input),
	})
	if err != nil || id == "" {
		return agent.GateDecision{Allow: false, Reason: "could not queue trust contract for sensitive ward"}
	}
	return agent.GateDecision{
		Allow:           false,
		Reason:          "awaiting boss approval (sensitive ward)",
		ContractID:      id,
		WaitForApproval: true,
		WaitTimeout:     15 * time.Minute,
	}
}

// WaitForDecision implements agent.ToolGate — polls the queued contract until
// it resolves. Mirrors ClaudeCodeGate.WaitForDecision.
func (g *WardGate) WaitForDecision(ctx context.Context, contractID string, timeout time.Duration) (bool, string) {
	if g == nil || g.trust == nil || contractID == "" {
		return false, "trust store not configured"
	}
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return false, "timed out waiting for approval (" + timeout.String() + ")"
			}
			return false, "session ended before approval"
		case <-tick.C:
			status, _, _, err := g.trust.LookupForGate(waitCtx, contractID)
			if err != nil {
				continue
			}
			switch status {
			case "approved":
				_, _ = g.trust.ConsumeByID(waitCtx, contractID)
				return true, ""
			case "denied":
				return false, "denied by the boss"
			case "snoozed":
				return false, "snoozed by the boss (treat as deny for this run)"
			case "consumed":
				return true, ""
			}
		}
	}
}
