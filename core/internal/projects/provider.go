// Package projects injects the session's ACTIVE project into the agent's
// system prompt every turn — the "where am I" anchor. Without it the agent has
// no idea which project a session is scoped to, so a mid-conversation pivot to
// another repo would silently operate in the wrong tree (the canvas + memory
// follow project_path; the agent never knew). This closes that gap and backs
// the loop's per-turn memory project-tag with the same resolution.
package projects

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Provider implements agent.MemoryProvider structurally (BuildSystemPrefix).
type Provider struct{ pool *pgxpool.Pool }

func NewProvider(pool *pgxpool.Pool) *Provider { return &Provider{pool: pool} }

// activePath reads the session's project_path. Error-safe → "".
func (p *Provider) activePath(ctx context.Context, sessionID string) string {
	if p == nil || p.pool == nil || sessionID == "" {
		return ""
	}
	var pp sql.NullString
	if err := p.pool.QueryRow(ctx,
		`SELECT project_path FROM mem_sessions WHERE id::text = $1`, sessionID,
	).Scan(&pp); err != nil {
		return ""
	}
	return strings.TrimSpace(pp.String)
}

// BuildSystemPrefix emits the active-project context block. Self/disk gets a
// "no project open — switch first" nudge; a real project names itself + its path
// + bridge so the agent works in the right tree and switches explicitly.
func (p *Provider) BuildSystemPrefix(ctx context.Context, sessionID, _ string) (string, error) {
	path := p.activePath(ctx, sessionID)

	if path == "" || path == "/workspace" || strings.HasSuffix(path, "/infinity") {
		return "<active_project>\n" +
			"No app project is open — you're on your own infinity code (the canvas shows \"Jarvis\"). " +
			"To work on an app, switch to it FIRST: `project_open <name>` for a known one, `project_create` " +
			"for a new one, or `project_clone` for an external repo. Never `cd` into a project directory and " +
			"work there without opening it — the boss's canvas, git panel, and memory all follow the active " +
			"project, so silent off-project work leaves him blind to what you're doing.\n" +
			"</active_project>", nil
	}

	bridgeKind := "Mac"
	if strings.HasPrefix(path, "/workspace") {
		bridgeKind = "cloud"
	}
	name := filepath.Base(path)
	return fmt.Sprintf("<active_project>\n"+
		"Active project: %s  (%s, %s). The boss sees this in the canvas breadcrumb; the file tree, git "+
		"panel, and your edits are scoped to this project. Work here. To switch to a DIFFERENT project, "+
		"call project_open / project_create / project_clone FIRST so the canvas and memory follow — never "+
		"silently operate in another repo's directory.\n"+
		"</active_project>", name, path, bridgeKind), nil
}

// ProjectFetcher returns the resolver the agent loop uses to tag each turn's
// observations with the active project (one source of truth with the prompt
// block above). Self/disk → "" (no tag); a project → its dir name.
func (p *Provider) ProjectFetcher() func(ctx context.Context, sessionID string) string {
	return func(ctx context.Context, sessionID string) string {
		path := p.activePath(ctx, sessionID)
		if path == "" || path == "/workspace" {
			return ""
		}
		return filepath.Base(path)
	}
}
