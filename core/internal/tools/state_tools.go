// state_tools.go - generic keyed state cells over mem_agent_state.
//
// Per Rule #1 (agents assemble, not hardwire), this is the building block
// for anything that needs to remember "what did I see last time?" across
// runs: a standing watch diffs the world against its previous snapshot, a
// recurring recipe checkpoints progress, a counter accumulates. Semantic
// memory (remember/recall) is fuzzy retrieval - this is an exact, keyed
// cell the agent reads and writes deliberately.
//
// Generic shape:
//
//	state_set({key, value, note?})    - upsert any JSON value under a key
//	state_get({key})                  - exact read; found=false when absent
//	state_list({prefix?, limit?})     - enumerate keys (no values; keep it light)
//	state_delete({key})               - remove a cell
//
// Key convention: namespace with a colon path, e.g. "watch:ai-news:last",
// "sentinel:<id>:baseline", so state_list({prefix:"watch:"}) enumerates a
// family.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterStateTools registers the keyed-state building block.
func RegisterStateTools(r *Registry, pool *pgxpool.Pool) {
	if pool == nil {
		return
	}
	r.Register(&stateSet{pool: pool})
	r.Register(&stateGet{pool: pool})
	r.Register(&stateList{pool: pool})
	r.Register(&stateDelete{pool: pool})
}

// maxStateValueBytes caps a single cell so a runaway snapshot can't bloat
// the table or a future read's context. Diff snapshots should be digests
// (ids, hashes, headlines), not raw payload dumps.
const maxStateValueBytes = 64 * 1024

// ── state_set ────────────────────────────────────────────────────────────

type stateSet struct{ pool *pgxpool.Pool }

func (t *stateSet) Name() string { return "state_set" }
func (t *stateSet) Description() string {
	return "Save a JSON value under an exact key, overwriting what was there. " +
		"This is your durable keyed memory for recurring work - e.g. after a " +
		"watch run, state_set the snapshot you compared against so the next run " +
		"can diff. Namespace keys like 'watch:<topic>:last'. Keep values small " +
		"(ids, hashes, headlines) - not raw payload dumps."
}
func (t *stateSet) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key":   map[string]any{"type": "string", "description": "Exact key, colon-namespaced (e.g. watch:ai-news:last)."},
			"value": map[string]any{"description": "Any JSON value - object, array, string, number."},
			"note":  map[string]any{"type": "string", "description": "Optional one-line human description of what this cell holds."},
		},
		"required": []string{"key", "value"},
	}
}
func (t *stateSet) Execute(ctx context.Context, in map[string]any) (string, error) {
	key := strings.TrimSpace(stateStr(in, "key"))
	if key == "" {
		return "", errors.New("key is required")
	}
	raw, err := json.Marshal(in["value"])
	if err != nil {
		return "", fmt.Errorf("value is not valid JSON: %w", err)
	}
	if len(raw) > maxStateValueBytes {
		return "", fmt.Errorf("value is %d bytes; cap is %d - store a digest (ids/hashes/headlines), not the raw payload", len(raw), maxStateValueBytes)
	}
	_, err = t.pool.Exec(ctx, `
		INSERT INTO mem_agent_state (key, value, note, updated_at)
		VALUES ($1, $2::jsonb, $3, NOW())
		ON CONFLICT (key) DO UPDATE
		SET value = EXCLUDED.value,
		    note = CASE WHEN EXCLUDED.note <> '' THEN EXCLUDED.note ELSE mem_agent_state.note END,
		    updated_at = NOW()
	`, key, string(raw), strings.TrimSpace(stateStr(in, "note")))
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "key": key, "bytes": len(raw)})
	return string(out), nil
}

// ── state_get ────────────────────────────────────────────────────────────

type stateGet struct{ pool *pgxpool.Pool }

func (t *stateGet) Name() string { return "state_get" }
func (t *stateGet) Description() string {
	return "Read the JSON value saved under an exact key with state_set. " +
		"Returns found=false (not an error) when the key has never been set - " +
		"a watch's first run reads nothing, does its baseline pass, then " +
		"state_set's the snapshot for next time."
}
func (t *stateGet) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key": map[string]any{"type": "string", "description": "Exact key to read."},
		},
		"required": []string{"key"},
	}
}
func (t *stateGet) Execute(ctx context.Context, in map[string]any) (string, error) {
	key := strings.TrimSpace(stateStr(in, "key"))
	if key == "" {
		return "", errors.New("key is required")
	}
	var raw string
	var note string
	var updatedAt time.Time
	err := t.pool.QueryRow(ctx, `
		SELECT value::text, note, updated_at FROM mem_agent_state WHERE key = $1
	`, key).Scan(&raw, &note, &updatedAt)
	if err != nil {
		// pgx returns ErrNoRows through the pool as "no rows in result set";
		// absence is a legitimate first-run answer, not a failure.
		if strings.Contains(err.Error(), "no rows") {
			out, _ := json.Marshal(map[string]any{"found": false, "key": key})
			return string(out), nil
		}
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"found":      true,
		"key":        key,
		"value":      json.RawMessage(raw),
		"note":       note,
		"updated_at": updatedAt.UTC().Format(time.RFC3339),
	})
	return string(out), nil
}

// ── state_list ───────────────────────────────────────────────────────────

type stateList struct{ pool *pgxpool.Pool }

func (t *stateList) Name() string { return "state_list" }
func (t *stateList) Description() string {
	return "Enumerate saved state keys (optionally by prefix, e.g. 'watch:'). " +
		"Returns keys, notes, and update times - not the values; state_get each " +
		"key you actually need."
}
func (t *stateList) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prefix": map[string]any{"type": "string", "description": "Only keys starting with this prefix."},
			"limit":  map[string]any{"type": "integer", "description": "Max rows. Default 50."},
		},
	}
}
func (t *stateList) Execute(ctx context.Context, in map[string]any) (string, error) {
	limit := 50
	if v, ok := in["limit"].(float64); ok && v > 0 && v <= 500 {
		limit = int(v)
	}
	prefix := strings.TrimSpace(stateStr(in, "prefix"))
	rows, err := t.pool.Query(ctx, `
		SELECT key, note, updated_at FROM mem_agent_state
		WHERE ($1 = '' OR key LIKE $1 || '%')
		ORDER BY updated_at DESC
		LIMIT $2
	`, prefix, limit)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	type cell struct {
		Key       string `json:"key"`
		Note      string `json:"note,omitempty"`
		UpdatedAt string `json:"updated_at"`
	}
	out := []cell{}
	for rows.Next() {
		var c cell
		var updatedAt time.Time
		if err := rows.Scan(&c.Key, &c.Note, &updatedAt); err != nil {
			return "", err
		}
		c.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	b, _ := json.Marshal(map[string]any{"count": len(out), "cells": out})
	return string(b), nil
}

// ── state_delete ─────────────────────────────────────────────────────────

type stateDelete struct{ pool *pgxpool.Pool }

func (t *stateDelete) Name() string { return "state_delete" }
func (t *stateDelete) Description() string {
	return "Delete a saved state cell by exact key - e.g. when a standing watch " +
		"is cancelled and its snapshot is no longer needed."
}
func (t *stateDelete) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key": map[string]any{"type": "string", "description": "Exact key to delete."},
		},
		"required": []string{"key"},
	}
}
func (t *stateDelete) Execute(ctx context.Context, in map[string]any) (string, error) {
	key := strings.TrimSpace(stateStr(in, "key"))
	if key == "" {
		return "", errors.New("key is required")
	}
	tag, err := t.pool.Exec(ctx, `DELETE FROM mem_agent_state WHERE key = $1`, key)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "key": key, "deleted": tag.RowsAffected() > 0})
	return string(out), nil
}

func stateStr(in map[string]any, key string) string {
	if v, ok := in[key].(string); ok {
		return v
	}
	return ""
}
