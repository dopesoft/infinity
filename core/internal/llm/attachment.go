package llm

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// AttachmentKind is the provider-neutral shape of a file the boss handed the
// agent. Providers map it onto their own native blocks (image / document /
// input_file) and fall back to the labelled text rendering (TextBlock) when
// the native form is unsupported or over a limit.
type AttachmentKind string

const (
	// AttachmentImage carries raw image bytes (jpeg/png/gif/webp).
	AttachmentImage AttachmentKind = "image"
	// AttachmentDocument carries a PDF: raw bytes for brains that take PDFs
	// natively, plus the extracted text and (for scanned PDFs) rasterized
	// pages for brains that don't.
	AttachmentDocument AttachmentKind = "document"
	// AttachmentText carries text only (plain text files, office documents
	// after conversion, anything whose native bytes no brain can consume).
	AttachmentText AttachmentKind = "text"
)

// Inline limits, derived from the vendors' documented request caps
// (Anthropic: 10 MB base64 per image, 32 MB per request, 100 pages per PDF;
// OpenAI: 50 MB per file). Raw-byte caps sit under the base64 ceiling so
// the encoded payload stays legal.
const (
	MaxInlineImageBytes = 7 << 20  // ~9.5 MB base64, under Anthropic's 10 MB
	MaxInlinePDFBytes   = 24 << 20 // ~32 MB base64 request ceiling with headroom
	MaxInlinePDFPages   = 100
)

// Attachment is one file attached to a user message. Data/Text/Pages are
// deliberately excluded from JSON: a Message is marshalled into logs and
// hook payloads, and a 20 MB PDF must never ride along. Metadata does.
type Attachment struct {
	ID            string         `json:"id,omitempty"`
	Name          string         `json:"name"`
	MIME          string         `json:"mime_type,omitempty"`
	Kind          AttachmentKind `json:"kind"`
	SizeBytes     int64          `json:"size_bytes,omitempty"`
	PageCount     int            `json:"page_count,omitempty"`
	Path          string         `json:"storage_path,omitempty"`
	PreviewURL    string         `json:"preview_url,omitempty"`
	ExtractStatus string         `json:"extract_status,omitempty"`

	// Data is the raw file bytes (image or PDF). Nil for text-only.
	Data []byte `json:"-"`
	// Text is the extracted / plain text. May be truncated (see Truncated).
	Text      string `json:"-"`
	Truncated bool   `json:"-"`
	// Pages are rasterized page images (PageMIME) for a document whose
	// text layer was empty, so a brain without native PDF input still sees it.
	Pages    [][]byte `json:"-"`
	PageMIME string   `json:"-"`
	// Note is a plain-language caveat the model MUST see ("text extraction
	// failed: …", "workspace unreachable, file not mirrored"). It is never
	// dropped: every provider renders it inside the text block.
	Note string `json:"-"`
}

// inlineImageMIMEs is the intersection of image formats every wired brain
// accepts natively (Anthropic + OpenAI both document exactly these four).
var inlineImageMIMEs = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// InlineImageOK reports whether the attachment can ride as a native image
// block: right kind, accepted format, under the size cap.
func (a Attachment) InlineImageOK() bool {
	return a.Kind == AttachmentImage &&
		len(a.Data) > 0 &&
		len(a.Data) <= MaxInlineImageBytes &&
		inlineImageMIMEs[strings.ToLower(a.MIME)]
}

// InlinePDFOK reports whether the attachment can ride as a native PDF block.
func (a Attachment) InlinePDFOK() bool {
	return a.Kind == AttachmentDocument &&
		len(a.Data) > 0 &&
		len(a.Data) <= MaxInlinePDFBytes &&
		strings.EqualFold(a.MIME, "application/pdf") &&
		(a.PageCount == 0 || a.PageCount <= MaxInlinePDFPages)
}

// DataURL renders the raw bytes as a data: URL for providers that take one.
func (a Attachment) DataURL() string {
	mime := a.MIME
	if mime == "" {
		mime = "application/octet-stream"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(a.Data)
}

// PageDataURL renders one rasterized page as a data: URL.
func (a Attachment) PageDataURL(i int) string {
	mime := a.PageMIME
	if mime == "" {
		mime = "image/jpeg"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(a.Pages[i])
}

// TextBlock is the provider-neutral text rendering: a labelled block the
// model can quote and refer to by name. Used (a) for AttachmentText, (b) for
// documents on brains without native PDF input, (c) as the carrier for Note
// so a failed extraction is stated plainly instead of silently missing.
func (a Attachment) TextBlock() string {
	var b strings.Builder
	b.WriteString(`<attachment name="`)
	b.WriteString(strings.ReplaceAll(a.Name, `"`, "'"))
	b.WriteString(`"`)
	if a.MIME != "" {
		fmt.Fprintf(&b, ` type="%s"`, a.MIME)
	}
	if a.PageCount > 0 {
		fmt.Fprintf(&b, ` pages="%d"`, a.PageCount)
	}
	if a.Path != "" {
		fmt.Fprintf(&b, ` path="%s"`, a.Path)
	}
	b.WriteString(">\n")
	if a.Note != "" {
		b.WriteString("[note: ")
		b.WriteString(strings.TrimSpace(a.Note))
		b.WriteString("]\n")
	}
	text := strings.TrimRight(a.Text, "\n")
	switch {
	case text != "":
		b.WriteString(text)
		b.WriteString("\n")
		if a.Truncated {
			b.WriteString("[truncated: this is the opening portion of the file")
			if a.Path != "" {
				fmt.Fprintf(&b, "; the full text is at %s.txt on the workspace, read it with fs_read or bash_run", a.Path)
			}
			b.WriteString("]\n")
		}
	case len(a.Pages) > 0:
		fmt.Fprintf(&b, "[no text layer; %d page image(s) of this document follow]\n", len(a.Pages))
	case a.Kind == AttachmentImage:
		b.WriteString("[image]\n")
	default:
		b.WriteString("[no text content could be read from this file]\n")
	}
	b.WriteString("</attachment>")
	return b.String()
}

// AttachmentsMeta is the JSON-safe metadata of a set of attachments, the
// shape persisted in the UserPromptSubmit hook payload so the transcript
// (and a post-restart hydrate) can find the files again by id.
func AttachmentsMeta(atts []Attachment) []map[string]any {
	if len(atts) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(atts))
	for _, a := range atts {
		row := map[string]any{"name": a.Name, "kind": string(a.Kind)}
		if a.ID != "" {
			row["id"] = a.ID
		}
		if a.MIME != "" {
			row["mime_type"] = a.MIME
		}
		if a.SizeBytes > 0 {
			row["size_bytes"] = a.SizeBytes
		}
		if a.PageCount > 0 {
			row["page_count"] = a.PageCount
		}
		if a.Path != "" {
			row["storage_path"] = a.Path
		}
		if a.PreviewURL != "" {
			row["preview_url"] = a.PreviewURL
		}
		if a.ExtractStatus != "" {
			row["extract_status"] = a.ExtractStatus
		}
		out = append(out, row)
	}
	return out
}
