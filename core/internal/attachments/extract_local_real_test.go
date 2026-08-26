package attachments

import (
	"context"
	"os"
	"testing"
)

// TestLocalPDFRealFile proves the pure-Go fallback reads a real PDF's text
// layer (the path Core takes when the cloud workspace is unreachable). Set
// INFINITY_TEST_PDF to a text-layer PDF to run it; skipped otherwise.
func TestLocalPDFRealFile(t *testing.T) {
	path := os.Getenv("INFINITY_TEST_PDF")
	if path == "" {
		t.Skip("INFINITY_TEST_PDF not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	res := LocalExtractor{}.Extract(context.Background(), Input{Name: "real.pdf", MIME: "application/pdf", Data: data})
	if res.Err != nil {
		t.Fatalf("local extraction failed: %v (status %s)", res.Err, res.Status)
	}
	if res.Status != StatusOK || len(res.Text) < 50 {
		t.Fatalf("expected real text, got status=%s len=%d", res.Status, len(res.Text))
	}
	t.Logf("pages=%d text=%d chars, head=%q", res.PageCount, len(res.Text), res.Text[:min(120, len(res.Text))])
}
