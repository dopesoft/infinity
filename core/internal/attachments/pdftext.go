package attachments

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/dopesoft/infinity/core/internal/bridge"
)

// pdftext.go — reading a PDF's text layer FAITHFULLY.
//
// The reference failure (2026-09-01): the boss's own resume came back as
// "shipping category-de ning software across the HR, healthcare, retail,
// sta ng, and aviation industries", and "iCIMS O er". Every f-ligature in the
// document had been swallowed: first, defining, staffing, Offer, office, field.
//
// The cause is not a bug we wrote, it is the extractor we picked. The resume
// embeds subsetted Type 1C fonts with a MacRoman encoding and NO ToUnicode
// map (pdffonts: "uni no"), so poppler has no way to name the ff / fi / ffi
// glyphs and emits a space where each one stood. MuPDF resolves the same
// glyphs through the font's own glyph names and gets every one of them right,
// verified against this exact file.
//
// So MuPDF leads and poppler is the fallback, and BOTH outputs go through
// normalizeLigatures, because the other half of this failure class is a PDF
// that maps its ligatures to the real Unicode ligature codepoints (U+FB00…)
// which then travel intact all the way into a prompt as characters no
// downstream matcher, no search index and no keyword filter will ever hit.
//
// The wider rule this sits under: a document Jarvis reads WRONG is worse than
// one he cannot read at all, because nothing about the result says it is
// damaged. He quotes it back with a straight face. Reading is a mechanic, so
// it lives here in code, gets the better engine by default, and is tested.

// ligatureReplacer expands every ligature codepoint that can reach us into
// its plain letters. Applied to every engine's output, always: correct text is
// never changed by it, so there is no path where skipping it would be right.
//
// U+FB00…U+FB06 are the real thing. U+F001/U+F002 are the private-use codes
// that Adobe's older subsetted fonts use for fi and fl, which are otherwise
// rendered as an empty box or dropped by anything reading the string later.
var ligatureReplacer = strings.NewReplacer(
	"ﬀ", "ff",
	"ﬁ", "fi",
	"ﬂ", "fl",
	"ﬃ", "ffi",
	"ﬄ", "ffl",
	"ﬅ", "st",
	"ﬆ", "st",
	"\uF001", "fi",
	"\uF002", "fl",
)

// normalizeLigatures returns text with every ligature codepoint spelled out.
func normalizeLigatures(s string) string { return ligatureReplacer.Replace(s) }

// muPDFScript extracts a PDF's text with MuPDF, installing the binding on
// first use. It is shipped base64-encoded so no layer of shell quoting can
// mangle it, and it writes the same <file>.txt the poppler path writes so the
// agent finds the extraction next to the document either way.
const muPDFScript = `import os, sys, subprocess

def load():
    for name in ("pymupdf", "fitz"):
        try:
            return __import__(name)
        except Exception:
            pass
    return None

mu = load()
if mu is None:
    # A fresh --user install lands in a directory this interpreter already
    # finished scanning, so importing it in THIS process fails even though the
    # install succeeded. Re-exec once, guarded, so a genuinely failed install
    # cannot spin.
    if os.environ.get("INF_PDFTEXT_RETRY"):
        sys.exit("pymupdf unavailable after install")
    subprocess.run(
        [sys.executable, "-m", "pip", "install", "--quiet",
         "--disable-pip-version-check", "pymupdf"],
        check=True, timeout=int(sys.argv[3]))
    os.environ["INF_PDFTEXT_RETRY"] = "1"
    os.execve(sys.executable, [sys.executable] + sys.argv, os.environ)

doc = mu.open(sys.argv[1])
pages = [p.get_text() for p in doc]
with open(sys.argv[2], "w", encoding="utf-8") as fh:
    fh.write("\n".join(pages))
sys.stdout.write(str(len(pages)))
`

// muPDFInstallSec caps the one-time binding install so a slow index cannot
// eat the whole bash window and strand us with no text at all.
const muPDFInstallSec = 40

// pdfTextMuPDF reads the text layer with MuPDF and returns it with the page
// count. A non-zero exit or a missing python is an ordinary miss, not a
// failure: the caller falls back to poppler.
func pdfTextMuPDF(ctx context.Context, b bridge.Bridge, dir, base, txtName string) (string, int, error) {
	script := base64.StdEncoding.EncodeToString([]byte(muPDFScript))
	// PIP_BREAK_SYSTEM_PACKAGES / PIP_USER: Debian marks its python
	// externally-managed (PEP 668) and refuses a bare install without them.
	cmd := fmt.Sprintf(
		"printf %%s %s | base64 -d > /tmp/.inf_pdftext.py && "+
			"PIP_BREAK_SYSTEM_PACKAGES=1 PIP_USER=1 python3 /tmp/.inf_pdftext.py %s %s %d",
		shq(script), shq(base), shq(txtName), muPDFInstallSec)

	out, code, err := runBash(ctx, b, dir, cmd)
	if err != nil {
		return "", 0, err
	}
	if code != 0 {
		return "", 0, fmt.Errorf("mupdf exit %d: %s", code, strings.TrimSpace(out))
	}
	raw, status, ok := b.Get(ctx, "/fs/raw?path="+url.QueryEscape(dir+"/"+txtName))
	if !ok || status >= 300 {
		return "", 0, fmt.Errorf("read %s: status %d", txtName, status)
	}
	text := strings.TrimSpace(normalizeLigatures(string(raw)))
	if text == "" {
		// An empty text layer is a real answer (a scan), but it is poppler's
		// answer to give: it also decides whether to rasterize.
		return "", 0, fmt.Errorf("mupdf: no text layer")
	}
	pages, _ := strconv.Atoi(strings.TrimSpace(out))
	return text, pages, nil
}
