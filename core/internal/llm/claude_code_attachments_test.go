package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ── fakes ────────────────────────────────────────────────────────────────

// placingRunner is a brain whose box accepts files.
type placingRunner struct {
	got  map[string]int // id -> times placed
	fail error
}

func (r *placingRunner) Converse(context.Context, BrainTurn, chan<- StreamEvent) (Response, error) {
	return Response{}, nil
}

func (r *placingRunner) PlaceFile(_ context.Context, id, name string, data []byte) (string, error) {
	if r.fail != nil {
		return "", r.fail
	}
	if r.got == nil {
		r.got = map[string]int{}
	}
	r.got[id]++
	_ = data
	return "/tmp/inf-attach/" + id + "-" + name, nil
}

// plainRunner is a brain with no way to put a file anywhere.
type plainRunner struct{}

func (plainRunner) Converse(context.Context, BrainTurn, chan<- StreamEvent) (Response, error) {
	return Response{}, nil
}

func resumeAttachment() Attachment {
	return Attachment{
		ID:        "6677ff03",
		Kind:      AttachmentDocument,
		Name:      "KhayaMalabie-2026-Resume.pdf",
		MIME:      "application/pdf",
		PageCount: 3,
		Data:      []byte("%PDF-1.4 ..."),
		Text:      "KHAYA MALABIE\nHead of Product\nLed the platform rebuild that...",
	}
}

func screenshot() Attachment {
	return Attachment{
		ID:   "9ea7efde",
		Kind: AttachmentImage,
		Name: "Screenshot.png",
		MIME: "image/png",
		Data: []byte{0x89, 'P', 'N', 'G'},
	}
}

func brainWith(r BrainRunner) *ClaudeCode { return NewClaudeCode(r, nil, "") }

// ── the file that never arrived ──────────────────────────────────────────

// Why: on 2026-09-01 he attached his resume, the chip rendered, the upload
// landed, the PDF extracted to 12,163 characters, the turn recorded it - and
// Jarvis said "no attachment came through on this message". Every other brain
// reads Message.Attachments; this one rendered Content and nothing else, so
// the file was real everywhere except where it mattered.
func TestClaudeCodeBrain_SendsHisAttachments(t *testing.T) {
	c := brainWith(&placingRunner{})
	msgs := []Message{{
		Role:        RoleUser,
		Content:     "Also i attached my resume for you.",
		Attachments: []Attachment{resumeAttachment()},
	}}

	// BOTH render paths, because a resumed session sends only the last message
	// and a cold one renders the whole transcript. A file lost on either is a
	// file lost, and the resumed path is the likelier place to lose it.
	last, ok := c.lastUserMessage(context.Background(), msgs)
	if !ok {
		t.Fatal("his message must be sendable")
	}
	for name, got := range map[string]string{
		"resumed turn":    last,
		"full transcript": c.renderTranscript(context.Background(), msgs),
	} {
		if !strings.Contains(got, "Also i attached my resume") {
			t.Errorf("%s: lost what he typed:\n%s", name, got)
		}
		if !strings.Contains(got, "KhayaMalabie-2026-Resume.pdf") {
			t.Errorf("%s: the file is not named:\n%s", name, got)
		}
		if !strings.Contains(got, "KHAYA MALABIE") {
			t.Errorf("%s: the file's real TEXT must be there, not just its name:\n%s", name, got)
		}
	}
}

// Why: "i need every fuckin model i use on mac or cloud bridge to take fuckin
// image or files, ALWAYS". Claude Code takes a prompt, so an image cannot ride
// as a native block the way it does on every other brain. It CAN open a file,
// and its Read tool renders an image. So the bytes go to the box and the model
// is told where they are and to open them - a path it doesn't know to read is
// the same as no path at all.
func TestClaudeCodeBrain_PutsAnImageWhereItCanOpenIt(t *testing.T) {
	r := &placingRunner{}
	c := brainWith(r)
	got, ok := c.lastUserMessage(context.Background(), []Message{{
		Role:        RoleUser,
		Content:     "what's wrong with this screen",
		Attachments: []Attachment{screenshot()},
	}})
	if !ok {
		t.Fatal("a message with an image must be sendable")
	}
	if r.got["9ea7efde"] != 1 {
		t.Fatalf("the image must be placed on the box exactly once, got %d", r.got["9ea7efde"])
	}
	if !strings.Contains(got, "/tmp/inf-attach/9ea7efde-Screenshot.png") {
		t.Errorf("the model must be told where the file is:\n%s", got)
	}
	if !strings.Contains(got, "Read tool") {
		t.Errorf("and told to open it, or the path is useless:\n%s", got)
	}
}

// Why: a PDF whose text came out clean does not need the bytes shipped
// anywhere - the text IS the content and it is already in the block. Sending
// a 20MB file to the box on every turn to re-derive text we already have is
// waste, not thoroughness.
func TestClaudeCodeBrain_DoesNotShipAPdfItAlreadyRead(t *testing.T) {
	r := &placingRunner{}
	c := brainWith(r)
	c.lastUserMessage(context.Background(), []Message{{
		Role: RoleUser, Content: "resume", Attachments: []Attachment{resumeAttachment()},
	}})
	if len(r.got) != 0 {
		t.Fatalf("a readable PDF needs no file placed: %v", r.got)
	}
}

// Why: a scanned PDF with no text layer is exactly the case where the bytes
// ARE the only content. Skipping it because it is "a document" would silently
// hand him an answer about a file nobody opened.
func TestClaudeCodeBrain_ShipsAPdfItCouldNotRead(t *testing.T) {
	r := &placingRunner{}
	c := brainWith(r)
	scanned := resumeAttachment()
	scanned.Text = ""
	got, _ := c.lastUserMessage(context.Background(), []Message{{
		Role: RoleUser, Attachments: []Attachment{scanned},
	}})
	if r.got["6677ff03"] != 1 {
		t.Fatal("a PDF with no text layer must be placed so the model can open it")
	}
	if !strings.Contains(got, "Read tool") {
		t.Errorf("and the model must be told to open it:\n%s", got)
	}
}

// Why: empty-because-broken must never read as empty-because-fine. If the file
// cannot be placed, the model has to be told it never got it, or it will write
// a confident answer about something nobody looked at.
func TestClaudeCodeBrain_SaysSoWhenAFileCannotBeDelivered(t *testing.T) {
	for name, c := range map[string]*ClaudeCode{
		"the box refused":    brainWith(&placingRunner{fail: errors.New("mac is asleep")}),
		"no way to place it": brainWith(plainRunner{}),
	} {
		got, _ := c.lastUserMessage(context.Background(), []Message{{
			Role: RoleUser, Content: "look at this", Attachments: []Attachment{screenshot()},
		}})
		if !strings.Contains(got, "did not") && !strings.Contains(got, "could not") {
			t.Errorf("%s: the model must be told the file never arrived:\n%s", name, got)
		}
		if !strings.Contains(got, "tell the boss") {
			t.Errorf("%s: and told to say so rather than guess:\n%s", name, got)
		}
	}
}

// Why: an existing caveat (a failed text extraction) is the thing he most
// needs to hear about. A delivery note must not overwrite it.
func TestClaudeCodeBrain_KeepsAnExistingCaveat(t *testing.T) {
	c := brainWith(&placingRunner{})
	a := screenshot()
	a.Note = "text extraction failed"
	got, _ := c.lastUserMessage(context.Background(), []Message{{Role: RoleUser, Attachments: []Attachment{a}}})
	if !strings.Contains(got, "text extraction failed") || !strings.Contains(got, "Read tool") {
		t.Fatalf("both notes must survive:\n%s", got)
	}
}

// Why: a message that is only an attachment is still a message.
func TestClaudeCodeBrain_AnAttachmentAloneIsStillAMessage(t *testing.T) {
	c := brainWith(&placingRunner{})
	got, ok := c.lastUserMessage(context.Background(), []Message{{
		Role: RoleUser, Attachments: []Attachment{resumeAttachment()},
	}})
	if !ok || !strings.Contains(got, "KHAYA MALABIE") {
		t.Fatalf("an attachment with no typed text must still be sent: ok=%v\n%s", ok, got)
	}
}

// Why: a plain message must render byte-identically, or every cached prefix in
// every resumed session is invalidated for nothing.
func TestClaudeCodeBrain_PlainMessagesAreUnchanged(t *testing.T) {
	c := brainWith(&placingRunner{})
	got, _ := c.lastUserMessage(context.Background(), []Message{{Role: RoleUser, Content: "  finish the pursuit  "}})
	if got != "finish the pursuit" {
		t.Fatalf("a message with no attachments must render exactly as before: %q", got)
	}
}
