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
	// document in a new tab AND the server persists it as a session artifact.
	// Late-bound from serve.go to the server's per-session broadcaster.
	// nil-safe (doc still generates without it).
	Emit func(sessionID, format, filename, path, markdown, pdfPath, thumbPath, htmlPath string, bytes int64)
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
					"• pptx → a THEMED deck. A branded default theme (logo, brand palette, title chrome) is applied automatically — never hand-style slides. Shape: {\"title\":\"...\",\"subtitle\":\"...\" (→ branded cover),\"slides\":[{\"layout\":\"<type>\", ...fields}]}. OPTIONAL top-level \"theme\": pass a built-in name to recolor the whole deck — \"dopesoft\" (default brand blue), \"emerald\", \"royal\", \"crimson\", \"amber\", \"slate\", or \"midnight\" (dark) — or a partial object that merges over the default (e.g. {\"theme\":{\"primary\":\"FF5A1F\"}}). Use a non-default theme only when the boss asks for a specific look/color/dark mode; otherwise omit it. Pick the LAYOUT whose data shape matches each slide; fields are skipped when absent. Layouts: " +
						"cover{title,subtitle,image} · agenda{title,items[]} · section{title,image} · bullets{title,tagline,bullets[]|body,image} · stats{title,tagline,bullets[],best_for,stats:[{value,label,caption}]} · statement{eyebrow,text} · number_grid{title,items:[{title,text}]} · timeline{title,steps:[{title,text}]} · metrics{title,lead,metrics:[{value,label,caption}]} · team{title,intro,members:[{name,role,photo}]} · profile{role,name,bio,skills:[{label,pct}],photo} · chart{title,lead,chart:{type:\"bar\"|\"line\",categories:[],series:[{name,values:[]}]}} · thankyou{title}.\n" +
					"• md → {\"markdown\":\"# Report...\"}.",
			},
			"also_pdf": map[string]any{
				"type":        "boolean",
				"description": "Office formats (xlsx/docx/pptx) ALREADY render inline in the boss's canvas column via an auto-generated sibling PDF — you do NOT need to set this. Only pass false to skip the PDF render (e.g. a throwaway sheet you don't need to preview).",
			},
		},
		"required": []string{"format", "filename", "content"},
	}
}

// ReadOnly: document_create writes a NEW file in the cloud workspace volume —
// no existing data is mutated or destroyed, so it runs unattended (no gate).
func (d *DocumentCreate) ReadOnly() bool { return true }

type docgenResult struct {
	Path      string `json:"path"`
	Format    string `json:"format"`
	Bytes     int64  `json:"bytes"`
	PDFPath   string `json:"pdf_path,omitempty"`
	PDFError  string `json:"pdf_error,omitempty"`
	ThumbPath string `json:"thumb_path,omitempty"`
	HTMLPath  string `json:"html_path,omitempty"`
	Error     string `json:"error,omitempty"`
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

	// Inline preview in the boss's canvas column is a MECHANIC, not a model
	// choice (Rule #1b). An office file with no sibling PDF falls back to a bare
	// download card — NOT the inline render the boss expects (and the whole
	// reason the canvas column exists). So default also_pdf=true for every
	// office format: LibreOffice renders a sibling .pdf that DocumentTab shows
	// inline. The model used to have to remember this per-format and the skill
	// only ever named docx/pptx — so spreadsheets silently shipped as a 404-ish
	// download card. Now it's guaranteed in code. pdf/md need no sibling (they
	// already render inline). An explicit also_pdf:false is still honored as an
	// escape hatch for callers that want the raw file without the conversion.
	fmtLower := strings.ToLower(strings.TrimSpace(format))
	alsoPDF := fmtLower == "xlsx" || fmtLower == "docx" || fmtLower == "pptx"
	if v, ok := input["also_pdf"].(bool); ok {
		alsoPDF = v
	}

	body, _ := json.Marshal(map[string]any{
		"format":   fmtLower,
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
		d.Emit(SessionIDFromContext(ctx), res.Format, strings.TrimSpace(filename), res.Path, markdown, res.PDFPath, res.ThumbPath, res.HTMLPath, res.Bytes)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Created %s (%s, %d bytes) at %s\n", filename, res.Format, res.Bytes, res.Path)
	if res.PDFPath != "" {
		fmt.Fprintf(&b, "PDF preview: %s\n", res.PDFPath)
	}
	b.WriteString("It is ALREADY OPEN and rendering inline in the boss's canvas column (a new document tab) — that is the deliverable. " +
		"Do NOT write a file path or a download link in your chat reply: there is no public URL and any link you invent will 404. " +
		"Just tell him in one line what you made. Then index it with artifact_save (kind=\"document\") so it also shows in his artifacts.")
	return b.String(), nil
}
