/**
 * isCodeChangeTool — single source of truth for "this tool call indicates an
 * active coding session." Used by both Canvas (to populate the changes list,
 * to flip file tabs into diff mode) and Live (to show the "Open in Canvas"
 * banner the first time the agent reaches for an editor verb) AND the
 * ToolCallCard (to show file-aware previews so the boss can tell at a
 * glance what's being touched without expanding every bubble).
 *
 * Names are checked case-insensitively because different MCP bridges
 * normalize tool names differently (Claude Code's CLI exposes "Edit",
 * the Anthropic-tools convention prefers "edit"). Both shapes show up.
 *
 * The set spans every code-write path Jarvis actually uses:
 *   • claude_code__*  — Mac local bridge (when the boss's Mac is up)
 *   • fs_*            — native fs tools used in cloud / non-Mac sessions
 *   • github__*       — GitHub MCP file ops, including direct-to-remote
 *                       pushes that bypass the local working tree entirely
 * Without the github__ entries, Jarvis pushing a file straight to main
 * via GitHub MCP showed up as a generic `github__push_files` row with
 * no file-path enrichment and never lit up the Changes column — which
 * is exactly the "I can't tell when it's coding" symptom.
 */

const CODE_CHANGE_TOOLS = new Set([
  // Local Mac bridge.
  "claude_code__edit",
  "claude_code__write",
  "claude_code__multiedit",
  "claude_code__notebookedit",
  // Cloud / native FS.
  "fs_edit",
  "fs_save",
  // GitHub MCP — direct-to-remote writes Jarvis uses when there's no
  // local working tree (or when the boss explicitly asked for a PR).
  "github__create_or_update_file",
  "github__push_files",
  "github__delete_file",
]);

// Repo-level write verbs that change the *codebase* without touching a
// specific file path (commits, pushes, PRs, branches). Worth surfacing
// distinctly in the chat so the boss can tell Jarvis is shipping code,
// even though they don't contribute a single file to the dirty set.
const REPO_WRITE_TOOLS = new Set([
  "git_commit",
  "git_push",
  "git_stage",
  "github__create_pull_request",
  "github__merge_pull_request",
  "github__create_branch",
  "github__update_pull_request",
  "github__update_pull_request_branch",
]);

export function isCodeChangeTool(name: string | undefined | null): boolean {
  if (!name) return false;
  return CODE_CHANGE_TOOLS.has(name.toLowerCase());
}

export function isRepoWriteTool(name: string | undefined | null): boolean {
  if (!name) return false;
  return REPO_WRITE_TOOLS.has(name.toLowerCase());
}

export function isCodeReadTool(name: string | undefined | null): boolean {
  if (!name) return false;
  const lower = name.toLowerCase();
  return (
    lower === "claude_code__read" ||
    lower === "claude_code__glob" ||
    lower === "claude_code__grep" ||
    lower === "claude_code__ls" ||
    lower === "fs_read" ||
    lower === "fs_ls" ||
    lower === "github__get_file_contents"
  );
}

export function extractToolFilePath(input: Record<string, unknown> | undefined): string | null {
  if (!input) return null;
  // Single-file inputs across every known bridge.
  const candidates = ["file_path", "path", "filepath", "filename", "file"];
  for (const k of candidates) {
    const v = input[k];
    if (typeof v === "string" && v.trim()) return v;
  }
  return null;
}

/**
 * extractToolFilePaths — multi-file variant. Returns every path the call
 * touches: the single-file fields above plus github__push_files' `files`
 * array (each entry has a `path` and optional `content`). Used by the
 * dirty-paths bookkeeping so a single `push_files` call lights up the
 * Changes column for every file in the push, not just the first.
 */
export function extractToolFilePaths(input: Record<string, unknown> | undefined): string[] {
  if (!input) return [];
  const out: string[] = [];
  const single = extractToolFilePath(input);
  if (single) out.push(single);
  const files = input["files"];
  if (Array.isArray(files)) {
    for (const f of files) {
      if (f && typeof f === "object") {
        const p = (f as Record<string, unknown>)["path"];
        if (typeof p === "string" && p.trim()) out.push(p);
      } else if (typeof f === "string" && f.trim()) {
        out.push(f);
      }
    }
  }
  return out;
}

/**
 * extractToolPreview — short content snippet to show in the in-chat
 * tool-call header while the call is running. Lets the boss tell
 * exactly what's being written at a glance instead of having to expand
 * every bubble.
 *
 * Edit  → returns the new_string (replacement text).
 * Write → returns the full content.
 * GitHub push_files → returns the first file's content.
 * Bash  → returns the command itself.
 * Git commit → returns the commit message.
 *
 * Always trimmed and length-capped — the card renders a fixed-height
 * preview box, this is just the source text.
 */
export function extractToolPreview(input: Record<string, unknown> | undefined): string {
  if (!input) return "";
  const pick = (key: string): string => {
    const v = input[key];
    return typeof v === "string" ? v : "";
  };
  // Edit-shaped: new_string is what's being written.
  const newString = pick("new_string");
  if (newString) return newString;
  // Write/Save/GitHub create_or_update_file: content is the full new body.
  const content = pick("content");
  if (content) return content;
  // Bash: command is the action.
  const command = pick("command");
  if (command) return command;
  // Git commit / PR creation: message body.
  const message = pick("message") || pick("body") || pick("title");
  if (message) return message;
  // push_files multi-file: show the first file's content if available.
  const files = input["files"];
  if (Array.isArray(files) && files.length > 0) {
    const first = files[0];
    if (first && typeof first === "object") {
      const c = (first as Record<string, unknown>)["content"];
      if (typeof c === "string") return c;
    }
  }
  return "";
}
