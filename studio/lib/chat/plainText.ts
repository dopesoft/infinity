/**
 * plainText.ts - markdown out of places that are not prose.
 *
 * THE BUG. The runtime brain writes its thinking trace in markdown, so the
 * ledger showed the boss this, verbatim, in a monospace block:
 *
 *   **Planning agent termination and cancellation****Evaluating workflow
 *   cancellation methods**
 *
 * Two things are wrong there. The asterisks are syntax, not content - nobody
 * wants to read them. And the two headings ran together into one word
 * (`cancellation****Evaluating`) because the model emits consecutive bold
 * headings with no separator, so even mentally stripping the markup leaves a
 * sentence that doesn't parse.
 *
 * WHERE THIS IS AND IS NOT USED. Only on PROSE - a thinking trace, a narration
 * line, an assistant sentence. Never on a tool's meta line, which is a file
 * path or a shell command where `*` is a glob and a backtick may be real
 * syntax; stripping there would corrupt the very thing the boss is reading it
 * to check. That distinction is why this is its own function rather than
 * something bolted into `oneLine`, which serves both.
 */

/**
 * stripMarkdown renders markdown-ish prose as the plain text a person would
 * read aloud. Emphasis markers are dropped, headings become their own lines,
 * and the run-together `**a****b**` case is split rather than concatenated.
 */
export function stripMarkdown(input: string): string {
  if (!input) return "";
  return (
    input
      // `**a****b**` → two separate blocks. Done FIRST, while the markers are
      // still there to tell us where one heading ended and the next began.
      .replace(/\*\*\s*\*\*/g, "\n")
      // Fenced code: keep the contents, drop the fence line.
      .replace(/^```[^\n]*$/gm, "")
      // A heading is a line, not a `#`.
      .replace(/^\s{0,3}#{1,6}\s+/gm, "")
      // Bold / italic / inline code, in that order so `***x***` unwraps fully.
      .replace(/\*\*\*(.+?)\*\*\*/gs, "$1")
      .replace(/\*\*(.+?)\*\*/gs, "$1")
      .replace(/(^|[\s(])\*(?!\s)([^*\n]+?)\*(?=[\s.,;:!?)]|$)/g, "$1$2")
      .replace(/(^|[\s(])_(?!\s)([^_\n]+?)_(?=[\s.,;:!?)]|$)/g, "$1$2")
      .replace(/`([^`\n]+)`/g, "$1")
      // A markdown link reads as its text.
      .replace(/\[([^\]\n]+)\]\([^)\s]+\)/g, "$1")
      // List bullets are noise once the structure is gone.
      .replace(/^\s{0,3}[-*+]\s+/gm, "")
      // Collapse the blank lines the substitutions above leave behind.
      .replace(/\n{3,}/g, "\n\n")
      .trim()
  );
}
