// Package attachments is the durable home for files the boss hands Jarvis in
// chat. It is a building block, not cognition: the store keeps the bytes in
// Postgres (mem_attachments), mirrors them to the cloud workspace so the agent
// can bash on them, extracts text / rasterizes scanned pages so EVERY wired
// brain can read them, indexes each one as a mem_artifacts row so the agent
// can find it again in a later turn, and converts a row into the
// provider-neutral llm.Attachment the agent loop ships to the model.
package attachments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/runs"
)

const (
	// MaxUploadBytes caps one file. Under every wired brain's per-file limit
	// (Anthropic 32 MB request, OpenAI 50 MB file) with headroom for base64.
	MaxUploadBytes = 25 << 20
	// maxTextChars caps the extracted text shipped inline per attachment
	// (~37k tokens). The full text stays next to the file on the workspace.
	maxTextChars = 150_000
	// previewChars is the transcript-side preview Studio shows on a chip.
	previewChars = 400
	// RawURLPrefix is the JWT-protected route that streams an attachment's
	// bytes back to Studio (image previews, "open file").
	RawURLPrefix = "/api/attachments/"
)

// Extraction statuses (mirror the CHECK constraint in migration 190).
const (
	StatusPending = "pending"
	StatusOK      = "ok"
	StatusEmpty   = "empty"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

var infoLog = log.New(os.Stdout, "", log.LstdFlags)

// Attachment is one mem_attachments row (bytes excluded; see Bytes).
type Attachment struct {
	ID            string
	SessionID     string
	Kind          string
	ParentID      string
	PageNo        int
	Name          string
	MIME          string
	SizeBytes     int64
	SHA256        string
	TextExtract   string
	ExtractStatus string
	ExtractError  string
	PageCount     int
	WorkspacePath string
	ArtifactID    string
	CreatedAt     time.Time
}

// RawURL is the Studio-facing URL for this attachment's bytes.
func (a *Attachment) RawURL() string { return RawURLPrefix + a.ID + "/raw" }

// IsImage reports whether the attachment is an image.
func (a *Attachment) IsImage() bool { return strings.HasPrefix(strings.ToLower(a.MIME), "image/") }

// TextPreview is the first few hundred characters of the extract, for chips.
func (a *Attachment) TextPreview() string {
	t := strings.TrimSpace(a.TextExtract)
	if len(t) <= previewChars {
		return t
	}
	return strings.TrimSpace(t[:previewChars]) + "…"
}

// Store persists attachments and runs extraction.
type Store struct {
	pool      *pgxpool.Pool
	extractor Extractor
}

// NewStore wires the store. extractor may be nil (bytes are still stored;
// nothing is extracted and every attachment carries a note saying so).
func NewStore(pool *pgxpool.Pool, extractor Extractor) *Store {
	return &Store{pool: pool, extractor: extractor}
}

// Save stores one uploaded file, indexes it as an artifact, mirrors it to the
// workspace and extracts what every brain will need. Extraction failures are
// recorded on the row (extract_status='failed') and on the run, never hidden,
// but they do not fail the upload: the bytes are safe and the model is told.
func (s *Store) Save(ctx context.Context, sessionID, name, mimeType string, data []byte) (*Attachment, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("attachments: store not configured")
	}
	if len(data) == 0 {
		return nil, errors.New("attachments: empty file")
	}
	if len(data) > MaxUploadBytes {
		return nil, fmt.Errorf("attachments: %s is %d MB; the limit is %d MB", name, len(data)>>20, MaxUploadBytes>>20)
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("attachments: session_id required")
	}
	name = SafeName(name)
	mimeType = resolveMIME(mimeType, name, data)
	sum := sha256.Sum256(data)

	att := &Attachment{
		SessionID:     sessionID,
		Kind:          "upload",
		Name:          name,
		MIME:          mimeType,
		SizeBytes:     int64(len(data)),
		SHA256:        hex.EncodeToString(sum[:]),
		ExtractStatus: StatusPending,
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mem_attachments (session_id, kind, name, mime, size_bytes, sha256, bytes, extract_status)
		VALUES ($1, 'upload', $2, $3, $4, $5, $6, 'pending')
		RETURNING id::text, created_at
	`, sessionID, name, mimeType, att.SizeBytes, att.SHA256, data).Scan(&att.ID, &att.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("attachments: insert: %w", err)
	}

	if artID, aerr := s.registerArtifact(ctx, att); aerr != nil {
		log.Printf("attachments: artifact index %s: %v", att.Name, aerr)
	} else {
		att.ArtifactID = artID
	}

	// Extraction under a tracked run so the /logs Runs lens shows it and a
	// failure goes red instead of vanishing into a warn line.
	_ = runs.Track(ctx, runs.KindAttachmentIngest, att.ID, "Reading "+att.Name, runs.SourceManual, func(rctx context.Context) error {
		return s.ingest(rctx, att, data)
	})
	return att, nil
}

// ingest runs the extractor and persists its result on the row.
func (s *Store) ingest(ctx context.Context, att *Attachment, data []byte) error {
	if s.extractor == nil {
		att.ExtractStatus = StatusSkipped
		att.ExtractError = "no extractor configured"
		return s.persistExtraction(ctx, att, nil, "")
	}
	res := s.extractor.Extract(ctx, Input{ID: att.ID, SessionID: att.SessionID, Name: att.Name, MIME: att.MIME, Data: data})
	att.WorkspacePath = res.WorkspacePath
	att.PageCount = res.PageCount
	att.TextExtract = res.Text
	att.ExtractStatus = res.Status
	var problems []string
	if res.Err != nil {
		att.ExtractStatus = StatusFailed
		problems = append(problems, res.Err.Error())
	}
	if res.MirrorErr != nil {
		problems = append(problems, "not mirrored to the workspace: "+res.MirrorErr.Error())
	}
	att.ExtractError = strings.Join(problems, "; ")
	if err := s.persistExtraction(ctx, att, res.Pages, res.PageMIME); err != nil {
		return err
	}
	if res.Err != nil {
		return res.Err
	}
	infoLog.Printf("attachments: %s (%s, %d bytes) status=%s pages=%d text=%d mirrored=%q",
		att.Name, att.MIME, att.SizeBytes, att.ExtractStatus, att.PageCount, len(att.TextExtract), att.WorkspacePath)
	return nil
}

func (s *Store) persistExtraction(ctx context.Context, att *Attachment, pages [][]byte, pageMIME string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_attachments
		   SET text_extract = NULLIF($2, ''),
		       extract_status = $3,
		       extract_error = NULLIF($4, ''),
		       page_count = NULLIF($5, 0),
		       workspace_path = NULLIF($6, ''),
		       updated_at = NOW()
		 WHERE id::text = $1
	`, att.ID, att.TextExtract, att.ExtractStatus, att.ExtractError, att.PageCount, att.WorkspacePath)
	if err != nil {
		return fmt.Errorf("attachments: persist extraction: %w", err)
	}
	if pageMIME == "" {
		pageMIME = "image/jpeg"
	}
	for i, page := range pages {
		if len(page) == 0 {
			continue
		}
		sum := sha256.Sum256(page)
		_, perr := s.pool.Exec(ctx, `
			INSERT INTO mem_attachments (session_id, kind, parent_id, page_no, name, mime, size_bytes, sha256, bytes, extract_status)
			VALUES ($1, 'page', $2::uuid, $3, $4, $5, $6, $7, $8, 'skipped')
		`, att.SessionID, att.ID, i+1, fmt.Sprintf("%s (page %d)", att.Name, i+1), pageMIME, len(page), hex.EncodeToString(sum[:]), page)
		if perr != nil {
			return fmt.Errorf("attachments: persist page %d: %w", i+1, perr)
		}
	}
	if att.ArtifactID != "" {
		_, _ = s.pool.Exec(ctx, `
			UPDATE mem_artifacts
			   SET metadata = metadata || $2::jsonb, updated_at = NOW()
			 WHERE id::text = $1
		`, att.ArtifactID, fmt.Sprintf(`{"extract_status":%q,"page_count":%d,"workspace_path":%q}`,
			att.ExtractStatus, att.PageCount, att.WorkspacePath))
	}
	return nil
}

// registerArtifact indexes the upload in mem_artifacts so artifact_get /
// artifact_list (the agent's existing "things I have" tools) can find it.
func (s *Store) registerArtifact(ctx context.Context, att *Attachment) (string, error) {
	vpath := "/uploads/" + shortSession(att.SessionID) + "/" + att.Name
	var sessionUUID any
	if _, err := uuid.Parse(att.SessionID); err == nil {
		sessionUUID = att.SessionID
	}
	meta := fmt.Sprintf(`{"attachment_id":%q,"mime":%q,"raw_url":%q}`, att.ID, att.MIME, att.RawURL())
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mem_artifacts
			(kind, name, description, storage_kind, storage_path, storage_size, storage_mime,
			 virtual_path, source_session_id, source_tool, metadata)
		VALUES
			($1, $2, $3, 'postgres', $4, $5, $6, $7, $8::uuid, 'chat_upload', $9::jsonb)
		ON CONFLICT (virtual_path) WHERE deleted_at IS NULL DO UPDATE
			SET storage_path = EXCLUDED.storage_path,
			    storage_size = EXCLUDED.storage_size,
			    storage_mime = EXCLUDED.storage_mime,
			    description  = EXCLUDED.description,
			    metadata     = EXCLUDED.metadata,
			    updated_at   = NOW()
		RETURNING id::text
	`, artifactKind(att.MIME, att.Name), att.Name, "Attached by the boss in chat", "attachment:"+att.ID,
		att.SizeBytes, att.MIME, vpath, sessionUUID, meta).Scan(&id)
	if err != nil {
		return "", err
	}
	_, _ = s.pool.Exec(ctx, `UPDATE mem_attachments SET artifact_id = $2::uuid WHERE id::text = $1`, att.ID, id)
	return id, nil
}

const selectCols = `id::text, session_id, kind, COALESCE(parent_id::text, ''), COALESCE(page_no, 0),
		name, mime, size_bytes, sha256, COALESCE(text_extract, ''), extract_status,
		COALESCE(extract_error, ''), COALESCE(page_count, 0), COALESCE(workspace_path, ''),
		COALESCE(artifact_id::text, ''), created_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAttachment(r rowScanner) (*Attachment, error) {
	var a Attachment
	if err := r.Scan(&a.ID, &a.SessionID, &a.Kind, &a.ParentID, &a.PageNo,
		&a.Name, &a.MIME, &a.SizeBytes, &a.SHA256, &a.TextExtract, &a.ExtractStatus,
		&a.ExtractError, &a.PageCount, &a.WorkspacePath, &a.ArtifactID, &a.CreatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

// Get loads one row (no bytes).
func (s *Store) Get(ctx context.Context, id string) (*Attachment, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("attachments: store not configured")
	}
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return nil, fmt.Errorf("attachments: bad id %q", id)
	}
	a, err := scanAttachment(s.pool.QueryRow(ctx, `SELECT `+selectCols+` FROM mem_attachments WHERE id::text = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("attachments: get %s: %w", id, err)
	}
	return a, nil
}

// GetByArtifact loads the attachment behind a mem_artifacts storage_path of
// the form "attachment:<id>".
func (s *Store) GetByArtifact(ctx context.Context, storagePath string) (*Attachment, error) {
	id := strings.TrimPrefix(strings.TrimSpace(storagePath), "attachment:")
	if id == storagePath {
		return nil, fmt.Errorf("attachments: %q is not an attachment storage path", storagePath)
	}
	return s.Get(ctx, id)
}

// Bytes streams an attachment's raw bytes (upload or page).
func (s *Store) Bytes(ctx context.Context, id string) ([]byte, *Attachment, error) {
	if s == nil || s.pool == nil {
		return nil, nil, errors.New("attachments: store not configured")
	}
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return nil, nil, fmt.Errorf("attachments: bad id %q", id)
	}
	var data []byte
	var a Attachment
	err := s.pool.QueryRow(ctx, `SELECT bytes, id::text, name, mime, size_bytes, kind FROM mem_attachments WHERE id::text = $1`, id).
		Scan(&data, &a.ID, &a.Name, &a.MIME, &a.SizeBytes, &a.Kind)
	if err != nil {
		return nil, nil, fmt.Errorf("attachments: bytes %s: %w", id, err)
	}
	return data, &a, nil
}

// ListForSession returns the uploads of a session, newest first.
func (s *Store) ListForSession(ctx context.Context, sessionID string, limit int) ([]*Attachment, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("attachments: store not configured")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT `+selectCols+` FROM mem_attachments
		WHERE session_id = $1 AND kind = 'upload' ORDER BY created_at DESC LIMIT $2`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) pages(ctx context.Context, parentID string) ([][]byte, string, error) {
	rows, err := s.pool.Query(ctx, `SELECT bytes, mime FROM mem_attachments
		WHERE parent_id::text = $1 AND kind = 'page' ORDER BY page_no ASC`, parentID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var pages [][]byte
	pageMIME := ""
	for rows.Next() {
		var b []byte
		var m string
		if err := rows.Scan(&b, &m); err != nil {
			return nil, "", err
		}
		pages = append(pages, b)
		if pageMIME == "" {
			pageMIME = m
		}
	}
	return pages, pageMIME, rows.Err()
}

// ToLLM loads everything the brain needs for one attachment: bytes for native
// blocks, extracted text for text-only brains, rasterized pages for scanned
// documents, plus the Note that states plainly what could NOT be read.
func (s *Store) ToLLM(ctx context.Context, id string) (llm.Attachment, error) {
	a, err := s.Get(ctx, id)
	if err != nil {
		return llm.Attachment{}, err
	}
	return s.toLLM(ctx, a)
}

func (s *Store) toLLM(ctx context.Context, a *Attachment) (llm.Attachment, error) {
	out := llm.Attachment{
		ID:            a.ID,
		Name:          a.Name,
		MIME:          a.MIME,
		SizeBytes:     a.SizeBytes,
		PageCount:     a.PageCount,
		Path:          a.WorkspacePath,
		ExtractStatus: a.ExtractStatus,
		Kind:          llm.AttachmentText,
	}
	if a.IsImage() {
		out.PreviewURL = a.RawURL()
	}
	var notes []string
	if a.ExtractError != "" {
		notes = append(notes, a.ExtractError)
	}
	if a.WorkspacePath == "" && a.ExtractError == "" {
		notes = append(notes, "not on the workspace volume (bridge was unreachable at upload time); the bytes are in the attachment store, artifact_get can re-mirror it")
	}

	switch Classify(a.MIME, a.Name) {
	case ClassImage:
		data, _, err := s.Bytes(ctx, a.ID)
		if err != nil {
			return out, err
		}
		out.Kind = llm.AttachmentImage
		out.Data = data
		if !out.InlineImageOK() {
			// Unsupported format (heic, tiff, svg…) or oversize: the model gets
			// the path and a plain statement instead of a silently missing image.
			out.Kind = llm.AttachmentText
			out.Data = nil
			notes = append(notes, fmt.Sprintf("image format %s (%d KB) can't be shown inline; convert it on the workspace with bash_run (e.g. `magick in out.jpg`) and view the result", a.MIME, a.SizeBytes>>10))
		}
	case ClassPDF:
		data, _, err := s.Bytes(ctx, a.ID)
		if err != nil {
			return out, err
		}
		out.Kind = llm.AttachmentDocument
		out.Data = data
		out.Text, out.Truncated = capText(a.TextExtract)
		if strings.TrimSpace(out.Text) == "" {
			pages, pageMIME, perr := s.pages(ctx, a.ID)
			if perr != nil {
				return out, perr
			}
			out.Pages, out.PageMIME = pages, pageMIME
		}
	default:
		out.Text, out.Truncated = capText(a.TextExtract)
	}
	out.Note = strings.Join(notes, "; ")
	return out, nil
}

// ToLLMMany resolves a set of ids best-effort. An id that cannot be loaded
// becomes a text attachment whose Note says so: the model is told, loudly,
// rather than the file quietly vanishing from the turn.
func (s *Store) ToLLMMany(ctx context.Context, ids []string) []llm.Attachment {
	out := make([]llm.Attachment, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		att, err := s.ToLLM(ctx, id)
		if err != nil {
			log.Printf("attachments: load %s: %v", id, err)
			out = append(out, llm.Attachment{
				ID:   id,
				Name: "attachment " + id,
				Kind: llm.AttachmentText,
				Note: "this attachment could not be loaded from the store: " + err.Error() + ". Tell the boss and ask him to re-attach it.",
			})
			continue
		}
		out = append(out, att)
	}
	return out
}

// EnsureMirrored re-runs the workspace mirror for an attachment that was
// uploaded while the bridge was down (artifact_get calls this lazily so the
// agent always gets a real path when the workspace is back).
func (s *Store) EnsureMirrored(ctx context.Context, a *Attachment) (string, error) {
	if a.WorkspacePath != "" {
		return a.WorkspacePath, nil
	}
	m, ok := s.extractor.(Mirrorer)
	if !ok || m == nil {
		return "", errors.New("attachments: no workspace mirror available")
	}
	data, _, err := s.Bytes(ctx, a.ID)
	if err != nil {
		return "", err
	}
	path, err := m.Mirror(ctx, Input{ID: a.ID, SessionID: a.SessionID, Name: a.Name, MIME: a.MIME, Data: data})
	if err != nil {
		return "", err
	}
	a.WorkspacePath = path
	_, _ = s.pool.Exec(ctx, `UPDATE mem_attachments SET workspace_path = $2, updated_at = NOW() WHERE id::text = $1`, a.ID, path)
	return path, nil
}

func capText(t string) (string, bool) {
	t = strings.TrimSpace(t)
	if len(t) <= maxTextChars {
		return t, false
	}
	cut := t[:maxTextChars]
	if i := strings.LastIndex(cut, "\n"); i > maxTextChars/2 {
		cut = cut[:i]
	}
	return cut, true
}

// SafeName reduces an upload's filename to a basename with no control
// characters or path separators, defaulting to "attachment".
func SafeName(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	var b strings.Builder
	for _, r := range name {
		if unicode.IsControl(r) || r == '/' || r == 0 {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." || out == ".." {
		return "attachment"
	}
	if len(out) > 180 {
		ext := filepath.Ext(out)
		out = out[:180-len(ext)] + ext
	}
	return out
}

func resolveMIME(declared, name string, data []byte) string {
	declared = strings.ToLower(strings.TrimSpace(declared))
	if i := strings.Index(declared, ";"); i > 0 {
		declared = strings.TrimSpace(declared[:i])
	}
	if declared != "" && declared != "application/octet-stream" {
		return declared
	}
	if ext := strings.ToLower(filepath.Ext(name)); ext != "" {
		if byExt := mime.TypeByExtension(ext); byExt != "" {
			if i := strings.Index(byExt, ";"); i > 0 {
				byExt = byExt[:i]
			}
			return byExt
		}
		if t, ok := extMIME[ext]; ok {
			return t
		}
	}
	sniffed := http.DetectContentType(data)
	if i := strings.Index(sniffed, ";"); i > 0 {
		sniffed = sniffed[:i]
	}
	return sniffed
}

// extMIME covers the extensions Go's mime package doesn't know on a
// distroless image (no /etc/mime.types).
var extMIME = map[string]string{
	".md": "text/markdown", ".markdown": "text/markdown", ".txt": "text/plain", ".csv": "text/csv",
	".tsv": "text/tab-separated-values", ".json": "application/json", ".yaml": "application/x-yaml",
	".yml": "application/x-yaml", ".toml": "application/toml", ".ini": "text/plain", ".sql": "application/sql",
	".ts": "text/typescript", ".tsx": "text/typescript", ".js": "text/javascript", ".jsx": "text/javascript",
	".py": "text/x-python", ".go": "text/x-go", ".rs": "text/x-rust", ".java": "text/x-java",
	".c": "text/x-c", ".cc": "text/x-c++", ".cpp": "text/x-c++", ".h": "text/x-c", ".hpp": "text/x-c++",
	".css": "text/css", ".html": "text/html", ".xml": "application/xml",
	".pdf": "application/pdf", ".rtf": "application/rtf",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".doc":  "application/msword", ".xls": "application/vnd.ms-excel", ".ppt": "application/vnd.ms-powerpoint",
	".odt": "application/vnd.oasis.opendocument.text", ".ods": "application/vnd.oasis.opendocument.spreadsheet",
	".odp":  "application/vnd.oasis.opendocument.presentation",
	".webp": "image/webp", ".heic": "image/heic", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
}

func artifactKind(mimeType, name string) string {
	switch Classify(mimeType, name) {
	case ClassImage:
		return "image"
	case ClassPDF:
		return "document"
	case ClassOffice:
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".xlsx" || ext == ".xls" || ext == ".ods" {
			return "dataset"
		}
		return "document"
	case ClassText:
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".csv" || ext == ".tsv" || ext == ".json" || ext == ".jsonl" {
			return "dataset"
		}
		return "document"
	}
	m := strings.ToLower(mimeType)
	switch {
	case strings.HasPrefix(m, "audio/"):
		return "audio"
	case strings.HasPrefix(m, "video/"):
		return "video"
	}
	return "other"
}

func shortSession(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 8 {
		return id[:8]
	}
	if id == "" {
		return "session"
	}
	return id
}
