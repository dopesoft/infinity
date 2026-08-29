/**
 * Unified-diff line tinting, shared by every surface that shows a diff.
 *
 * Contract: one line of unified-diff text in, one Tailwind class string out
 * (possibly empty for a context line). Pure — no React, no parsing of
 * multi-file structure, no state. Callers own the container and the
 * per-line `<div>`; this only decides the colour.
 *
 * Extracted from `components/ToolCallCard.tsx`'s `DiffPre` so the Majordomo
 * `<Inset variant="diff">` primitive and the tool-call card tint diffs
 * identically. If the palette changes, it changes here once.
 *
 * Precedence matters: `+++` / `---` file headers must be tested BEFORE the
 * bare `+` / `-` add/remove lines, otherwise every file header renders as an
 * addition or a deletion.
 */
export function diffLineClass(line: string): string {
  if (line.startsWith("+++") || line.startsWith("---")) return "text-muted-foreground";
  if (line.startsWith("@@")) return "text-info";
  if (line.startsWith("+")) return "bg-success/10 text-success";
  if (line.startsWith("-")) return "bg-danger/10 text-danger";
  return "";
}

/**
 * Heuristic: does this blob of text look like a unified diff?
 *
 * Used to decide whether a tool result should render with diff tinting.
 * Deliberately loose — a false positive costs a little colour, a false
 * negative costs the reader the whole signal.
 */
export function looksLikeDiff(text: string): boolean {
  if (!text) return false;
  return /^(@@ |\+\+\+ |--- |diff --git )/m.test(text);
}

/**
 * A code change, as a diff — the thing the boss asked for and never got.
 *
 * "I don't see it coding based on my mockup … UNLIKE claude which shows the
 * file its working on and a sampling of the code written." An edit step used
 * to render `new_string` as an untinted mono blob: you could see that SOMETHING
 * was written, never what changed about it.
 *
 * The tool call carries `old_string` and `new_string` (or just `content` for a
 * write), which is everything needed to show a real hunk. This turns that pair
 * into one, and the caller renders it through the existing `Inset variant=
 * "diff"` so the tinting is the same tinting every other diff in the app uses.
 *
 * PURE, and deliberately not a full LCS. An `Edit` is by construction one
 * contiguous replacement, so trimming the common head and tail and calling the
 * remainder the hunk is exactly right for the shape we actually get — and it
 * is O(n) rather than O(n·m), which matters when the "old string" is a
 * thousand-line file body.
 */
export interface CodeChange {
  /** Unified-diff text, ready for `Inset variant="diff"`. */
  unified: string;
  added: number;
  removed: number;
  /** Lines of the hunk that did not fit the excerpt. */
  hidden: number;
}

/** Context lines kept either side of a change, the way a diff tool shows them. */
const CONTEXT_LINES = 2;

export function buildCodeChange(before: string, after: string, maxLines = 22): CodeChange {
  const b = splitLines(before);
  const a = splitLines(after);

  // Common head, then common tail — never overlapping into each other.
  let head = 0;
  while (head < b.length && head < a.length && b[head] === a[head]) head++;
  let tail = 0;
  while (
    tail < b.length - head &&
    tail < a.length - head &&
    b[b.length - 1 - tail] === a[a.length - 1 - tail]
  ) {
    tail++;
  }

  const removedLines = b.slice(head, b.length - tail);
  const addedLines = a.slice(head, a.length - tail);
  const ctxBefore = b.slice(Math.max(0, head - CONTEXT_LINES), head);
  const ctxAfter = b.slice(b.length - tail, Math.min(b.length, b.length - tail + CONTEXT_LINES));

  const body: string[] = [
    ...ctxBefore.map((l) => ` ${l}`),
    ...removedLines.map((l) => `-${l}`),
    ...addedLines.map((l) => `+${l}`),
    ...ctxAfter.map((l) => ` ${l}`),
  ];

  // Trim from the MIDDLE outward would be cleverer and less readable. Keeping
  // the top of the hunk matches how a person reads a change: the first lines
  // are the ones that say what it is.
  const hidden = Math.max(0, body.length - maxLines);
  return {
    unified: body.slice(0, maxLines).join("\n"),
    added: addedLines.length,
    removed: removedLines.length,
    hidden,
  };
}

/** Drops the one trailing newline a file body normally ends with. */
function splitLines(s: string): string[] {
  if (!s) return [];
  return s.replace(/\n$/, "").split("\n");
}

/** `+142 −8`, or "" when nothing changed. The minus is U+2212, not a hyphen. */
export function changeStats(change: CodeChange): string {
  const parts: string[] = [];
  if (change.added > 0) parts.push(`+${change.added}`);
  if (change.removed > 0) parts.push(`−${change.removed}`);
  return parts.join(" ");
}
