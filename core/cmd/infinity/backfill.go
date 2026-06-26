package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

// One-time recovery for documents made before the artifacts repository existed.
//
// The Studio Media/Artifacts gallery (and doc-tab rehydration) reads
// mem_artifacts rows that document_create now writes deterministically. Older
// docs never got a row — but every document_create left a PostToolUse
// observation whose raw_text is a stable, parseable line:
//
//	document_create: Created <file> (<fmt>, <bytes> bytes) at <path>
//	PDF preview: <pdf_path>            (present only when also_pdf ran)
//
// This command reconstructs the path / format / session from those
// observations, records a mem_artifacts row per document, and (best-effort)
// renders a preview PDF + thumbnail from the still-on-disk file via the
// workspace /docpreview endpoint. Idempotent: ON CONFLICT merges metadata, so
// it's safe to run before the workspace deploy (rows only) and again after
// (fills in thumbnails).

var (
	docCreateRe = regexp.MustCompile(`Created\s+(\S+)\s+\((\w+),\s+(\d+)\s+bytes\)\s+at\s+(\S+)`)
	pdfPreviewRe = regexp.MustCompile(`PDF preview:\s+(\S+)`)
	errFileGone  = errors.New("file no longer exists")
)

type backfillCand struct {
	session, filename, format, path, pdfPath string
	size                                     int64
}

func backfillArtifactsCmd() *cobra.Command {
	var (
		session string
		dryRun  bool
		noThumb bool
	)
	cmd := &cobra.Command{
		Use:   "backfill-artifacts",
		Short: "Recover pre-existing documents into mem_artifacts so they show in the Studio gallery",
		Long: `Scans mem_observations for past document_create tool results, reconstructs the
file path / format / session for each, and writes a mem_artifacts row so the
document appears in the boss's Studio Media/Artifacts gallery (and its tabs
rehydrate). When the workspace bridge is reachable it also renders a preview
PDF + thumbnail from the still-on-disk file (skip with --no-thumb; a file that
no longer exists is skipped, not recorded as a broken row).

Idempotent: ON CONFLICT merges metadata, so it's safe to re-run (e.g. once
before the workspace deploy for rows, again after for thumbnails).`,
		RunE: func(_ *cobra.Command, _ []string) error {
			dsn := os.Getenv("DATABASE_URL")
			if dsn == "" {
				return errors.New("DATABASE_URL is required")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
			defer cancel()
			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return err
			}
			defer pool.Close()

			wsURL := strings.TrimRight(strings.TrimSpace(os.Getenv("WORKSPACE_BRIDGE_URL")), "/")
			if wsURL == "" && strings.TrimSpace(os.Getenv("RAILWAY_ENVIRONMENT_NAME")) != "" {
				wsURL = "http://workspace.railway.internal:8080"
			}
			wsToken := strings.TrimSpace(os.Getenv("WORKSPACE_BRIDGE_TOKEN"))

			cands, err := loadBackfillCandidates(ctx, pool, session)
			if err != nil {
				return err
			}
			if len(cands) == 0 {
				fmt.Println("backfill: no document_create observations found")
				return nil
			}
			fmt.Printf("backfill: found %d distinct document(s)\n", len(cands))

			recorded, thumbed, skipped := 0, 0, 0
			for _, c := range cands {
				pdfPath, thumbPath := c.pdfPath, ""
				if !noThumb && !dryRun && wsURL != "" {
					pv, err := docPreviewRender(ctx, wsURL, wsToken, c.path)
					switch {
					case errors.Is(err, errFileGone):
						fmt.Printf("  skip %s — file is gone (%s)\n", c.filename, c.path)
						skipped++
						continue
					case err != nil:
						fmt.Printf("  %s — preview failed (%v); recording row without thumbnail\n", c.filename, err)
					default:
						if pv.PDFPath != "" {
							pdfPath = pv.PDFPath
						}
						if pv.ThumbPath != "" {
							thumbPath = pv.ThumbPath
							thumbed++
						}
					}
				}
				if dryRun {
					fmt.Printf("  [dry] %-46s %-5s session=%s pdf=%s\n", c.filename, c.format, shortID(c.session), yesNo(pdfPath))
					continue
				}
				if err := upsertBackfillArtifact(ctx, pool, c, pdfPath, thumbPath); err != nil {
					fmt.Printf("  insert %s: %v\n", c.filename, err)
					continue
				}
				recorded++
			}
			if dryRun {
				fmt.Printf("backfill: %d document(s) would be recorded (dry-run, nothing written)\n", len(cands))
				return nil
			}
			fmt.Printf("backfill: recorded %d, thumbnailed %d, skipped %d\n", recorded, thumbed, skipped)
			return nil
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "limit to a single session id (UUID)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "parse + report only, write nothing")
	cmd.Flags().BoolVar(&noThumb, "no-thumb", false, "skip preview/thumbnail rendering (record rows only)")
	return cmd
}

// loadBackfillCandidates parses document_create observations into one candidate
// per distinct file path (latest observation wins on metadata/session).
func loadBackfillCandidates(ctx context.Context, pool *pgxpool.Pool, session string) ([]backfillCand, error) {
	where := "hook_name = 'PostToolUse' AND raw_text LIKE 'document_create:%'"
	args := []any{}
	if session != "" {
		where += " AND session_id = $1"
		args = append(args, session)
	}
	rows, err := pool.Query(ctx, `
		SELECT session_id::text, raw_text
		  FROM mem_observations
		 WHERE `+where+`
		 ORDER BY created_at ASC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byPath := map[string]backfillCand{}
	order := []string{}
	for rows.Next() {
		var sid, raw string
		if err := rows.Scan(&sid, &raw); err != nil {
			continue
		}
		m := docCreateRe.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		size, _ := strconv.ParseInt(m[3], 10, 64)
		c := backfillCand{
			session:  sid,
			filename: m[1],
			format:   strings.ToLower(m[2]),
			size:     size,
			path:     m[4],
		}
		if pm := pdfPreviewRe.FindStringSubmatch(raw); pm != nil {
			c.pdfPath = pm[1]
		}
		if _, ok := byPath[c.path]; !ok {
			order = append(order, c.path)
		}
		byPath[c.path] = c // last wins
	}
	out := make([]backfillCand, 0, len(order))
	for _, p := range order {
		out = append(out, byPath[p])
	}
	return out, nil
}

func upsertBackfillArtifact(ctx context.Context, pool *pgxpool.Pool, c backfillCand, pdfPath, thumbPath string) error {
	meta := map[string]any{"format": c.format}
	if pdfPath != "" {
		meta["pdf_path"] = pdfPath
	}
	if thumbPath != "" {
		meta["thumb_path"] = thumbPath
	}
	metaJSON, _ := json.Marshal(meta)
	vpath := "/artifacts/" + filepath.Base(c.path)
	_, err := pool.Exec(ctx, `
		INSERT INTO mem_artifacts
			(kind, name, storage_kind, storage_path, storage_size, storage_mime,
			 virtual_path, bridge, source_session_id, source_tool, metadata)
		VALUES
			('document', $1, 'filesystem', $2, NULLIF($3,0), NULLIF($4,''),
			 $5, 'cloud', $6, 'document_create:backfill', $7::jsonb)
		ON CONFLICT (virtual_path) WHERE deleted_at IS NULL DO UPDATE
			SET metadata     = mem_artifacts.metadata || EXCLUDED.metadata,
			    storage_size = COALESCE(EXCLUDED.storage_size, mem_artifacts.storage_size),
			    updated_at   = NOW()
	`, c.filename, c.path, c.size, backfillMime(c.format), vpath, c.session, string(metaJSON))
	return err
}

type docPreviewResult struct {
	PDFPath   string `json:"pdf_path"`
	ThumbPath string `json:"thumb_path"`
}

// docPreviewRender asks the workspace to render a preview PDF + thumbnail for an
// existing file. Returns errFileGone on 404 so the caller skips missing files.
func docPreviewRender(ctx context.Context, wsURL, token, path string) (docPreviewResult, error) {
	var res docPreviewResult
	body, _ := json.Marshal(map[string]string{"path": path})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wsURL+"/docpreview", bytes.NewReader(body))
	if err != nil {
		return res, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 3 * time.Minute}).Do(req)
	if err != nil {
		return res, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		// Only OUR handler's 404 means the file is gone; a route-missing 404
		// (workspace not deployed with /docpreview) must NOT be mistaken for it,
		// or we'd skip every doc. Fall through to a generic error in that case
		// so the row is still recorded (just without a thumbnail).
		if bytes.Contains(raw, []byte("file not found")) {
			return res, errFileGone
		}
		return res, errors.New("docpreview endpoint unavailable (404) — workspace may not be deployed")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return res, fmt.Errorf("docpreview HTTP %d", resp.StatusCode)
	}
	_ = json.Unmarshal(raw, &res)
	return res, nil
}

func backfillMime(format string) string {
	switch format {
	case "xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case "docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case "pdf":
		return "application/pdf"
	case "md", "markdown":
		return "text/markdown"
	}
	return "application/octet-stream"
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func yesNo(s string) string {
	if s != "" {
		return "yes"
	}
	return "no"
}
