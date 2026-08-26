package attachments

import (
	"context"
	"strings"
	"testing"
)

// Why: the class decides whether a file is read natively, converted, or only
// mirrored. Misclassifying a PDF as "other" would send the brain nothing but
// a path again.
func TestClassifyByMIMEThenExtension(t *testing.T) {
	cases := []struct {
		mime, name string
		want       Class
	}{
		{"application/pdf", "x.bin", ClassPDF},
		{"application/octet-stream", "mindset.pdf", ClassPDF},
		{"image/png", "shot", ClassImage},
		{"", "notes.docx", ClassOffice},
		{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "q.xlsx", ClassOffice},
		{"text/plain", "a", ClassText},
		{"", "main.go", ClassText},
		{"application/json", "x", ClassText},
		{"application/zip", "x.zip", ClassOther},
	}
	for _, c := range cases {
		if got := Classify(c.mime, c.name); got != c.want {
			t.Errorf("Classify(%q,%q)=%s want %s", c.mime, c.name, got, c.want)
		}
	}
}

func TestSafeNameStripsPathsAndControl(t *testing.T) {
	if got := SafeName("../../etc/passwd"); got != "passwd" {
		t.Fatalf("got %q", got)
	}
	if got := SafeName("a\x00b\n.pdf"); got != "ab.pdf" {
		t.Fatalf("got %q", got)
	}
	if got := SafeName("   "); got != "attachment" {
		t.Fatalf("got %q", got)
	}
}

// Why: with the cloud bridge down, text files must still be readable and a
// PDF without a text layer must FAIL loudly (it needs the workspace to
// rasterize), never come back as a quiet "empty".
func TestLocalExtractorIsHonest(t *testing.T) {
	loc := LocalExtractor{}
	res := loc.Extract(context.Background(), Input{Name: "a.md", MIME: "text/markdown", Data: []byte("# hi\n")})
	if res.Status != StatusOK || res.Text != "# hi" {
		t.Fatalf("text file: %+v", res)
	}
	res = loc.Extract(context.Background(), Input{Name: "deck.pptx", Data: []byte("PK")})
	if res.Status != StatusFailed || res.Err == nil || !strings.Contains(res.Err.Error(), "unreachable") {
		t.Fatalf("office without workspace must fail loudly: %+v", res)
	}
	res = loc.Extract(context.Background(), Input{Name: "junk.pdf", MIME: "application/pdf", Data: []byte("not a pdf")})
	if res.Status != StatusFailed || res.Err == nil {
		t.Fatalf("unparseable pdf must fail loudly: %+v", res)
	}
}

func TestShellQuoting(t *testing.T) {
	if got := shq("it's.pdf"); got != `'it'\''s.pdf'` {
		t.Fatalf("got %s", got)
	}
}

func TestResolveMIMEFallsBackToExtensionThenSniff(t *testing.T) {
	if got := resolveMIME("", "a.md", []byte("# x")); got != "text/markdown" {
		t.Fatalf("got %q", got)
	}
	if got := resolveMIME("application/octet-stream", "x.pdf", []byte("%PDF-1.4")); got != "application/pdf" {
		t.Fatalf("got %q", got)
	}
	if got := resolveMIME("image/png; charset=binary", "x", nil); got != "image/png" {
		t.Fatalf("got %q", got)
	}
}
