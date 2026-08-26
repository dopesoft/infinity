package llm

import (
	"strings"
	"testing"
)

// Why these tests exist: the attachment path failed silently for months
// because a file's CONTENT never reached the brain (metadata-only prompt
// stuffing). Every provider renderer must (a) put the attachment before the
// typed text, (b) never drop the Note, and (c) fall back to text + page
// images when the native form is unavailable. A regression here is the exact
// bug the boss reported ("the attachment has not landed").

func TestTextBlockCarriesNoteAndTruncation(t *testing.T) {
	a := Attachment{
		Name:      "mindset.pdf",
		MIME:      "application/pdf",
		Kind:      AttachmentDocument,
		PageCount: 12,
		Path:      "/workspace/uploads/abc/mindset.pdf",
		Text:      "page one",
		Truncated: true,
		Note:      "not mirrored",
	}
	out := a.TextBlock()
	for _, want := range []string{`name="mindset.pdf"`, `pages="12"`, `path="/workspace/uploads/abc/mindset.pdf"`, "[note: not mirrored]", "page one", "[truncated:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("TextBlock missing %q:\n%s", want, out)
		}
	}
}

func TestTextBlockStatesWhenNothingCouldBeRead(t *testing.T) {
	a := Attachment{Name: "scan.pdf", Kind: AttachmentDocument}
	if !strings.Contains(a.TextBlock(), "no text content could be read") {
		t.Fatalf("an unreadable document must say so, got:\n%s", a.TextBlock())
	}
	b := Attachment{Name: "scan.pdf", Kind: AttachmentDocument, Pages: [][]byte{{1}, {2}}}
	if !strings.Contains(b.TextBlock(), "2 page image(s)") {
		t.Fatalf("page images must be announced, got:\n%s", b.TextBlock())
	}
}

func TestInlineLimitsFollowVendorCaps(t *testing.T) {
	img := Attachment{Kind: AttachmentImage, MIME: "image/png", Data: make([]byte, 10)}
	if !img.InlineImageOK() {
		t.Fatal("small png must ride natively")
	}
	img.MIME = "image/heic"
	if img.InlineImageOK() {
		t.Fatal("heic is not accepted by any wired brain; must fall back to text")
	}
	img.MIME = "image/png"
	img.Data = make([]byte, MaxInlineImageBytes+1)
	if img.InlineImageOK() {
		t.Fatal("oversize image must not ride natively")
	}
	pdf := Attachment{Kind: AttachmentDocument, MIME: "application/pdf", Data: []byte("%PDF"), PageCount: MaxInlinePDFPages + 1}
	if pdf.InlinePDFOK() {
		t.Fatal("a PDF over the page cap must fall back to extracted text")
	}
	pdf.PageCount = 3
	if !pdf.InlinePDFOK() {
		t.Fatal("small PDF must ride natively")
	}
}

func TestResponsesUserContentPutsFilesBeforeText(t *testing.T) {
	m := Message{
		Role:    RoleUser,
		Content: "what does this say?",
		Attachments: []Attachment{
			{Name: "photo.png", MIME: "image/png", Kind: AttachmentImage, Data: []byte{1, 2, 3}},
			{Name: "doc.pdf", MIME: "application/pdf", Kind: AttachmentDocument, Data: []byte("%PDF"), Text: "hello"},
			{Name: "scan.pdf", MIME: "application/pdf", Kind: AttachmentDocument, Data: []byte("%PDF"), Pages: [][]byte{{9}}, PageMIME: "image/jpeg"},
		},
	}
	items := responsesUserContent(m)
	types := make([]string, 0, len(items))
	for _, it := range items {
		types = append(types, it["type"].(string))
	}
	// caption, image, doc text, scan text, scan page image, typed text
	want := []string{"input_text", "input_image", "input_text", "input_text", "input_image", "input_text"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("item order = %v, want %v", types, want)
	}
	if last := items[len(items)-1]["text"]; last != "what does this say?" {
		t.Fatalf("typed text must come last, got %v", last)
	}
	if !strings.HasPrefix(items[1]["image_url"].(string), "data:image/png;base64,") {
		t.Fatalf("image must be a data URL, got %v", items[1]["image_url"])
	}
	if !strings.Contains(items[2]["text"].(string), "hello") {
		t.Fatalf("PDF text must reach a brain without native PDF input, got %v", items[2]["text"])
	}
}

func TestResponsesUserContentFileOnly(t *testing.T) {
	m := Message{Role: RoleUser, Attachments: []Attachment{{Name: "a.txt", Kind: AttachmentText, Text: "x"}}}
	items := responsesUserContent(m)
	if len(items) != 1 || !strings.Contains(items[0]["text"].(string), "x") {
		t.Fatalf("file-only message must still carry the file, got %v", items)
	}
}

func TestOpenAIPartsAndAnthropicBlocksRenderNatively(t *testing.T) {
	m := Message{
		Role:    RoleUser,
		Content: "go",
		Attachments: []Attachment{
			{Name: "photo.jpg", MIME: "image/jpeg", Kind: AttachmentImage, Data: []byte{1}},
			{Name: "doc.pdf", MIME: "application/pdf", Kind: AttachmentDocument, Data: []byte("%PDF"), Text: "t"},
		},
	}
	parts := openaiUserParts(m)
	if len(parts) != 5 {
		t.Fatalf("openai parts = %d, want caption+image+caption+file+text", len(parts))
	}
	if parts[1].OfImageURL == nil || parts[3].OfFile == nil || parts[4].OfText == nil {
		t.Fatal("openai parts must be image / file / text in that order")
	}
	blocks := anthropicUserBlocks(m)
	if len(blocks) != 5 {
		t.Fatalf("anthropic blocks = %d, want caption+image+caption+document+text", len(blocks))
	}
	if blocks[1].OfImage == nil || blocks[3].OfDocument == nil || blocks[4].OfText == nil {
		t.Fatal("anthropic blocks must be image / document / text in that order")
	}
}

func TestAttachmentsMetaNeverCarriesBytes(t *testing.T) {
	meta := AttachmentsMeta([]Attachment{{ID: "id1", Name: "big.pdf", Kind: AttachmentDocument, Data: make([]byte, 1<<20), Text: "secret body"}})
	if len(meta) != 1 {
		t.Fatalf("meta = %v", meta)
	}
	for k := range meta[0] {
		if k == "data" || k == "text" || k == "pages" {
			t.Fatalf("hook payload must not carry %s", k)
		}
	}
	if meta[0]["id"] != "id1" {
		t.Fatal("hook payload must carry the id so hydrate can reload the file")
	}
}
