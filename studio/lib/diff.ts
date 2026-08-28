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
