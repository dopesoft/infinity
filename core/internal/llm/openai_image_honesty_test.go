package llm

import (
	"strings"
	"testing"
)

// Why: DeepSeek (and any other Chat-Completions vendor without vision) takes
// no image parts, so the text rendering is all an image gets - and TextBlock
// alone can only say "[image]". A model handed a filename and nothing else
// answers as though it looked, which is the same confident-answer-about-
// nothing this codebase refuses everywhere else. It has to be told it did not
// see the picture, and told to say so.
func TestBlindVendor_IsToldItCannotSeeThePicture(t *testing.T) {
	got := openaiAttachmentText(Message{
		Role:    RoleUser,
		Content: "what's wrong with this screen",
		Attachments: []Attachment{{
			Kind: AttachmentImage, Name: "Screenshot.png", MIME: "image/png",
			Data: []byte{0x89, 'P', 'N', 'G'},
		}},
	})
	if !strings.Contains(got, "have NOT seen this image") {
		t.Errorf("the model must be told it cannot see it:\n%s", got)
	}
	if !strings.Contains(got, "never guess") {
		t.Errorf("and told not to guess:\n%s", got)
	}
	if !strings.Contains(got, "what's wrong with this screen") {
		t.Errorf("what he typed must survive:\n%s", got)
	}
}

// Why: a document reaches this vendor as its real extracted text, so it HAS
// seen the content. Telling it otherwise would make it refuse work it can do.
func TestBlindVendor_SaysNothingOfTheSortAboutADocument(t *testing.T) {
	got := openaiAttachmentText(Message{
		Role: RoleUser,
		Attachments: []Attachment{{
			Kind: AttachmentDocument, Name: "resume.pdf", MIME: "application/pdf",
			Text: "KHAYA MALABIE\nHead of Product",
		}},
	})
	if strings.Contains(got, "cannot be shown") {
		t.Errorf("a document's text DID reach it:\n%s", got)
	}
	if !strings.Contains(got, "KHAYA MALABIE") {
		t.Errorf("and the text must be there:\n%s", got)
	}
}

// Why: a scanned PDF arrives as rasterized page images, which this rendering
// DOES carry. Marking it unseen would be wrong in the other direction.
func TestBlindVendor_LeavesRasterizedPagesAlone(t *testing.T) {
	got := openaiAttachmentText(Message{
		Role: RoleUser,
		Attachments: []Attachment{{
			Kind: AttachmentImage, Name: "scan.png", MIME: "image/png",
			Pages: [][]byte{{1}}, PageMIME: "image/png",
		}},
	})
	if strings.Contains(got, "cannot be shown") {
		t.Errorf("page images do reach it:\n%s", got)
	}
}
