package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DocumentCreate renders a structured spec into a real .xlsx / .docx / .pptx
// / .pdf / .md file via the workspace bridge's /docgen endpoint, which
// dispatches to baked helpers (openpyxl, docx-js, pptxgenjs, reportlab —
// modeling Claude's file-ops skill stack). The agent passes structured
// content; it never hand-writes Office-format code (that's the jank).
//
// Generic building block (Rule #1): no per-document judgment here. What goes
// IN a report/sheet/deck lives in the make-report / make-spreadsheet /
// make-deck skills. Doc-gen is cloud-only (the helpers + LibreOffice live in
// the workspace image), so this targets the workspace bridge directly rather
// than the per-session Mac/Cloud router.
type DocumentCreate struct {
	url    string
	token  string
	client *http.Client
	// Emit, when set, fires after a successful render so Studio opens the
	// document in a new tab. Late-bound from serve.go to the server's
	// per-session broadcaster. nil-safe (doc still generates without it).
	Emit func(sessionID, format, filename, path, markdown, pdfPath string, bytes int64)
}

// NewDocumentCreate returns the tool, or nil when no workspace URL is
// configured (doc-gen disabled). Wired from serve.go with the same cloud
// workspace URL + token the bridge uses.
func NewDocumentCreate(workspaceURL, token string) *DocumentCreate {
	workspaceURL = strings.TrimRight(strings.TrimSpace(workspaceURL), "/")
	if workspaceURL == "" {
		return nil
	}
	return &DocumentCreate{
		url:    workspaceURL,
		token:  strings.TrimSpace(token),
		client: &http.Client{Timeout: 3 * time.Minute},
	}
}

func (d *DocumentCreate) Name() string { return "document_create" }

func (d *DocumentCreate) Description() string {
	return "Generate a real document file — Excel (.xlsx), Word (.docx), PowerPoint (.pptx), PDF (.pdf), or Markdown (.md) — from structured content. Pass `format`, a `filename`, and a `content` object shaped for that format (see below). The file is written to the cloud workspace and a rendered/downloadable view opens in Studio. Use this for any 'make me a spreadsheet/report/deck/doc' task; pair it with web-browsing to turn scraped data into a deliverable."
}

func (d *DocumentCreate) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"format": map[string]any{
				"type":        "string",
				"enum":        []string{"xlsx", "docx", "pptx", "pdf", "md"},
				"description": "Output format.",
			},
			"filename": map[string]any{
				"type":        "string",
				"description": "Output filename incl. extension, e.g. \"frisco-plumbers.xlsx\". Lands in the cloud workspace.",
			},
			"content": map[string]any{
				"type": "object",
				"description": "Structured content for the format:\n" +
					"• xlsx → {\"sheets\":[{\"name\":\"Leads\",\"columns\":[\"Name\",\"Phone\"],\"rows\":[[\"ABC\",\"469-555-0142\"]]}]} (or a single {\"columns\",\"rows\"}).\n" +
					"• docx/pdf → {\"title\":\"...\",\"subtitle\":\"...\",\"blocks\":[{\"type\":\"heading\",\"level\":1,\"text\":\"...\"},{\"type\":\"paragraph\",\"text\":\"...\"},{\"type\":\"bullets\",\"items\":[\"...\"]},{\"type\":\"table\",\"columns\":[...],\"rows\":[[...]]}]}.\n" +
					"• pptx → {\"title\":\"...\",\"subtitle\":\"...\",\"slides\":[{\"title\":\"...\",\"bullets\":[\"...\"]}]}.\n" +
					"• md → {\"markdown\":\"# Report...\"}.",
			},
			"also_pdf": map[string]any{
				"type":        "boolean",
				"description": "For docx/pptx/xlsx: also render a sibling .pdf (via LibreOffice) for easy viewing. Default false.",
			},
		},
		"required": []string{"format", "filename", "content"},
	}
}

// ReadOnly: document_create writes a NEW file in the cloud workspace volume —
// no existing data is mutated or destroyed, so it runs unattended (no gate).
func (d *DocumentCreate) ReadOnly() bool { return true }

type docgenResult struct {
	Path     string `json:"path"`
	Format   string `json:"format"`
	Bytes    int64  `json:"bytes"`
	PDFPath  string `json:"pdf_path,omitempty"`
	PDFError string `json:"pdf_error,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (d *DocumentCreate) Execute(ctx context.Context, input map[string]any) (string, error) {
	format, _ := input["format"].(string)
	filename, _ := input["filename"].(string)
	if strings.TrimSpace(format) == "" || strings.TrimSpace(filename) == "" {
		return "", errors.New("format and filename are required")
	}
	content := input["content"]
	if content == nil {
		content = map[string]any{}
	}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return "", fmt.Errorf("content must be a JSON object: %w", err)
	}
	alsoPDF, _ := input["also_pdf"].(bool)

	body, _ := json.Marshal(map[string]any{
		"format":   strings.ToLower(strings.TrimSpace(format)),
		"filename": strings.TrimSpace(filename),
		"content":  json.RawMessage(contentJSON),
		"also_pdf": alsoPDF,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url+"/docgen", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if d.token != "" {
		req.Header.Set("Authorization", "Bearer "+d.token)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("docgen request failed (is the workspace bridge up?): %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e docgenResult
		_ = json.Unmarshal(raw, &e)
		if e.Error != "" {
			return "", fmt.Errorf("docgen: %s", e.Error)
		}
		return "", fmt.Errorf("docgen: HTTP %d", resp.StatusCode)
	}
	var res docgenResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}

	// Open it in a new Studio tab. For md/report formats the markdown rides
	// the event so it renders inline with no fetch; binaries download via the
	// cloud-direct proxy.
	if d.Emit != nil {
		markdown := ""
		if strings.EqualFold(strings.TrimSpace(format), "md") {
			if m, ok := content.(map[string]any); ok {
				markdown, _ = m["markdown"].(string)
			}
		}
		d.Emit(SessionIDFromContext(ctx), res.Format, strings.TrimSpace(filename), res.Path, markdown, res.PDFPath, res.Bytes)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Created %s (%s, %d bytes) at %s\n", filename, res.Format, res.Bytes, res.Path)
	if res.PDFPath != "" {
		fmt.Fprintf(&b, "PDF preview: %s\n", res.PDFPath)
	}
	b.WriteString("The file is in the workspace and available to view/download in Studio. " +
		"Index it with artifact_save (kind=\"document\") so it shows in the boss's artifacts.")
	return b.String(), nil
}
