package attachments

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNormalizeLigatures_SpellsOutWhatAMatcherCannotSee.
//
// A ligature codepoint reads correctly to a human and is invisible to
// everything else: a search for "office" misses "oﬃce", a skill matching on
// "staffing" misses "staﬃng", and the boss never finds out why. The fix is
// not cosmetic, so it is pinned.
func TestNormalizeLigatures_SpellsOutWhatAMatcherCannotSee(t *testing.T) {
	cases := []struct{ in, want string }{
		{"staﬃng", "staffing"},
		{"iCIMS Oﬀer", "iCIMS Offer"},
		{"category-deﬁning", "category-defining"},
		{"the ﬁrst global patient registry", "the first global patient registry"},
		{"conﬂict", "conflict"},
		{"shuﬄe", "shuffle"},
		{"eld", "field"},
		{"conuence", "confluence"},
		// Text that was already correct must come through untouched.
		{"Senior product leader with 18 years", "Senior product leader with 18 years"},
	}
	for _, c := range cases {
		if got := normalizeLigatures(c.in); got != c.want {
			t.Errorf("normalizeLigatures(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestNormalizeLigatures_LeavesEveryOtherRuneAlone guards the replacer against
// the one way it could do harm: an empty or over-broad pattern would rewrite
// text at every position.
func TestNormalizeLigatures_LeavesEveryOtherRuneAlone(t *testing.T) {
	const s = "f i ff fi fl ffi — 40% ARR · $1.83M · résumé · 日本語"
	if got := normalizeLigatures(s); got != s {
		t.Fatalf("plain text was rewritten:\n got %q\nwant %q", got, s)
	}
}

// TestMuPDFScript_KeepsTheLigaturesPopplerDrops runs the script we actually
// ship against a real PDF and proves the words survive.
//
// This is the whole reason MuPDF leads: on the boss's resume, poppler emits
// "sta ng" / "O er" / "the rst" because the embedded Type 1C subsets carry no
// ToUnicode map. Point INFINITY_TEST_PDF at that file to run it.
func TestMuPDFScript_KeepsTheLigaturesPopplerDrops(t *testing.T) {
	src := os.Getenv("INFINITY_TEST_PDF")
	if src == "" {
		t.Skip("INFINITY_TEST_PDF not set")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "extract.py")
	if err := os.WriteFile(script, []byte(muPDFScript), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.txt")

	cmd := exec.Command("python3", script, src, out, "40")
	cmd.Env = append(os.Environ(), "PIP_BREAK_SYSTEM_PACKAGES=1", "PIP_USER=1")
	// Not a Skip. This test is opt-in via INFINITY_TEST_PDF, so once it is
	// running, the script failing IS the thing under test failing - and a
	// skip here is exactly how a broken extractor keeps looking fine.
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the shipped script failed: %v\n%s", err, strings.TrimSpace(string(b)))
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := normalizeLigatures(string(raw))

	// The damage poppler leaves on this document, verbatim. Any one of these
	// appearing means a ligature was swallowed again.
	for _, dropped := range []string{"sta ng", "O er", "de ning", "the rst", "o ce"} {
		if strings.Contains(text, dropped) {
			t.Errorf("ligature dropped: found %q in the extracted text", dropped)
		}
	}
	// And the words themselves are present, so this cannot pass by extracting
	// nothing at all.
	for _, want := range []string{"staffing", "Offer", "defining", "first"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in the extracted text", want)
		}
	}
}

// TestMuPDFCommand_SurvivesShellQuoting: the script travels to the bridge as
// base64 precisely so no layer of quoting can mangle it. If the encoding ever
// stops round-tripping, every PDF silently falls back to poppler and the
// ligature bug returns with nothing to show for it.
func TestMuPDFCommand_SurvivesShellQuoting(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString([]byte(muPDFScript))
	back, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != muPDFScript {
		t.Fatal("script did not round-trip through base64")
	}
	if strings.ContainsAny(enc, "'\"$`\\") {
		t.Fatalf("encoded script carries shell metacharacters: %q", enc)
	}
}
