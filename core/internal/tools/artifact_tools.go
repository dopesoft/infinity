// artifact_tools.go — generic CRUD over mem_artifacts.
//
// Per Rule #1 (agents assemble, not hardwire), these are building blocks
// the agent uses to surface "things I've made" without bespoke
// per-kind code paths. `project_create` writes through these. So does
// `image_generate` (future). So does anything else that produces a
// durable artifact.
//
// Generic shape:
//
//   artifact_save({kind, name, virtual_path, storage_kind, storage_path?,
//                  description?, tags?, metadata?, derived_from?,
//                  github_url?, bridge?, source_tool?})
//   artifact_list({kind?, tag?, virtual_path_prefix?, session_id?, limit?})
//   artifact_get({id?, name?, virtual_path?})
//   artifact_delete({id})   — soft delete (sets deleted_at)
//
// virtual_path is the unique boss-facing identity. The Library UI in
// Studio renders artifacts as a tree keyed on virtual_path so the boss
// can browse like a filesystem regardless of where bytes actually live.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterArtifactTools registers the generic CRUD tools. project_create,
// image_generate, etc. are higher-level tools registered separately that
// write rows via these primitives.
func RegisterArtifactTools(r *Registry, pool *pgxpool.Pool) {
	if pool == nil {
		return
	}
	r.Register(&artifactSave{pool: pool})
	r.Register(&artifactList{pool: pool})
	r.Register(&artifactGet{pool: pool})
	r.Register(&artifactDelete{pool: pool})
}

// ── artifact_save ────────────────────────────────────────────────────────

type artifactSave struct{ pool *pgxpool.Pool }

func (t *artifactSave) Name() string { return "artifact_save" }
func (t *artifactSave) Description() string {
	return "Index a created artifact (project, image, document, audio, video, dataset). " +
		"`virtual_path` is the boss-facing tree location (e.g. /projects/habit-tracker, " +
		"/images/landing-page/hero-v3.png) — it powers the Library UI. `storage_path` is " +
		"where bytes actually live (real FS path OR object-store URL). Returns the new id."
}
func (t *artifactSave) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind":          map[string]any{"type": "string", "description": "project | image | audio | video | document | dataset | memory | other"},
			"name":          map[string]any{"type": "string"},
			"description":   map[string]any{"type": "string"},
			"virtual_path":  map[string]any{"type": "string", "description": "Boss-facing folder/path in the Library tree. Must be unique."},
			"storage_kind":  map[string]any{"type": "string", "enum": []string{"filesystem", "object_store", "postgres", "inline"}},
			"storage_path":  map[string]any{"type": "string"},
			"storage_size":  map[string]any{"type": "integer"},
			"storage_mime":  map[string]any{"type": "string"},
			"bridge":        map[string]any{"type": "string", "enum": []string{"mac", "cloud"}},
			"github_url":    map[string]any{"type": "string"},
			"derived_from":  map[string]any{"type": "string", "description": "Optional parent artifact id this was derived from."},
			"source_tool":   map[string]any{"type": "string"},
			"tags":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"metadata":      map[string]any{"type": "object"},
		},
		"required": []string{"kind", "name", "virtual_path", "storage_kind"},
	}
}

func (t *artifactSave) Execute(ctx context.Context, in map[string]any) (string, error) {
	kind := strString(in, "kind")
	name := strString(in, "name")
	vpath := strString(in, "virtual_path")
	storageKind := strString(in, "storage_kind")
	if kind == "" || name == "" || vpath == "" || storageKind == "" {
		return "", errors.New("kind, name, virtual_path, storage_kind all required")
	}
	tags := stringSliceOrEmpty(in, "tags")
	meta := mapOrEmpty(in, "metadata")
	tagsJSON, _ := json.Marshal(tags)
	metaJSON, _ := json.Marshal(meta)

	var derivedFrom *string
	if s := strString(in, "derived_from"); s != "" {
		derivedFrom = &s
	}
	sid := SessionIDFromContext(ctx)
	var sidPtr *string
	if sid != "" {
		sidPtr = &sid
	}

	var id string
	err := t.pool.QueryRow(ctx, `
		INSERT INTO mem_artifacts
			(kind, name, description, virtual_path,
			 storage_kind, storage_path, storage_size, storage_mime,
			 bridge, github_url, derived_from,
			 source_session_id, source_tool,
			 tags, metadata)
		VALUES
			($1, $2, NULLIF($3,''), $4,
			 $5, NULLIF($6,''), NULLIF($7,0), NULLIF($8,''),
			 NULLIF($9,''), NULLIF($10,''), $11,
			 $12, NULLIF($13,''),
			 $14::jsonb, $15::jsonb)
		RETURNING id::text
	`,
		kind, name, strString(in, "description"), vpath,
		storageKind, strString(in, "storage_path"), intOrZero(in, "storage_size"), strString(in, "storage_mime"),
		strString(in, "bridge"), strString(in, "github_url"), derivedFrom,
		sidPtr, strString(in, "source_tool"),
		string(tagsJSON), string(metaJSON),
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("artifact_save: %w", err)
	}
	out, _ := json.Marshal(map[string]any{"id": id, "virtual_path": vpath})
	return string(out), nil
}

// ── artifact_list ────────────────────────────────────────────────────────

type artifactList struct{ pool *pgxpool.Pool }

func (t *artifactList) Name() string     { return "artifact_list" }
func (t *artifactList) ReadOnly() bool   { return true }
func (t *artifactList) Description() string {
	return "List artifacts. Filter by kind, tag, virtual_path prefix, or session. " +
		"Returns rows sorted by most-recent-first. Use this BEFORE creating " +
		"something similar to check for duplicates."
}
func (t *artifactList) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind":                map[string]any{"type": "string"},
			"tag":                 map[string]any{"type": "string"},
			"virtual_path_prefix": map[string]any{"type": "string"},
			"session_id":          map[string]any{"type": "string"},
			"limit":               map[string]any{"type": "integer", "default": 50},
		},
	}
}
func (t *artifactList) Execute(ctx context.Context, in map[string]any) (string, error) {
	limit := intOrZero(in, "limit")
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	clauses := []string{"deleted_at IS NULL"}
	args := []any{}
	add := func(sql string, val any) {
		args = append(args, val)
		clauses = append(clauses, strings.ReplaceAll(sql, "?", fmt.Sprintf("$%d", len(args))))
	}
	if k := strString(in, "kind"); k != "" {
		add("kind = ?", k)
	}
	if tag := strString(in, "tag"); tag != "" {
		add("tags @> ?::jsonb", fmt.Sprintf(`["%s"]`, tag))
	}
	if p := strString(in, "virtual_path_prefix"); p != "" {
		add("virtual_path LIKE ?", p+"%")
	}
	if s := strString(in, "session_id"); s != "" {
		add("source_session_id::text = ?", s)
	}
	args = append(args, limit)
	query := fmt.Sprintf(`
		SELECT id::text, kind, name, virtual_path, storage_kind, storage_path,
		       bridge, github_url, source_session_id::text,
		       tags::text, metadata::text,
		       created_at
		  FROM mem_artifacts
		 WHERE %s
		 ORDER BY created_at DESC
		 LIMIT $%d
	`, strings.Join(clauses, " AND "), len(args))
	rows, err := t.pool.Query(ctx, query, args...)
	if err != nil {
		return "", fmt.Errorf("artifact_list: %w", err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id, kind, name, vpath, storageKind  string
			storagePath, bridge, github, sessID *string
			tagsRaw, metaRaw                    string
			createdAt                           any
		)
		if err := rows.Scan(&id, &kind, &name, &vpath, &storageKind,
			&storagePath, &bridge, &github, &sessID,
			&tagsRaw, &metaRaw, &createdAt); err != nil {
			return "", fmt.Errorf("artifact_list scan: %w", err)
		}
		row := map[string]any{
			"id":           id,
			"kind":         kind,
			"name":         name,
			"virtual_path": vpath,
			"storage_kind": storageKind,
			"created_at":   createdAt,
		}
		if storagePath != nil {
			row["storage_path"] = *storagePath
		}
		if bridge != nil {
			row["bridge"] = *bridge
		}
		if github != nil {
			row["github_url"] = *github
		}
		if sessID != nil {
			row["source_session_id"] = *sessID
		}
		var tags []string
		var meta map[string]any
		_ = json.Unmarshal([]byte(tagsRaw), &tags)
		_ = json.Unmarshal([]byte(metaRaw), &meta)
		row["tags"] = tags
		row["metadata"] = meta
		out = append(out, row)
	}
	b, _ := json.Marshal(map[string]any{"count": len(out), "artifacts": out})
	return string(b), nil
}

// ── artifact_get ─────────────────────────────────────────────────────────

type artifactGet struct{ pool *pgxpool.Pool }

func (t *artifactGet) Name() string     { return "artifact_get" }
func (t *artifactGet) ReadOnly() bool   { return true }
func (t *artifactGet) Description() string {
	return "Fetch one artifact by id, virtual_path, or name. Returns the full row including metadata. " +
		"Bumps accessed_at so 'recently used' queries work."
}
func (t *artifactGet) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":           map[string]any{"type": "string"},
			"virtual_path": map[string]any{"type": "string"},
			"name":         map[string]any{"type": "string"},
		},
	}
}
func (t *artifactGet) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "id")
	vpath := strString(in, "virtual_path")
	name := strString(in, "name")
	if id == "" && vpath == "" && name == "" {
		return "", errors.New("one of id, virtual_path, name is required")
	}

	var query string
	var arg any
	switch {
	case id != "":
		query = `SELECT id::text, kind, name, description, virtual_path,
		                storage_kind, storage_path, storage_size, storage_mime,
		                bridge, github_url, derived_from::text,
		                source_session_id::text, source_tool,
		                tags::text, metadata::text,
		                created_at, updated_at
		           FROM mem_artifacts
		          WHERE id::text = $1 AND deleted_at IS NULL`
		arg = id
	case vpath != "":
		query = `SELECT id::text, kind, name, description, virtual_path,
		                storage_kind, storage_path, storage_size, storage_mime,
		                bridge, github_url, derived_from::text,
		                source_session_id::text, source_tool,
		                tags::text, metadata::text,
		                created_at, updated_at
		           FROM mem_artifacts
		          WHERE virtual_path = $1 AND deleted_at IS NULL`
		arg = vpath
	default:
		query = `SELECT id::text, kind, name, description, virtual_path,
		                storage_kind, storage_path, storage_size, storage_mime,
		                bridge, github_url, derived_from::text,
		                source_session_id::text, source_tool,
		                tags::text, metadata::text,
		                created_at, updated_at
		           FROM mem_artifacts
		          WHERE LOWER(name) = LOWER($1) AND deleted_at IS NULL
		          ORDER BY created_at DESC
		          LIMIT 1`
		arg = name
	}

	var (
		rid, kind, n, vp, storageKind                                  string
		desc, storagePath, mime, bridge, github, derived, sessID, tool *string
		size                                                           *int64
		tagsRaw, metaRaw                                               string
		createdAt, updatedAt                                           any
	)
	err := t.pool.QueryRow(ctx, query, arg).Scan(
		&rid, &kind, &n, &desc, &vp,
		&storageKind, &storagePath, &size, &mime,
		&bridge, &github, &derived,
		&sessID, &tool,
		&tagsRaw, &metaRaw,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return "", fmt.Errorf("artifact_get: %w", err)
	}
	// Best-effort accessed_at bump; ignore the result.
	_, _ = t.pool.Exec(ctx, `UPDATE mem_artifacts SET accessed_at = NOW() WHERE id::text = $1`, rid)

	row := map[string]any{
		"id":           rid,
		"kind":         kind,
		"name":         n,
		"virtual_path": vp,
		"storage_kind": storageKind,
		"created_at":   createdAt,
		"updated_at":   updatedAt,
	}
	if desc != nil {
		row["description"] = *desc
	}
	if storagePath != nil {
		row["storage_path"] = *storagePath
	}
	if size != nil {
		row["storage_size"] = *size
	}
	if mime != nil {
		row["storage_mime"] = *mime
	}
	if bridge != nil {
		row["bridge"] = *bridge
	}
	if github != nil {
		row["github_url"] = *github
	}
	if derived != nil {
		row["derived_from"] = *derived
	}
	if sessID != nil {
		row["source_session_id"] = *sessID
	}
	if tool != nil {
		row["source_tool"] = *tool
	}
	var tags []string
	var meta map[string]any
	_ = json.Unmarshal([]byte(tagsRaw), &tags)
	_ = json.Unmarshal([]byte(metaRaw), &meta)
	row["tags"] = tags
	row["metadata"] = meta
	b, _ := json.Marshal(row)
	return string(b), nil
}

// ── artifact_delete ──────────────────────────────────────────────────────

type artifactDelete struct{ pool *pgxpool.Pool }

func (t *artifactDelete) Name() string { return "artifact_delete" }
func (t *artifactDelete) Description() string {
	return "Soft-delete an artifact by id. The metadata row stays for audit but no longer surfaces in lists/Library. " +
		"For project artifacts, this does NOT delete the actual files on disk — use bash_run for that."
}
func (t *artifactDelete) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "string"},
		},
		"required": []string{"id"},
	}
}
func (t *artifactDelete) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "id")
	if id == "" {
		return "", errors.New("id required")
	}
	tag, err := t.pool.Exec(ctx, `UPDATE mem_artifacts SET deleted_at = NOW() WHERE id::text = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return "", fmt.Errorf("artifact_delete: %w", err)
	}
	out, _ := json.Marshal(map[string]any{"id": id, "deleted": tag.RowsAffected()})
	return string(out), nil
}

// ── helpers ──────────────────────────────────────────────────────────────

func stringSliceOrEmpty(in map[string]any, key string) []string {
	out := []string{}
	if arr, ok := in[key].([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func mapOrEmpty(in map[string]any, key string) map[string]any {
	if m, ok := in[key].(map[string]any); ok {
		return m
	}
	return map[string]any{}
}
