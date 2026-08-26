package attachments

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	pdflib "github.com/dslipak/pdf"

	"github.com/dopesoft/infinity/core/internal/bridge"
)

// Class is the coarse handling class of an upload.
type Class string

const (
	ClassImage  Class = "image"
	ClassPDF    Class = "pdf"
	ClassOffice Class = "office"
	ClassText   Class = "text"
	ClassOther  Class = "other"
)

var officeExt = map[string]bool{
	".docx": true, ".doc": true, ".xlsx": true, ".xls": true, ".pptx": true, ".ppt": true,
	".odt": true, ".ods": true, ".odp": true, ".rtf": true,
}

var textExt = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".json": true, ".jsonl": true, ".csv": true, ".tsv": true,
	".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".py": true, ".go": true, ".rs": true,
	".java": true, ".c": true, ".cc": true, ".cpp": true, ".h": true, ".hpp": true, ".css": true,
	".html": true, ".htm": true, ".xml": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true,
	".sql": true, ".sh": true, ".env": true, ".log": true,
}

// Classify decides how a file is read, by MIME first and extension second.
func Classify(mimeType, name string) Class {
	m := strings.ToLower(strings.TrimSpace(mimeType))
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case strings.HasPrefix(m, "image/"):
		return ClassImage
	case m == "application/pdf" || ext == ".pdf":
		return ClassPDF
	case officeExt[ext],
		strings.HasPrefix(m, "application/vnd.openxmlformats-officedocument"),
		strings.HasPrefix(m, "application/vnd.oasis.opendocument"),
		m == "application/msword", m == "application/vnd.ms-excel", m == "application/vnd.ms-powerpoint",
		m == "application/rtf", m == "text/rtf":
		return ClassOffice
	case strings.HasPrefix(m, "text/"),
		m == "application/json", m == "application/xml", m == "application/x-yaml",
		m == "application/toml", m == "application/sql", m == "application/javascript",
		textExt[ext]:
		return ClassText
	}
	return ClassOther
}

// Input is one file to extract.
type Input struct {
	ID        string
	SessionID string
	Name      string
	MIME      string
	Data      []byte
}

// Result is what the extractor found. Status uses the Status* constants.
// Err is a REAL failure of a step that should have worked (pdftotext died,
// office conversion failed); MirrorErr is the workspace copy failing. Both
// surface to the model and the boss; neither is folded into "empty".
type Result struct {
	Text          string
	PageCount     int
	Pages         [][]byte
	PageMIME      string
	WorkspacePath string
	Status        string
	Err           error
	MirrorErr     error
}

// Extractor turns an upload into text / pages the brain can consume.
type Extractor interface {
	Extract(ctx context.Context, in Input) Result
}

// Mirrorer is the optional "copy the bytes to the workspace volume" half,
// used lazily when an upload happened while the bridge was down.
type Mirrorer interface {
	Mirror(ctx context.Context, in Input) (string, error)
}

// ── workspace extractor ──────────────────────────────────────────────────

const (
	uploadsRoot = "/workspace/uploads"
	// rasterPages caps how many pages of a scanned PDF are shipped as images.
	rasterPages = 8
	// rasterDPI keeps a page around 900×1200 px: legible, ~1.5k tokens each.
	rasterDPI = 110
	// sparseCharsPerPage: below this average the PDF is treated as scanned
	// (no usable text layer) and pages are rasterized so the brain can SEE it.
	sparseCharsPerPage = 40
	// bashTimeoutSec stays under the bridge HTTP client's 60 s ceiling.
	bashTimeoutSec = 55
)

// WorkspaceExtractor mirrors the file to Jarvis's cloud workspace and uses
// the toolchain baked into that image (poppler, LibreOffice) to read it.
// When the cloud bridge is unreachable it degrades to the local extractor
// and reports the mirror failure instead of pretending.
type WorkspaceExtractor struct {
	router   *bridge.Router
	fallback Extractor
}

// NewWorkspaceExtractor wires the cloud-backed extractor with a local fallback.
func NewWorkspaceExtractor(router *bridge.Router) *WorkspaceExtractor {
	return &WorkspaceExtractor{router: router, fallback: LocalExtractor{}}
}

func (w *WorkspaceExtractor) cloud(ctx context.Context) (bridge.Bridge, error) {
	if w == nil || w.router == nil {
		return nil, errors.New("no bridge router configured")
	}
	b, _, err := w.router.For(ctx, bridge.PrefCloud)
	if err != nil {
		return nil, err
	}
	if b == nil || b.Name() != bridge.KindCloud {
		return nil, errors.New("cloud workspace bridge is not available")
	}
	return b, nil
}

// Mirror copies the bytes to /workspace/uploads/<session>/<id8>-<name>.
// The bridge's /fs/save is text-only (JSON string), so the bytes travel as
// base64 and are decoded in place; no workspace-image change required.
func (w *WorkspaceExtractor) Mirror(ctx context.Context, in Input) (string, error) {
	b, err := w.cloud(ctx)
	if err != nil {
		return "", err
	}
	return mirror(ctx, b, in)
}

func mirror(ctx context.Context, b bridge.Bridge, in Input) (string, error) {
	dir := uploadsRoot + "/" + shortSession(in.SessionID)
	fname := shortSession(in.ID) + "-" + SafeName(in.Name)
	path := dir + "/" + fname
	body, status, ok := b.Post(ctx, "/fs/save", map[string]any{
		"path":    path + ".b64",
		"content": base64.StdEncoding.EncodeToString(in.Data),
	})
	if !ok || status >= 300 {
		return "", fmt.Errorf("workspace /fs/save %s: status %d %s", path, status, trimBody(body))
	}
	out, code, err := runBash(ctx, b, dir, fmt.Sprintf("base64 -d %s > %s && rm -f %s", shq(fname+".b64"), shq(fname), shq(fname+".b64")))
	if err != nil {
		return "", fmt.Errorf("workspace decode %s: %w", path, err)
	}
	if code != 0 {
		return "", fmt.Errorf("workspace decode %s: exit %d: %s", path, code, strings.TrimSpace(out))
	}
	return path, nil
}

// Extract implements Extractor.
func (w *WorkspaceExtractor) Extract(ctx context.Context, in Input) Result {
	class := Classify(in.MIME, in.Name)
	b, err := w.cloud(ctx)
	if err != nil {
		res := w.fallback.Extract(ctx, in)
		res.MirrorErr = err
		return res
	}
	res := Result{Status: StatusSkipped}
	path, merr := mirror(ctx, b, in)
	if merr != nil {
		res.MirrorErr = merr
	} else {
		res.WorkspacePath = path
	}

	switch class {
	case ClassText:
		text, terr := decodeText(in.Data)
		if terr != nil {
			res.Status, res.Err = StatusFailed, terr
			return res
		}
		res.Text = text
		res.Status = statusForText(text)
		return res
	case ClassImage, ClassOther:
		// Nothing to extract; the image rides natively, "other" is only
		// reachable via the workspace path.
		return res
	}
	if merr != nil {
		// pdf/office need the file on the volume; fall back to local reading
		// and keep the mirror error visible.
		local := w.fallback.Extract(ctx, in)
		local.MirrorErr = merr
		return local
	}

	pdfPath := path
	if class == ClassOffice {
		converted, cerr := officeToPDF(ctx, b, path)
		if cerr != nil {
			res.Status, res.Err = StatusFailed, cerr
			return res
		}
		pdfPath = converted
	}
	text, pages, perr := pdfExtract(ctx, b, pdfPath, class == ClassPDF)
	if perr != nil {
		res.Status, res.Err = StatusFailed, perr
		return res
	}
	res.Text = text.text
	res.PageCount = text.pages
	res.Status = statusForText(text.text)
	if len(pages) > 0 {
		res.Pages, res.PageMIME = pages, "image/jpeg"
	}
	return res
}

type pdfText struct {
	text  string
	pages int
}

// pdfExtract runs pdfinfo + pdftotext next to the file (leaving <file>.txt on
// the volume for the agent) and rasterizes the opening pages when the text
// layer is effectively empty (scanned document).
func pdfExtract(ctx context.Context, b bridge.Bridge, path string, rasterize bool) (pdfText, [][]byte, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	out := pdfText{}

	info, code, err := runBash(ctx, b, dir, fmt.Sprintf("pdfinfo %s 2>/dev/null | awk '/^Pages:/{print $2}'", shq(base)))
	if err == nil && code == 0 {
		out.pages, _ = strconv.Atoi(strings.TrimSpace(info))
	}

	txtName := base + ".txt"
	o, code, err := runBash(ctx, b, dir, fmt.Sprintf("pdftotext -layout %s %s", shq(base), shq(txtName)))
	if err != nil {
		return out, nil, fmt.Errorf("pdftotext: %w", err)
	}
	if code != 0 {
		return out, nil, fmt.Errorf("pdftotext exit %d: %s", code, strings.TrimSpace(o))
	}
	raw, status, ok := b.Get(ctx, "/fs/raw?path="+url.QueryEscape(dir+"/"+txtName))
	if !ok || status >= 300 {
		return out, nil, fmt.Errorf("read %s: status %d", txtName, status)
	}
	out.text = strings.TrimSpace(string(raw))

	if !rasterize {
		return out, nil, nil
	}
	perPage := len(out.text)
	if out.pages > 0 {
		perPage = len(out.text) / out.pages
	}
	if perPage >= sparseCharsPerPage {
		return out, nil, nil
	}
	pagesDir := base + ".pages"
	cmd := fmt.Sprintf("mkdir -p %s && pdftoppm -r %d -jpeg -jpegopt quality=72 -f 1 -l %d %s %s/p && ls %s",
		shq(pagesDir), rasterDPI, rasterPages, shq(base), shq(pagesDir), shq(pagesDir))
	o, code, err = runBash(ctx, b, dir, cmd)
	if err != nil {
		return out, nil, fmt.Errorf("pdftoppm: %w", err)
	}
	if code != 0 {
		return out, nil, fmt.Errorf("pdftoppm exit %d: %s", code, strings.TrimSpace(o))
	}
	var names []string
	for _, line := range strings.Split(o, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ".jpg") {
			names = append(names, line)
		}
	}
	sort.Strings(names)
	var pages [][]byte
	for _, n := range names {
		if len(pages) >= rasterPages {
			break
		}
		img, status, ok := b.Get(ctx, "/fs/raw?path="+url.QueryEscape(dir+"/"+pagesDir+"/"+n))
		if !ok || status >= 300 || len(img) == 0 {
			return out, pages, fmt.Errorf("read page image %s: status %d", n, status)
		}
		pages = append(pages, img)
	}
	if len(pages) == 0 {
		return out, nil, errors.New("pdftoppm produced no page images")
	}
	return out, pages, nil
}

// officeToPDF converts an office document with headless LibreOffice and
// returns the PDF path.
func officeToPDF(ctx context.Context, b bridge.Bridge, path string) (string, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	convDir := ".conv"
	pdfName := strings.TrimSuffix(base, filepath.Ext(base)) + ".pdf"
	cmd := fmt.Sprintf("mkdir -p %s && soffice --headless --convert-to pdf --outdir %s %s >/dev/null 2>&1; test -f %s",
		shq(convDir), shq(convDir), shq(base), shq(convDir+"/"+pdfName))
	o, code, err := runBash(ctx, b, dir, cmd)
	if err != nil {
		return "", fmt.Errorf("libreoffice convert: %w", err)
	}
	if code != 0 {
		return "", fmt.Errorf("libreoffice could not convert %s to PDF: %s", base, strings.TrimSpace(o))
	}
	return dir + "/" + convDir + "/" + pdfName, nil
}

func runBash(ctx context.Context, b bridge.Bridge, cwd, cmd string) (string, int, error) {
	body, status, ok := b.Post(ctx, "/bash", map[string]any{"cmd": cmd, "cwd": cwd, "timeout_sec": bashTimeoutSec})
	if !ok {
		return "", -1, errors.New("workspace /bash unreachable")
	}
	if status >= 300 {
		return "", -1, fmt.Errorf("workspace /bash status %d: %s", status, trimBody(body))
	}
	var resp struct {
		Output   string `json:"output"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", -1, fmt.Errorf("workspace /bash: bad response: %w", err)
	}
	return resp.Output, resp.ExitCode, nil
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func trimBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func statusForText(t string) string {
	if strings.TrimSpace(t) == "" {
		return StatusEmpty
	}
	return StatusOK
}

func decodeText(data []byte) (string, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if !utf8.Valid(data) {
		return "", errors.New("file is not valid UTF-8 text")
	}
	return strings.TrimSpace(string(data)), nil
}

// ── local fallback ───────────────────────────────────────────────────────

// LocalExtractor reads what Core can read on its own: UTF-8 text and the
// text layer of a PDF (pure Go). It can't rasterize or convert office files;
// those come back as a plain "workspace unreachable" failure.
type LocalExtractor struct{}

// Extract implements Extractor.
func (LocalExtractor) Extract(_ context.Context, in Input) Result {
	res := Result{Status: StatusSkipped}
	switch Classify(in.MIME, in.Name) {
	case ClassText:
		text, err := decodeText(in.Data)
		if err != nil {
			res.Status, res.Err = StatusFailed, err
			return res
		}
		res.Text, res.Status = text, statusForText(text)
	case ClassPDF:
		text, pages, err := localPDFText(in.Data)
		res.PageCount = pages
		if err != nil {
			res.Status, res.Err = StatusFailed, fmt.Errorf("cloud workspace unreachable and the local PDF reader failed: %w", err)
			return res
		}
		res.Text, res.Status = text, statusForText(text)
		if res.Status == StatusEmpty {
			res.Err = errors.New("no text layer and the cloud workspace (needed to rasterize pages) is unreachable")
			res.Status = StatusFailed
		}
	case ClassOffice:
		res.Status, res.Err = StatusFailed, errors.New("office documents are converted on the cloud workspace, which is unreachable right now")
	case ClassImage, ClassOther:
		// image rides natively; other has nothing to read locally.
	}
	return res
}

func localPDFText(data []byte) (text string, pages int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("pdf reader panicked: %v", r)
		}
	}()
	rd, err := pdflib.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", 0, err
	}
	pages = rd.NumPage()
	pr, err := rd.GetPlainText()
	if err != nil {
		return "", pages, err
	}
	raw, err := io.ReadAll(pr)
	if err != nil {
		return "", pages, err
	}
	return strings.TrimSpace(string(raw)), pages, nil
}
