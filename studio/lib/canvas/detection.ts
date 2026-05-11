/**
 * isCodeChangeTool — single source of truth for "this tool call indicates an
 * active coding session." Used by both Canvas (to populate the changes list,
 * to flip file tabs into diff mode) and Live (to show the "Open in Canvas"
 * banner the first time the agent reaches for an editor verb).
 *
 * Names are checked case-insensitively because different MCP bridges
 * normalize tool names differently (Claude Code's CLI exposes "Edit",
 * the Anthropic-tools convention prefers "edit"). Both shapes show up.
 */

const CODE_CHANGE_TOOLS = new Set([
  "claude_code__edit",
  "claude_code__write",
  "claude_code__multiedit",
  "claude_code__notebookedit",
]);

export function isCodeChangeTool(name: string | undefined | null): boolean {
  if (!name) return false;
  return CODE_CHANGE_TOOLS.has(name.toLowerCase());
}

export function isCodeReadTool(name: string | undefined | null): boolean {
  if (!name) return false;
  const lower = name.toLowerCase();
  return (
    lower === "claude_code__read" ||
    lower === "claude_code__glob" ||
    lower === "claude_code__grep" ||
    lower === "claude_code__ls"
  );
}

export function extractToolFilePath(input: Record<string, unknown> | undefined): string | null {
  if (!input) return null;
  const candidates = ["file_path", "path", "filepath", "filename", "file"];
  for (const k of candidates) {
    const v = input[k];
    if (typeof v === "string" && v.trim()) return v;
  }
  return null;
}
