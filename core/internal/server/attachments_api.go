package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dopesoft/infinity/core/internal/attachments"
)

// Chat attachments: Studio uploads the bytes here FIRST, then references the
// returned ids on the WS `message` / `steer` frame. Core resolves the ids into
// native multimodal blocks for the brain (see ws.go resolveAttachments) and
// persists the metadata on the turn so the transcript survives reload and a
// Core restart. Both routes sit behind the JWT middleware; Studio fetches the
// raw bytes with authedFetch and renders them through an object URL.

const (
	// uploadRequestCap bounds one multipart request (several files).
	uploadRequestCap = 64 << 20
	uploadMemoryCap  = 8 << 20
)

type attachmentDTO struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	MimeType      string `json:"mime_type,omitempty"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	PreviewURL    string `json:"preview_url,omitempty"`
	StoragePath   string `json:"storage_path,omitempty"`
	ExtractStatus string `json:"extract_status,omitempty"`
	ExtractError  string `json:"extract_error,omitempty"`
	PageCount     int    `json:"page_count,omitempty"`
	TextPreview   string `json:"text_preview,omitempty"`
}

func attachmentToDTO(a *attachments.Attachment) attachmentDTO {
	dto := attachmentDTO{
		ID:            a.ID,
		Name:          a.Name,
		MimeType:      a.MIME,
		SizeBytes:     a.SizeBytes,
		StoragePath:   a.WorkspacePath,
		ExtractStatus: a.ExtractStatus,
		ExtractError:  a.ExtractError,
		PageCount:     a.PageCount,
		TextPreview:   a.TextPreview(),
	}
	if a.IsImage() {
		dto.PreviewURL = a.RawURL()
	}
	return dto
}

type uploadFailure struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

// handleAttachmentUpload accepts multipart/form-data with `session_id` and one
// or more `file` parts. Returns every stored attachment plus a per-file
// failure list; the request is 4xx only when nothing was stored.
//
//	POST /api/attachments/upload
func (s *Server) handleAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.attachments == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "attachment store not configured"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, uploadRequestCap)
	if err := r.ParseMultipartForm(uploadMemoryCap); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
				"error": fmt.Sprintf("upload is over the %d MB request limit", uploadRequestCap>>20),
			})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "multipart form expected: " + err.Error()})
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id required"})
		return
	}
	files := append([]*multipartFile{}, collectFiles(r, "file")...)
	files = append(files, collectFiles(r, "files")...)
	if len(files) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no file parts"})
		return
	}

	var stored []attachmentDTO
	var failed []uploadFailure
	for _, f := range files {
		data, err := f.read()
		if err != nil {
			failed = append(failed, uploadFailure{Name: f.name, Error: err.Error()})
			continue
		}
		att, err := s.attachments.Save(r.Context(), sessionID, f.name, f.mime, data)
		if err != nil {
			log.Printf("attachments: save %s: %v", f.name, err)
			failed = append(failed, uploadFailure{Name: f.name, Error: err.Error()})
			continue
		}
		stored = append(stored, attachmentToDTO(att))
	}
	status := http.StatusOK
	if len(stored) == 0 {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, map[string]any{
		"attachments": stored,
		"failed":      failed,
	})
}

type multipartFile struct {
	name string
	mime string
	open func() (io.ReadCloser, error)
}

func (f *multipartFile) read() ([]byte, error) {
	rc, err := f.open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, attachments.MaxUploadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > attachments.MaxUploadBytes {
		return nil, fmt.Errorf("%s is over the %d MB per-file limit", f.name, attachments.MaxUploadBytes>>20)
	}
	return data, nil
}

func collectFiles(r *http.Request, field string) []*multipartFile {
	if r.MultipartForm == nil {
		return nil
	}
	var out []*multipartFile
	for _, fh := range r.MultipartForm.File[field] {
		fh := fh
		out = append(out, &multipartFile{
			name: fh.Filename,
			mime: fh.Header.Get("Content-Type"),
			open: func() (io.ReadCloser, error) { return fh.Open() },
		})
	}
	return out
}

// handleAttachmentRaw streams an attachment's bytes (upload or rasterized
// page) inline with its MIME type.
//
//	GET /api/attachments/{id}/raw
func (s *Server) handleAttachmentRaw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if s.attachments == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "attachment store not configured"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, attachments.RawURLPrefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[1] != "raw" {
		http.NotFound(w, r)
		return
	}
	data, att, err := s.attachments.Bytes(r.Context(), parts[0])
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "attachment not found"})
		return
	}
	ct := att.MIME
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Disposition", "inline; filename="+strconv.Quote(filepath.Base(att.Name)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}
