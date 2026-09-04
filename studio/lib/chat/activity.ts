/**
 * activity.ts - the vocabulary and the ledger arithmetic behind the Majordomo
 * activity ledger (docs/studio/MAJORDOMO.md §6).
 *
 * WHY THIS FILE EXISTS (CLAUDE.md Rule #1b: mechanics live in code, never in
 * prose, and never in a component):
 *
 *   Every decision about WHAT WORDS APPEAR when Jarvis does something is made
 *   here, once, and is tested. The component layer is dumb rendering: it takes
 *   a verb, a glyph name, a meta line, a status and a detail kind, and paints
 *   them. It never derives a label, never parses an output string, never
 *   decides whether two rows are "the same kind of work".
 *
 *   The bug this replaces: the old ToolCallCard fell through to `call.name`
 *   whenever a tool wasn't in its tiny hardcoded list, so the boss read rows
 *   that said `composio__GMAIL_FETCH_EMAILS` and `mem_act`. A raw tool id must
 *   NEVER reach a row. Unknown tools humanize (`server__VERB` → "Server ·
 *   verb"); there is no path out of describeStep that returns an id.
 *
 * PURE. No React, no DOM, no timers, no fetch. `glyph` is a Lucide icon NAME
 * (a string) precisely so this file stays free of `lucide-react` and can be
 * unit-tested in plain node.
 */

// Type-only: erased at compile time, so this module pulls in neither React nor
// next/navigation at runtime even though useChat does.
import type { ChatMessage } from "@/hooks/useChat";
// Relative, like the other value import below: the test runner does not
// resolve the "@/" alias in this module.
import { stripMarkdown } from "./plainText";
import type { WSToolEvent } from "@/lib/ws/client";
// Value import, deliberately relative so the module resolves without the "@/"
// bundler alias (it is executed directly by the test runner).
import {
  extractToolFilePath,
  extractToolFilePaths,
  extractToolPreview,
  isCodeChangeTool,
  isRepoWriteTool,
} from "../canvas/detection";

// ─────────────────────────────────────────────────────────────────────────────
// Public types
// ─────────────────────────────────────────────────────────────────────────────

/**
 * The detail renderer a step's payload wants, per MAJORDOMO §6. The component
 * maps each to an `<Inset variant>`; nothing else in the UI branches on tool id.
 */
export type DetailKind =
  | "kv"
  | "terminal"
  | "diff"
  | "note"
  | "thought"
  | "search"
  | "files"
  | "plan"
  | "goal"
  | "commit"
  | "approval"
  | "failure";

/**
 * The five states a ledger row can be in (§6). This collapses the old card's
 * six-way split: `awaiting` (gate parked the call) and `gated` (the call came
 * back BLOCKED) are both "waiting on the boss" and both render amber, so they
 * share the `approval` status and are told apart by the `awaiting` / `gated`
 * flags on StepStatusInfo when the detail needs to say which.
 */
export type StepStatus = "running" | "done" | "error" | "approval" | "stopped";

export type StepDescription = {
  /** Present tense, for a row that is happening now: "Editing migrate.go". */
  verb: string;
  /** Past tense, for a settled row: "Edited migrate.go". */
  verbPast: string;
  /** Lucide icon NAME, e.g. "FilePen". Never a component. */
  glyph: string;
  /** The quiet second line, drawn from the input field that actually matters. */
  meta: string;
  kind: DetailKind;
};

export type StepStatusInfo = {
  status: StepStatus;
  /** The gate parked the call and is still holding it (no result yet). */
  awaiting: boolean;
  /** The call returned a synthesised "BLOCKED:" result WITH a Trust contract. */
  gated: boolean;
  /**
   * The call was refused outright and nothing was queued: the loop guard
   * stopping a repeated call, a gate saying no. Red, never amber, and never
   * a Trust link - there is nothing in the Trust tab to find.
   */
  refused: boolean;
  /** Trust contract to approve, from either path. Feeds the /trust?focus= link. */
  contractId?: string;
};

export type ActivityItem = {
  /** Stable React key: the id of the first message in the run. */
  id: string;
  /** Coalescing identity. Two adjacent items with the same key fold. */
  key: string;
  kind: DetailKind;
  glyph: string;
  /** Present-tense label, counted: "Editing migrate.go" / "Running 3 commands". */
  verb: string;
  /** Past-tense label, counted: "Edited migrate.go" / "Ran 3 commands". */
  verbPast: string;
  /** What the row shows right now: `verb` while running, `verbPast` once settled. */
  label: string;
  meta: string;
  status: StepStatus;
  awaiting: boolean;
  gated: boolean;
  refused: boolean;
  contractId?: string;
  /** How many underlying calls folded into this row. 1 for a lone step. */
  count: number;
  /** Every underlying message, so the component can render the group's detail. */
  messages: ChatMessage[];
  startedAt: number;
  endedAt?: number;
  /** How telling this work is, for summaryFor's "three most telling verbs". */
  weight: number;
};

type Rec = Record<string, unknown>;

// ─────────────────────────────────────────────────────────────────────────────
// Gate parsing (was inline in ToolCallCard; one source of truth now)
// ─────────────────────────────────────────────────────────────────────────────

/**
 * Recognise the "BLOCKED: … Trust contract: <uuid>" result the agent gate
 * synthesises when it parks a call, so a row can offer the one-tap Trust link.
 *
 * Gated means QUEUED: there is a contract to approve. A "BLOCKED:" with no
 * contract is not gated, it is refused (see detectRefusal). Reading it as
 * gated put an "Approve in Trust tab" link under a loop-guard block, and the
 * boss went to the Trust tab and found it empty (2026-09-04) - nothing had
 * ever been queued, by design.
 */
const TRUST_CONTRACT_RE = /Trust contract:\s*([0-9a-fA-F-]{8,})/;
/** The legacy gate copy that told the model the Trust store was broken. Chrome, never shown. */
const LEGACY_TRUST_WARNING_RE = /WARNING: this call was NOT persisted to the Trust queue[\s\S]*?simply refused\.\s*/;

export function detectGated(output?: string): { gated: boolean; contractId?: string } {
  if (!output || !output.startsWith("BLOCKED:")) return { gated: false };
  const m = output.match(TRUST_CONTRACT_RE);
  if (!m) return { gated: false };
  return { gated: true, contractId: m[1] };
}

/**
 * A refusal: the gate said no and queued nothing. Either the current copy
 * ("NOT RUN: … was refused") or the old "BLOCKED:" copy with no contract.
 */
export function detectRefusal(output?: string): boolean {
  if (!output) return false;
  if (output.startsWith("NOT RUN:")) return true;
  return output.startsWith("BLOCKED:") && !TRUST_CONTRACT_RE.test(output);
}

/**
 * A refusal, said rather than dumped.
 *
 * A gate result is a page of machine copy — "BLOCKED: …", a Trust contract
 * uuid, a paragraph of what to do instead — and rendering the whole slab in a
 * monospace box is the single ugliest thing in the transcript: the boss's eye
 * has to find the one sentence that matters inside a wall of grey.
 *
 * So it is split: the LEAD is the first real sentence, set in the voice face
 * like anything Jarvis says, and the REST goes behind the inset for whoever
 * wants it. The `BLOCKED:` marker itself and the contract uuid are chrome —
 * the row is already amber and already links to Trust — so neither survives.
 */
export function splitRefusal(output?: string): { lead: string; rest: string } {
  const text = (output ?? "").trim();
  if (!text) return { lead: "", rest: "" };
  const body = text
    .replace(/^(BLOCKED|NOT RUN)\s*[:\-–—]\s*/i, "")
    .replace(TRUST_CONTRACT_RE, "")
    .replace(LEGACY_TRUST_WARNING_RE, "")
    .trim();
  const cut = body.indexOf("\n\n");
  if (cut < 0) {
    // One paragraph: if it is short enough to read as a sentence, it IS the
    // whole explanation and there is nothing to hide behind an inset.
    return body.length <= 320 ? { lead: body, rest: "" } : { lead: firstSentence(body), rest: body };
  }
  return { lead: body.slice(0, cut).trim(), rest: body.slice(cut).trim() };
}

/**
 * deriveStatus - THE status derivation. Component layers import this; they do
 * not re-implement it. Order matters and encodes real behaviour:
 *
 *  1. awaiting  - the gate parked the call on a contract and the agent loop is
 *                 blocked inside WaitForDecision. There is no result and there
 *                 will not be one until the boss decides. Amber, never "running".
 *  2. stopped   - the turn ended (complete / error / Stop pressed) before the
 *                 result frame arrived. settleInFlight() in useChat stamps
 *                 `interrupted`. A stopped call MUST NOT read as done: nobody
 *                 knows whether it did anything.
 *  3. running   - no result yet, turn still live.
 *  4. approval  - a result came back, but it is the gate's BLOCKED sentinel.
 *  5. error     - the tool itself failed.
 *  6. done.
 */
export function deriveStatus(message: ChatMessage): StepStatusInfo {
  const call = message.toolCall;
  const result = message.toolResult;
  const awaiting = !result && !!call?.awaiting_approval && !!call?.contract_id;
  if (awaiting) {
    return { status: "approval", awaiting: true, gated: false, refused: false, contractId: call?.contract_id };
  }
  if (!result && message.interrupted) {
    return { status: "stopped", awaiting: false, gated: false, refused: false };
  }
  if (!result) {
    // A pending thinking / interim bubble is live work too.
    return { status: "running", awaiting: false, gated: false, refused: false };
  }
  const gate = detectGated(result.output);
  if (gate.gated) {
    return { status: "approval", awaiting: false, gated: true, refused: false, contractId: gate.contractId };
  }
  // Refused outright: red, and nothing to approve anywhere.
  if (detectRefusal(result.output)) {
    return { status: "error", awaiting: false, gated: false, refused: true };
  }
  if (result.is_error) return { status: "error", awaiting: false, gated: false, refused: false };
  return { status: "done", awaiting: false, gated: false, refused: false };
}

/** True while any row in the ledger is still working. */
export function activityIsLive(items: ActivityItem[]): boolean {
  return items.some((i) => i.status === "running");
}

// ─────────────────────────────────────────────────────────────────────────────
// Small pure helpers
// ─────────────────────────────────────────────────────────────────────────────

const MAX_SUBJECT = 42;
const MAX_META = 120;

function basename(p: string): string {
  const trimmed = p.replace(/[\\/]+$/, "");
  const idx = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  return idx >= 0 ? trimmed.slice(idx + 1) : trimmed;
}

function dirname(p: string): string {
  const trimmed = p.replace(/[\\/]+$/, "");
  const idx = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  return idx >= 0 ? trimmed.slice(0, idx) : "";
}

function clamp(s: string, max: number): string {
  const t = s.trim();
  if (t.length <= max) return t;
  return t.slice(0, max - 1).trimEnd() + "…";
}

/** First line, whitespace-collapsed, length-capped. Meta is one quiet line. */
function oneLine(s: string, max = MAX_META): string {
  const first = s.split("\n").find((l) => l.trim().length > 0) ?? "";
  return clamp(first.replace(/\s+/g, " "), max);
}

function str(input: Rec | undefined, ...keys: string[]): string {
  if (!input) return "";
  for (const k of keys) {
    const v = input[k];
    if (typeof v === "string" && v.trim()) return v;
    if (typeof v === "number") return String(v);
  }
  return "";
}

/** The generic "which input field is the point of this call" fallback. */
const META_KEYS = [
  "query",
  "q",
  "search",
  "command",
  "cmd",
  "pattern",
  "url",
  "title",
  "name",
  "summary",
  "goal",
  "task",
  "prompt",
  "message",
  "text",
  "content",
  "description",
  "key",
  "id",
];

function defaultMeta(input: Rec | undefined): string {
  const v = str(input, ...META_KEYS);
  return v ? oneLine(v) : "";
}

/** The file a call touches, for verbs like "Editing migrate.go". */
function fileSubject(input: Rec | undefined): string {
  const paths = extractToolFilePaths(input);
  if (paths.length > 1) return `${paths.length} files`;
  const single = extractToolFilePath(input);
  return single ? clamp(basename(single), MAX_SUBJECT) : "";
}

function fileMeta(input: Rec | undefined): string {
  const paths = extractToolFilePaths(input);
  if (paths.length > 1) return oneLine(paths.map((p) => basename(p)).join(", "));
  const single = extractToolFilePath(input);
  return single ? oneLine(dirname(single)) : "";
}

// ─────────────────────────────────────────────────────────────────────────────
// The verb table
// ─────────────────────────────────────────────────────────────────────────────

/**
 * One entry per tool the agent can actually call. Enumerated from the Go
 * registry (core/internal/tools/*.go, plus the tools registered in
 * internal/agent, internal/browser, internal/calendar, internal/contacts,
 * internal/extensions, internal/eval, internal/initiative, internal/phone,
 * internal/proactive, internal/skills, internal/worldmodel) and from the MCP
 * namespaces in core/config/mcp.yaml.
 *
 *  present / past  - the stems. "Editing" / "Edited".
 *  object          - the singular object phrase used when no subject resolves.
 *                    "Running" + "a command" → "Running a command".
 *  plural          - the counted noun used in a folded row.
 *                    "Ran" + 3 + "commands" → "Ran 3 commands".
 *  subject         - resolves a better object from the call's input, so a real
 *                    edit reads "Editing migrate.go" instead of "Editing a file".
 *  meta            - the quiet second line, taken from the RIGHT input field.
 *  group           - overrides the counted label where the default reads badly
 *                    ("Searched 3 the webs").
 *  solo            - never folds into a run (decision cards, §6).
 */
type VerbSpec = {
  present: string;
  past: string;
  object: string;
  plural: string;
  glyph: string;
  kind: DetailKind;
  subject?: (input: Rec | undefined) => string;
  meta?: (input: Rec | undefined) => string;
  group?: (n: number, past: string) => string;
  weight?: number;
  solo?: boolean;
};

/** How telling a kind of work is, when summaryFor picks three verbs to name. */
const KIND_WEIGHT: Record<DetailKind, number> = {
  failure: 7,
  approval: 6,
  commit: 5,
  diff: 4,
  plan: 4,
  goal: 3,
  terminal: 3,
  note: 2,
  search: 2,
  files: 1,
  kv: 1,
  thought: 0,
};

const timesGroup = (n: number, past: string) => `${past} ${n} times`;

const VERBS: Record<string, VerbSpec> = {
  // ── Files: cloud bridge + Mac (claude_code) ───────────────────────────────
  fs_read: { present: "Reading", past: "Read", object: "a file", plural: "files", glyph: "FileText", kind: "files", subject: fileSubject, meta: fileMeta },
  fs_ls: { present: "Listing", past: "Listed", object: "a folder", plural: "folders", glyph: "FolderOpen", kind: "files", subject: fileSubject, meta: fileMeta },
  fs_save: { present: "Writing", past: "Wrote", object: "a file", plural: "files", glyph: "FilePlus", kind: "diff", subject: fileSubject, meta: fileMeta },
  fs_edit: { present: "Editing", past: "Edited", object: "a file", plural: "files", glyph: "FilePen", kind: "diff", subject: fileSubject, meta: fileMeta },
  bash_run: { present: "Running", past: "Ran", object: "a command", plural: "commands", glyph: "Terminal", kind: "terminal", meta: (i) => oneLine(str(i, "command", "cmd")) },
  claude_code__read: { present: "Reading", past: "Read", object: "a file", plural: "files", glyph: "FileText", kind: "files", subject: fileSubject, meta: fileMeta },
  claude_code__write: { present: "Writing", past: "Wrote", object: "a file", plural: "files", glyph: "FilePlus", kind: "diff", subject: fileSubject, meta: fileMeta },
  claude_code__edit: { present: "Editing", past: "Edited", object: "a file", plural: "files", glyph: "FilePen", kind: "diff", subject: fileSubject, meta: fileMeta },
  claude_code__multiedit: { present: "Editing", past: "Edited", object: "a file", plural: "files", glyph: "FilePen", kind: "diff", subject: fileSubject, meta: fileMeta },
  claude_code__notebookedit: { present: "Editing", past: "Edited", object: "a notebook", plural: "notebooks", glyph: "NotebookPen", kind: "diff", subject: fileSubject, meta: fileMeta },
  claude_code__bash: { present: "Running", past: "Ran", object: "a command", plural: "commands", glyph: "Terminal", kind: "terminal", meta: (i) => oneLine(str(i, "command", "cmd")) },
  claude_code__ls: { present: "Listing", past: "Listed", object: "a folder", plural: "folders", glyph: "FolderOpen", kind: "files", subject: fileSubject, meta: fileMeta },
  claude_code__list: { present: "Listing", past: "Listed", object: "a folder", plural: "folders", glyph: "FolderOpen", kind: "files", subject: fileSubject, meta: fileMeta },
  claude_code__grep: { present: "Searching", past: "Searched", object: "the code", plural: "the code", glyph: "ScanSearch", kind: "search", group: timesGroup, meta: (i) => oneLine(str(i, "pattern", "query", "path")) },
  claude_code__glob: { present: "Finding", past: "Found", object: "files", plural: "files", glyph: "Search", kind: "files", meta: (i) => oneLine(str(i, "pattern", "path")) },

  // ── Git on the active bridge ──────────────────────────────────────────────
  git_status: { present: "Checking", past: "Checked", object: "the repo", plural: "the repo", glyph: "GitBranch", kind: "terminal", group: timesGroup },
  git_diff: { present: "Reviewing", past: "Reviewed", object: "the changes", plural: "the changes", glyph: "GitBranch", kind: "diff", group: timesGroup },
  git_stage: { present: "Staging", past: "Staged", object: "the changes", plural: "the changes", glyph: "GitBranch", kind: "commit", group: timesGroup },
  git_commit: { present: "Committing", past: "Committed", object: "the changes", plural: "commits", glyph: "GitCommitHorizontal", kind: "commit", group: (n) => `Made ${n} commits`, meta: (i) => oneLine(str(i, "message")) },
  git_push: { present: "Pushing", past: "Pushed", object: "to the remote", plural: "the remote", glyph: "Upload", kind: "commit", group: timesGroup },
  git_pull: { present: "Pulling", past: "Pulled", object: "from the remote", plural: "the remote", glyph: "Download", kind: "commit", group: timesGroup },

  // ── GitHub MCP (github__*) ────────────────────────────────────────────────
  github__get_file_contents: { present: "Reading", past: "Read", object: "a file on GitHub", plural: "files on GitHub", glyph: "FileText", kind: "files", subject: fileSubject, meta: fileMeta },
  github__create_or_update_file: { present: "Writing", past: "Wrote", object: "a file on GitHub", plural: "files on GitHub", glyph: "FilePlus", kind: "diff", subject: fileSubject, meta: fileMeta },
  github__push_files: { present: "Pushing", past: "Pushed", object: "files to GitHub", plural: "files to GitHub", glyph: "Upload", kind: "commit", meta: (i) => oneLine(extractToolFilePaths(i).map((p) => basename(p)).join(", ")) },
  github__delete_file: { present: "Deleting", past: "Deleted", object: "a file on GitHub", plural: "files on GitHub", glyph: "Trash2", kind: "diff", subject: fileSubject, meta: fileMeta },
  github__create_pull_request: { present: "Opening", past: "Opened", object: "a pull request", plural: "pull requests", glyph: "GitPullRequest", kind: "commit", meta: (i) => oneLine(str(i, "title")) },
  github__update_pull_request: { present: "Updating", past: "Updated", object: "a pull request", plural: "pull requests", glyph: "GitPullRequest", kind: "commit", meta: (i) => oneLine(str(i, "title")) },
  github__update_pull_request_branch: { present: "Updating", past: "Updated", object: "a pull request", plural: "pull requests", glyph: "GitPullRequest", kind: "commit" },
  github__merge_pull_request: { present: "Merging", past: "Merged", object: "a pull request", plural: "pull requests", glyph: "GitPullRequest", kind: "commit", meta: (i) => oneLine(str(i, "commit_title", "title")) },
  github__create_branch: { present: "Creating", past: "Created", object: "a branch", plural: "branches", glyph: "GitBranch", kind: "commit", meta: (i) => oneLine(str(i, "branch", "name")) },
  github__create_issue: { present: "Opening", past: "Opened", object: "an issue", plural: "issues", glyph: "CircleAlert", kind: "note", meta: (i) => oneLine(str(i, "title")) },
  github__list_issues: { present: "Checking", past: "Checked", object: "GitHub issues", plural: "GitHub issues", glyph: "CircleAlert", kind: "kv", group: timesGroup },
  github__search_code: { present: "Searching", past: "Searched", object: "GitHub", plural: "GitHub", glyph: "Search", kind: "search", group: timesGroup, meta: (i) => oneLine(str(i, "q", "query")) },

  // ── Memory ────────────────────────────────────────────────────────────────
  remember: { present: "Saving", past: "Saved", object: "a note", plural: "notes", glyph: "Bookmark", kind: "note", meta: (i) => oneLine(str(i, "text", "content", "fact")) },
  recall: { present: "Searching", past: "Searched", object: "memory", plural: "memory", glyph: "Brain", kind: "search", group: timesGroup, meta: (i) => oneLine(str(i, "query", "q")) },
  forget: { present: "Archiving", past: "Archived", object: "a memory", plural: "memories", glyph: "Trash2", kind: "note" },
  mem_list: { present: "Reading", past: "Read", object: "his own records", plural: "record sets", glyph: "Database", kind: "kv", meta: (i) => oneLine(str(i, "table")) },
  mem_act: { present: "Applying", past: "Applied", object: "an action", plural: "actions", glyph: "Zap", kind: "kv", meta: (i) => oneLine(str(i, "action", "table")) },
  action_register: { present: "Registering", past: "Registered", object: "an action", plural: "actions", glyph: "Zap", kind: "kv", meta: (i) => oneLine(str(i, "action", "table")) },
  action_list: { present: "Checking", past: "Checked", object: "registered actions", plural: "registered actions", glyph: "Database", kind: "kv", group: timesGroup },
  compact_context: { present: "Compacting", past: "Compacted", object: "the conversation", plural: "the conversation", glyph: "Layers", kind: "note", group: timesGroup },

  // ── Plans ─────────────────────────────────────────────────────────────────
  plan_create: { present: "Laying out", past: "Laid out", object: "a plan", plural: "plans", glyph: "ListChecks", kind: "plan", solo: true, meta: (i) => oneLine(str(i, "title", "goal", "task")) },
  plan_approve: { present: "Approving", past: "Approved", object: "the plan", plural: "plans", glyph: "SquareCheckBig", kind: "plan" },
  // plan_resume: the boss said carry on with work already approved. Its own
  // verb because "Advancing the plan" would hide the thing he asked for.
  plan_resume: { present: "Picking back up", past: "Picked back up", object: "the plan", plural: "plans", glyph: "ListChecks", kind: "plan" },
  plan_update: { present: "Advancing", past: "Advanced", object: "the plan", plural: "plan steps", glyph: "ListChecks", kind: "plan", meta: (i) => oneLine(str(i, "status", "step_id")) },
  plan_verify: { present: "Verifying", past: "Verified", object: "a step", plural: "steps", glyph: "ShieldCheck", kind: "plan", meta: (i) => oneLine(str(i, "evidence", "step_id")) },
  plan_get: { present: "Checking", past: "Checked", object: "the plan", plural: "the plan", glyph: "ListChecks", kind: "plan", group: timesGroup },
  plan_list: { present: "Listing", past: "Listed", object: "plans", plural: "plans", glyph: "ListChecks", kind: "plan", group: timesGroup },
  plan_revise: { present: "Revising", past: "Revised", object: "the plan", plural: "plans", glyph: "ListChecks", kind: "plan", solo: true, meta: (i) => oneLine(str(i, "reason", "title")) },
  plan_cancel: { present: "Cancelling", past: "Cancelled", object: "the plan", plural: "plans", glyph: "Ban", kind: "plan", meta: (i) => oneLine(str(i, "reason")) },

  // ── Goals and the world model ─────────────────────────────────────────────
  goal_set: { present: "Setting", past: "Set", object: "a goal", plural: "goals", glyph: "Target", kind: "goal", meta: (i) => oneLine(str(i, "title", "goal", "name")) },
  goal_update: { present: "Updating", past: "Updated", object: "the goal", plural: "goals", glyph: "Target", kind: "goal", meta: (i) => oneLine(str(i, "progress_note", "status", "title")) },
  goal_list: { present: "Checking", past: "Checked", object: "his goals", plural: "his goals", glyph: "Target", kind: "goal", group: timesGroup },
  worldmodel_extract: { present: "Extracting", past: "Extracted", object: "what he learned", plural: "batches", glyph: "Network", kind: "kv" },
  entity_upsert: { present: "Updating", past: "Updated", object: "the world model", plural: "entities", glyph: "Network", kind: "kv", meta: (i) => oneLine(str(i, "name", "entity_type")) },
  entity_link: { present: "Linking", past: "Linked", object: "two entities", plural: "entity links", glyph: "Waypoints", kind: "kv", meta: (i) => oneLine(str(i, "relation", "relation_type")) },
  entity_get: { present: "Reading", past: "Read", object: "an entity", plural: "entities", glyph: "Network", kind: "kv", meta: (i) => oneLine(str(i, "name", "id")) },
  entity_search: { present: "Searching", past: "Searched", object: "the world model", plural: "the world model", glyph: "Network", kind: "search", group: timesGroup, meta: (i) => oneLine(str(i, "query", "q")) },

  // ── Dashboard: to-dos, pursuits, follow-ups, the shelf, the inbox ────────
  todo_write: { present: "Updating", past: "Updated", object: "the checklist", plural: "the checklist", glyph: "ListChecks", kind: "plan", group: timesGroup },
  task_create: { present: "Adding", past: "Added", object: "a to-do", plural: "to-dos", glyph: "SquareCheckBig", kind: "kv", meta: (i) => oneLine(str(i, "title")) },
  task_update: { present: "Updating", past: "Updated", object: "a to-do", plural: "to-dos", glyph: "SquareCheckBig", kind: "kv", meta: (i) => oneLine(str(i, "title", "status")) },
  task_done: { present: "Ticking off", past: "Ticked off", object: "a to-do", plural: "to-dos", glyph: "SquareCheckBig", kind: "kv" },
  task_list: { present: "Checking", past: "Checked", object: "his to-dos", plural: "his to-dos", glyph: "SquareCheckBig", kind: "kv", group: timesGroup },
  pursuit_create: { present: "Starting", past: "Started", object: "a pursuit", plural: "pursuits", glyph: "Flag", kind: "goal", meta: (i) => oneLine(str(i, "title", "name")) },
  pursuit_checkin: { present: "Checking in on", past: "Checked in on", object: "a pursuit", plural: "pursuits", glyph: "Flag", kind: "goal" },
  pursuit_list: { present: "Checking", past: "Checked", object: "his pursuits", plural: "his pursuits", glyph: "Flag", kind: "goal", group: timesGroup },
  pursuit_pc_state: { present: "Reading", past: "Read", object: "the pursuit", plural: "pursuits", glyph: "Flag", kind: "goal" },
  pursuit_pc_write: { present: "Updating", past: "Updated", object: "the pursuit", plural: "pursuits", glyph: "Flag", kind: "goal" },
  followup_snooze: { present: "Snoozing", past: "Snoozed", object: "a follow-up", plural: "follow-ups", glyph: "Clock", kind: "kv" },
  followup_dismiss: { present: "Clearing", past: "Cleared", object: "a follow-up", plural: "follow-ups", glyph: "Inbox", kind: "kv", meta: (i) => oneLine(str(i, "outcome")) },
  followup_list: { present: "Checking", past: "Checked", object: "follow-ups", plural: "follow-ups", glyph: "Inbox", kind: "kv", group: timesGroup },
  read_email: { present: "Reading", past: "Read", object: "an email", plural: "emails", glyph: "Mail", kind: "note" },
  saved_add: { present: "Saving", past: "Saved", object: "to the shelf", plural: "items to the shelf", glyph: "Bookmark", kind: "note", meta: (i) => oneLine(str(i, "title", "url", "text")) },
  saved_list: { present: "Checking", past: "Checked", object: "the shelf", plural: "the shelf", glyph: "Bookmark", kind: "kv", group: timesGroup },
  surface_item: { present: "Surfacing", past: "Surfaced", object: "an item", plural: "items", glyph: "Inbox", kind: "kv", meta: (i) => oneLine(str(i, "title", "surface")) },
  surface_update: { present: "Updating", past: "Updated", object: "a surfaced item", plural: "surfaced items", glyph: "Inbox", kind: "kv", meta: (i) => oneLine(str(i, "status", "title")) },
  surface_list: { present: "Checking", past: "Checked", object: "the dashboard", plural: "the dashboard", glyph: "Inbox", kind: "kv", group: timesGroup },

  // ── Mandates and tracked outcomes ─────────────────────────────────────────
  mandate_open: { present: "Opening", past: "Opened", object: "a mandate", plural: "mandates", glyph: "Flag", kind: "plan", meta: (i) => oneLine(str(i, "title", "goal")) },
  mandate_check: { present: "Checking off", past: "Checked off", object: "a criterion", plural: "criteria", glyph: "SquareCheckBig", kind: "plan", meta: (i) => oneLine(str(i, "evidence", "criterion")) },
  mandate_close: { present: "Closing", past: "Closed", object: "the mandate", plural: "mandates", glyph: "Flag", kind: "plan" },
  mandate_abandon: { present: "Dropping", past: "Dropped", object: "the mandate", plural: "mandates", glyph: "Ban", kind: "plan", meta: (i) => oneLine(str(i, "reason")) },
  mandate_verify: { present: "Double-checking", past: "Double-checked", object: "the work", plural: "the work", glyph: "Microscope", kind: "plan", group: timesGroup },
  outcome_track: { present: "Tracking", past: "Tracked", object: "a decision", plural: "decisions", glyph: "Flag", kind: "kv", meta: (i) => oneLine(str(i, "decision", "title")) },
  outcome_resolve: { present: "Closing", past: "Closed", object: "a decision", plural: "decisions", glyph: "Flag", kind: "kv", meta: (i) => oneLine(str(i, "result", "outcome")) },

  // ── Skills ────────────────────────────────────────────────────────────────
  skills_list: { present: "Checking", past: "Checked", object: "his skills", plural: "his skills", glyph: "Puzzle", kind: "kv", group: timesGroup },
  skills_invoke: { present: "Running", past: "Ran", object: "a skill", plural: "skills", glyph: "Puzzle", kind: "note", meta: (i) => oneLine(str(i, "name", "skill")) },
  skills_discover: { present: "Searching", past: "Searched", object: "his skills", plural: "his skills", glyph: "Puzzle", kind: "search", group: timesGroup, meta: (i) => oneLine(str(i, "query", "q")) },
  skills_history: { present: "Checking", past: "Checked", object: "past skill runs", plural: "past skill runs", glyph: "History", kind: "kv", group: timesGroup },
  skill_create: { present: "Writing", past: "Wrote", object: "a new skill", plural: "new skills", glyph: "Puzzle", kind: "note", solo: true, meta: (i) => oneLine(str(i, "name", "title")) },
  skill_propose: { present: "Proposing", past: "Proposed", object: "a skill", plural: "skills", glyph: "Puzzle", kind: "note", solo: true, meta: (i) => oneLine(str(i, "name", "title")) },
  skill_optimize: { present: "Improving", past: "Improved", object: "a skill", plural: "skills", glyph: "Puzzle", kind: "note", solo: true, meta: (i) => oneLine(str(i, "name", "skill")) },
  skill_proposal_get: { present: "Checking", past: "Checked", object: "a skill proposal", plural: "skill proposals", glyph: "Puzzle", kind: "kv" },
  skill_tests: { present: "Checking", past: "Checked", object: "a skill's tests", plural: "skill tests", glyph: "ShieldCheck", kind: "kv" },
  skill_test_generate: { present: "Writing", past: "Wrote", object: "a skill test", plural: "skill tests", glyph: "ShieldCheck", kind: "note" },

  // ── Cron, sentinels, watches ──────────────────────────────────────────────
  cron_create_agent: { present: "Scheduling", past: "Scheduled", object: "a job", plural: "jobs", glyph: "Clock", kind: "kv", meta: (i) => oneLine(str(i, "name", "schedule")) },
  cron_create_poll: { present: "Scheduling", past: "Scheduled", object: "a poll", plural: "polls", glyph: "Clock", kind: "kv", meta: (i) => oneLine(str(i, "name", "schedule")) },
  cron_list: { present: "Checking", past: "Checked", object: "the schedule", plural: "the schedule", glyph: "Clock", kind: "kv", group: timesGroup },
  cron_delete: { present: "Deleting", past: "Deleted", object: "a job", plural: "jobs", glyph: "Trash2", kind: "kv", meta: (i) => oneLine(str(i, "name", "id")) },
  cron_pause: { present: "Pausing", past: "Paused", object: "a job", plural: "jobs", glyph: "Pause", kind: "kv", meta: (i) => oneLine(str(i, "name", "id")) },
  cron_run_now: { present: "Firing", past: "Fired", object: "a job", plural: "jobs", glyph: "Play", kind: "kv", meta: (i) => oneLine(str(i, "name", "id")) },
  sentinel_create: { present: "Setting up", past: "Set up", object: "a watcher", plural: "watchers", glyph: "Radar", kind: "kv", meta: (i) => oneLine(str(i, "name", "watch_type")) },
  sentinel_list: { present: "Checking", past: "Checked", object: "his watchers", plural: "his watchers", glyph: "Radar", kind: "kv", group: timesGroup },
  sentinel_delete: { present: "Removing", past: "Removed", object: "a watcher", plural: "watchers", glyph: "Trash2", kind: "kv", meta: (i) => oneLine(str(i, "name", "id")) },
  sentinel_pause: { present: "Pausing", past: "Paused", object: "a watcher", plural: "watchers", glyph: "Pause", kind: "kv", meta: (i) => oneLine(str(i, "name", "id")) },
  sentinel_test: { present: "Testing", past: "Tested", object: "a watcher", plural: "watchers", glyph: "Radar", kind: "kv", meta: (i) => oneLine(str(i, "name", "id")) },
  watch_until: { present: "Watching", past: "Watched", object: "for the result", plural: "things", glyph: "Eye", kind: "kv", meta: (i) => oneLine(str(i, "label", "target", "kind")) },

  // ── Workflows ─────────────────────────────────────────────────────────────
  workflow_create: { present: "Saving", past: "Saved", object: "a workflow", plural: "workflows", glyph: "Route", kind: "plan", meta: (i) => oneLine(str(i, "name", "title")) },
  workflow_run: { present: "Running", past: "Ran", object: "a workflow", plural: "workflows", glyph: "Route", kind: "plan", meta: (i) => oneLine(str(i, "name", "workflow")) },
  workflow_status: { present: "Checking", past: "Checked", object: "a workflow", plural: "workflows", glyph: "Route", kind: "kv", group: timesGroup },
  workflow_resume: { present: "Resuming", past: "Resumed", object: "a workflow", plural: "workflows", glyph: "Play", kind: "plan" },
  workflow_cancel: { present: "Cancelling", past: "Cancelled", object: "a workflow", plural: "workflows", glyph: "Ban", kind: "plan" },
  workflow_validate: { present: "Checking", past: "Checked", object: "a workflow", plural: "workflows", glyph: "ShieldCheck", kind: "kv" },
  workflow_list: { present: "Listing", past: "Listed", object: "workflows", plural: "workflows", glyph: "Route", kind: "kv", group: timesGroup },

  // ── The web, code, media, documents, artifacts ───────────────────────────
  web_search: { present: "Searching", past: "Searched", object: "the web", plural: "the web", glyph: "Globe", kind: "search", group: timesGroup, meta: (i) => oneLine(str(i, "query", "q")) },
  http_fetch: { present: "Fetching", past: "Fetched", object: "a page", plural: "pages", glyph: "Globe", kind: "note", meta: (i) => oneLine(str(i, "url")) },
  code_exec: { present: "Running", past: "Ran", object: "some code", plural: "snippets", glyph: "Terminal", kind: "terminal", meta: (i) => oneLine(str(i, "code", "source")) },
  media_job: { present: "Making", past: "Made", object: "media", plural: "media files", glyph: "Image", kind: "note", meta: (i) => oneLine(str(i, "prompt", "command", "kind")) },
  document_create: { present: "Writing", past: "Wrote", object: "a document", plural: "documents", glyph: "FileSpreadsheet", kind: "note", meta: (i) => oneLine(str(i, "filename", "title", "format")) },
  artifact_save: { present: "Saving", past: "Saved", object: "an artifact", plural: "artifacts", glyph: "Save", kind: "kv", meta: (i) => oneLine(str(i, "name", "virtual_path")) },
  artifact_list: { present: "Checking", past: "Checked", object: "his artifacts", plural: "his artifacts", glyph: "Layers", kind: "kv", group: timesGroup },
  artifact_get: { present: "Opening", past: "Opened", object: "an artifact", plural: "artifacts", glyph: "Layers", kind: "kv", meta: (i) => oneLine(str(i, "name", "virtual_path", "id")) },
  artifact_delete: { present: "Deleting", past: "Deleted", object: "an artifact", plural: "artifacts", glyph: "Trash2", kind: "kv" },

  // ── The browser ───────────────────────────────────────────────────────────
  browser_open: { present: "Opening", past: "Opened", object: "the browser", plural: "browser sessions", glyph: "MonitorPlay", kind: "note", meta: (i) => oneLine(str(i, "url")) },
  browser_navigate: { present: "Going to", past: "Went to", object: "a page", plural: "pages", glyph: "Globe", kind: "note", meta: (i) => oneLine(str(i, "url")) },
  browser_observe: { present: "Looking at", past: "Looked at", object: "the page", plural: "pages", glyph: "Eye", kind: "note" },
  browser_act: { present: "Clicking through", past: "Clicked through", object: "the page", plural: "pages", glyph: "MousePointerClick", kind: "note", meta: (i) => oneLine(str(i, "action", "text", "index")) },
  browser_extract: { present: "Reading", past: "Read", object: "the page", plural: "pages", glyph: "BookOpen", kind: "note" },
  browser_close: { present: "Closing", past: "Closed", object: "the browser", plural: "browser sessions", glyph: "MonitorPlay", kind: "note" },
  browser_request_takeover: { present: "Handing over", past: "Handed over", object: "the browser", plural: "the browser", glyph: "Hand", kind: "approval", solo: true, meta: (i) => oneLine(str(i, "reason")) },

  // ── Calendar, contacts, phone, reaching the boss ─────────────────────────
  calendar_respond: { present: "Replying to", past: "Replied to", object: "an invite", plural: "invites", glyph: "Calendar", kind: "kv", meta: (i) => oneLine(str(i, "response", "event_id")) },
  calendar_sync_now: { present: "Syncing", past: "Synced", object: "the calendar", plural: "the calendar", glyph: "RefreshCw", kind: "kv", group: timesGroup },
  calendar_create: { present: "Booking", past: "Booked", object: "an event", plural: "events", glyph: "Calendar", kind: "kv", meta: (i) => oneLine(str(i, "summary", "title")) },
  calendar_patch: { present: "Updating", past: "Updated", object: "an event", plural: "events", glyph: "Calendar", kind: "kv", meta: (i) => oneLine(str(i, "summary", "event_id")) },
  calendar_delete: { present: "Cancelling", past: "Cancelled", object: "an event", plural: "events", glyph: "Calendar", kind: "kv", meta: (i) => oneLine(str(i, "event_id")) },
  contact_save: { present: "Saving", past: "Saved", object: "a contact", plural: "contacts", glyph: "Contact", kind: "kv", meta: (i) => oneLine(str(i, "name")) },
  contact_find: { present: "Looking up", past: "Looked up", object: "a contact", plural: "contacts", glyph: "Contact", kind: "search", meta: (i) => oneLine(str(i, "name", "query")) },
  phone_call: { present: "Calling", past: "Called", object: "someone", plural: "people", glyph: "Phone", kind: "note", group: (n) => `Made ${n} calls`, meta: (i) => oneLine(str(i, "to", "to_number", "name", "purpose")) },
  notify: { present: "Pinging", past: "Pinged", object: "you", plural: "times", glyph: "Bell", kind: "note", meta: (i) => oneLine(str(i, "title", "message", "text")) },
  notification_digest: { present: "Sending", past: "Sent", object: "the digest", plural: "digests", glyph: "Bell", kind: "note" },
  intervention_score: { present: "Weighing", past: "Weighed", object: "whether to interrupt", plural: "decisions", glyph: "Gauge", kind: "kv" },
  cost_record: { present: "Logging", past: "Logged", object: "a cost", plural: "costs", glyph: "DollarSign", kind: "kv", meta: (i) => oneLine(str(i, "kind", "amount_usd", "label")) },
  budget_status: { present: "Checking", past: "Checked", object: "the budget", plural: "the budget", glyph: "DollarSign", kind: "kv", group: timesGroup },

  // ── Connectors, extensions, self-knowledge, settings ─────────────────────
  connector_identity_set: { present: "Recording", past: "Recorded", object: "an account identity", plural: "account identities", glyph: "Link", kind: "kv", meta: (i) => oneLine(str(i, "identity", "toolkit")) },
  connector_accounts_list: { present: "Checking", past: "Checked", object: "connected accounts", plural: "connected accounts", glyph: "Link", kind: "kv", group: timesGroup },
  connector_coverage_mark: { present: "Marking", past: "Marked", object: "an account covered", plural: "accounts covered", glyph: "Link", kind: "kv", meta: (i) => oneLine(str(i, "toolkit", "status")) },
  extension_register: { present: "Adding", past: "Added", object: "a tool", plural: "tools", glyph: "Puzzle", kind: "kv", meta: (i) => oneLine(str(i, "name", "kind")) },
  extension_list: { present: "Checking", past: "Checked", object: "his extensions", plural: "his extensions", glyph: "Puzzle", kind: "kv", group: timesGroup },
  extension_remove: { present: "Removing", past: "Removed", object: "an extension", plural: "extensions", glyph: "Puzzle", kind: "kv", meta: (i) => oneLine(str(i, "name")) },
  extension_check: { present: "Checking", past: "Checked", object: "an extension", plural: "extensions", glyph: "Puzzle", kind: "kv", meta: (i) => oneLine(str(i, "name")) },
  extension_activate: { present: "Signing in to", past: "Signed in to", object: "a tool", plural: "tools", glyph: "Lock", kind: "note", meta: (i) => oneLine(str(i, "name")) },
  system_map: { present: "Looking at", past: "Looked at", object: "his own wiring", plural: "his own wiring", glyph: "Network", kind: "kv", group: timesGroup },
  domain_hint_add: { present: "Recording", past: "Recorded", object: "a mapping", plural: "mappings", glyph: "Network", kind: "kv", meta: (i) => oneLine(str(i, "table", "tool_prefix")) },
  domain_hint_list: { present: "Checking", past: "Checked", object: "his mappings", plural: "his mappings", glyph: "Network", kind: "kv", group: timesGroup },
  tool_search: { present: "Looking for", past: "Looked for", object: "a tool", plural: "tools", glyph: "ScanSearch", kind: "search", meta: (i) => oneLine(str(i, "query", "q")) },
  // Plumbing, and it must READ as plumbing. "Picked up a tool · plan_cancel"
  // put a bare identifier on a row that sounded like it was describing work,
  // which is exactly the junk the boss called out. "Loaded" is what it is; the
  // identifier stays in the quiet mono meta where an identifier belongs, and a
  // run of them always folds into one line.
  load_tools: { present: "Loading", past: "Loaded", object: "a tool", plural: "tools", glyph: "Wrench", kind: "kv", group: (n, p) => `${p} ${n} tools`, meta: (i) => oneLine(namesOf(i)) },
  unload_tools: { present: "Putting away", past: "Put away", object: "a tool", plural: "tools", glyph: "Wrench", kind: "kv", meta: (i) => oneLine(namesOf(i)) },
  state_set: { present: "Saving", past: "Saved", object: "a setting", plural: "settings", glyph: "Settings", kind: "kv", meta: (i) => oneLine(str(i, "key")) },
  state_get: { present: "Reading", past: "Read", object: "a setting", plural: "settings", glyph: "Settings", kind: "kv", meta: (i) => oneLine(str(i, "key")) },
  state_list: { present: "Listing", past: "Listed", object: "his settings", plural: "his settings", glyph: "Settings", kind: "kv", group: timesGroup },
  state_delete: { present: "Clearing", past: "Cleared", object: "a setting", plural: "settings", glyph: "Settings", kind: "kv", meta: (i) => oneLine(str(i, "key")) },

  // ── Delegation and heavy work ─────────────────────────────────────────────
  delegate: { present: "Delegating", past: "Delegated", object: "to a sub-agent", plural: "sub-agents", glyph: "Bot", kind: "note", meta: (i) => oneLine(str(i, "task", "prompt", "goal")) },
  delegate_parallel: { present: "Delegating", past: "Delegated", object: "to several sub-agents", plural: "sub-agents", glyph: "Bot", kind: "note", meta: (i) => oneLine(str(i, "task", "prompt")) },
  background_build: { present: "Kicking off", past: "Kicked off", object: "a background build", plural: "background builds", glyph: "Blocks", kind: "note", meta: (i) => oneLine(str(i, "task", "title", "prompt")) },
  agent_team_start: { present: "Starting", past: "Started", object: "an agent team", plural: "agent teams", glyph: "Users", kind: "note", solo: true, meta: (i) => oneLine(str(i, "goal", "task", "title")) },
  code_agent: { present: "Handing", past: "Handed", object: "the work to Claude Code", plural: "coding tasks", glyph: "Terminal", kind: "terminal", meta: (i) => oneLine(str(i, "task", "prompt")) },

  // ── Self-improvement, traces, evaluation, trust, curiosity ───────────────
  self_improve_control: { present: "Adjusting", past: "Adjusted", object: "his self-improve loop", plural: "settings", glyph: "RefreshCw", kind: "kv", meta: (i) => oneLine(str(i, "action")) },
  code_proposal_decide: { present: "Deciding on", past: "Decided on", object: "a code proposal", plural: "code proposals", glyph: "SquareCheckBig", kind: "kv", meta: (i) => oneLine(str(i, "status", "id")) },
  deploy_status: { present: "Checking", past: "Checked", object: "the deploy", plural: "the deploy", glyph: "RadioTower", kind: "kv", group: timesGroup },
  deploy_status_refresh: { present: "Re-checking", past: "Re-checked", object: "the deploy", plural: "the deploy", glyph: "RefreshCw", kind: "kv", group: timesGroup },
  traces_recent: { present: "Reviewing", past: "Reviewed", object: "recent turns", plural: "recent turns", glyph: "History", kind: "kv", group: timesGroup },
  trace_inspect: { present: "Inspecting", past: "Inspected", object: "a turn", plural: "turns", glyph: "History", kind: "kv", meta: (i) => oneLine(str(i, "turn_id", "id")) },
  traces_search: { present: "Searching", past: "Searched", object: "past turns", plural: "past turns", glyph: "History", kind: "search", group: timesGroup, meta: (i) => oneLine(str(i, "query", "q")) },
  eval_record: { present: "Scoring", past: "Scored", object: "a run", plural: "runs", glyph: "Gauge", kind: "kv", meta: (i) => oneLine(str(i, "capability", "outcome")) },
  eval_scorecard: { present: "Checking", past: "Checked", object: "the scorecard", plural: "scorecards", glyph: "Gauge", kind: "kv" },
  trust_batch_assign: { present: "Batching", past: "Batched", object: "approvals", plural: "approvals", glyph: "ShieldCheck", kind: "approval" },
  question_list: { present: "Checking", past: "Checked", object: "his open questions", plural: "his open questions", glyph: "CircleAlert", kind: "kv", group: timesGroup },
  question_decide: { present: "Closing", past: "Closed", object: "a question", plural: "questions", glyph: "CircleAlert", kind: "kv", meta: (i) => oneLine(str(i, "decision", "id")) },

  // ── Preview and projects ──────────────────────────────────────────────────
  preview_start: { present: "Starting", past: "Started", object: "the preview", plural: "previews", glyph: "Play", kind: "note", meta: (i) => oneLine(str(i, "project", "path")) },
  preview_stop: { present: "Stopping", past: "Stopped", object: "the preview", plural: "previews", glyph: "Pause", kind: "note" },
  preview_status: { present: "Checking", past: "Checked", object: "the preview", plural: "the preview", glyph: "MonitorPlay", kind: "kv", group: timesGroup },
  project_create: { present: "Creating", past: "Created", object: "a project", plural: "projects", glyph: "FolderOpen", kind: "kv", meta: (i) => oneLine(str(i, "name", "path")) },
  project_clone: { present: "Cloning", past: "Cloned", object: "a repo", plural: "repos", glyph: "GitBranch", kind: "kv", meta: (i) => oneLine(str(i, "url", "repo", "name")) },
  project_open: { present: "Opening", past: "Opened", object: "a project", plural: "projects", glyph: "FolderOpen", kind: "kv", meta: (i) => oneLine(str(i, "name", "path")) },

  // ── Claude Code's own extras (Mac bridge) ────────────────────────────────
  claude_code__agent: { present: "Delegating", past: "Delegated", object: "to a sub-agent", plural: "sub-agents", glyph: "Bot", kind: "note", meta: (i) => oneLine(str(i, "prompt", "description")) },
  claude_code__skill: { present: "Running", past: "Ran", object: "a skill", plural: "skills", glyph: "Puzzle", kind: "note", meta: (i) => oneLine(str(i, "skill", "name")) },
  claude_code__todowrite: { present: "Updating", past: "Updated", object: "the checklist", plural: "the checklist", glyph: "ListChecks", kind: "plan", group: timesGroup },
  claude_code__websearch: { present: "Searching", past: "Searched", object: "the web", plural: "the web", glyph: "Globe", kind: "search", group: timesGroup, meta: (i) => oneLine(str(i, "query", "q")) },
  claude_code__webfetch: { present: "Fetching", past: "Fetched", object: "a page", plural: "pages", glyph: "Globe", kind: "note", meta: (i) => oneLine(str(i, "url")) },
  claude_code__toolsearch: { present: "Looking for", past: "Looked for", object: "a tool", plural: "tools", glyph: "ScanSearch", kind: "search", meta: (i) => oneLine(str(i, "query")) },
  claude_code__taskoutput: { present: "Checking", past: "Checked", object: "a task", plural: "tasks", glyph: "Bot", kind: "kv" },
  claude_code__taskstop: { present: "Stopping", past: "Stopped", object: "a task", plural: "tasks", glyph: "Ban", kind: "kv" },
  claude_code__monitor: { present: "Watching", past: "Watched", object: "for a change", plural: "changes", glyph: "Eye", kind: "kv" },
  claude_code__askuserquestion: { present: "Asking", past: "Asked", object: "you a question", plural: "questions", glyph: "MessageSquare", kind: "approval", solo: true, meta: (i) => oneLine(str(i, "question", "prompt")) },
  claude_code__enterplanmode: { present: "Switching to", past: "Switched to", object: "planning", plural: "planning", glyph: "ListChecks", kind: "plan" },
  claude_code__exitplanmode: { present: "Leaving", past: "Left", object: "planning", plural: "planning", glyph: "ListChecks", kind: "plan" },
  claude_code__enterworktree: { present: "Opening", past: "Opened", object: "a worktree", plural: "worktrees", glyph: "GitBranch", kind: "kv" },
  claude_code__exitworktree: { present: "Closing", past: "Closed", object: "a worktree", plural: "worktrees", glyph: "GitBranch", kind: "kv" },
  claude_code__croncreate: { present: "Scheduling", past: "Scheduled", object: "a job", plural: "jobs", glyph: "Clock", kind: "kv" },
  claude_code__cronlist: { present: "Checking", past: "Checked", object: "the schedule", plural: "the schedule", glyph: "Clock", kind: "kv", group: timesGroup },
  claude_code__crondelete: { present: "Deleting", past: "Deleted", object: "a job", plural: "jobs", glyph: "Trash2", kind: "kv" },
  claude_code__pushnotification: { present: "Pinging", past: "Pinged", object: "you", plural: "times", glyph: "Bell", kind: "note" },
  claude_code__remotetrigger: { present: "Triggering", past: "Triggered", object: "a remote run", plural: "remote runs", glyph: "RadioTower", kind: "kv" },
  claude_code__schedulewakeup: { present: "Setting", past: "Set", object: "a wake-up", plural: "wake-ups", glyph: "Clock", kind: "kv" },

  // ── Composio (gateway). Named entries for the high-traffic Gmail /
  //    Calendar / GitHub verbs; everything else falls to composioSpec() below.
  composio__gmail_fetch_emails: { present: "Checking", past: "Checked", object: "the inbox", plural: "the inbox", glyph: "Inbox", kind: "kv", group: timesGroup, meta: (i) => oneLine(str(i, "query", "user_id")) },
  composio__gmail_list_messages: { present: "Checking", past: "Checked", object: "the inbox", plural: "the inbox", glyph: "Inbox", kind: "kv", group: timesGroup, meta: (i) => oneLine(str(i, "query", "user_id")) },
  composio__gmail_list_threads: { present: "Checking", past: "Checked", object: "the inbox", plural: "the inbox", glyph: "Inbox", kind: "kv", group: timesGroup, meta: (i) => oneLine(str(i, "query", "user_id")) },
  composio__gmail_fetch_message_by_message_id: { present: "Reading", past: "Read", object: "an email", plural: "emails", glyph: "Mail", kind: "note", meta: (i) => oneLine(str(i, "message_id")) },
  composio__gmail_fetch_message_by_thread_id: { present: "Reading", past: "Read", object: "an email", plural: "emails", glyph: "Mail", kind: "note", meta: (i) => oneLine(str(i, "thread_id")) },
  composio__gmail_send_email: { present: "Sending", past: "Sent", object: "an email", plural: "emails", glyph: "Send", kind: "note", meta: (i) => oneLine(str(i, "recipient_email", "to", "subject")) },
  composio__gmail_send: { present: "Sending", past: "Sent", object: "an email", plural: "emails", glyph: "Send", kind: "note", meta: (i) => oneLine(str(i, "recipient_email", "to", "subject")) },
  composio__gmail_send_draft: { present: "Sending", past: "Sent", object: "a draft", plural: "drafts", glyph: "Send", kind: "note", meta: (i) => oneLine(str(i, "draft_id", "subject")) },
  composio__gmail_create_email_draft: { present: "Drafting", past: "Drafted", object: "a reply", plural: "replies", glyph: "Pencil", kind: "note", meta: (i) => oneLine(str(i, "subject", "recipient_email", "to")) },
  composio__gmail_list_drafts: { present: "Checking", past: "Checked", object: "his drafts", plural: "his drafts", glyph: "Pencil", kind: "kv", group: timesGroup },
  composio__gmail_reply_to_thread: { present: "Replying to", past: "Replied to", object: "a thread", plural: "threads", glyph: "Reply", kind: "note", meta: (i) => oneLine(str(i, "thread_id", "recipient_email")) },
  composio__gmail_add_label_to_email: { present: "Filing", past: "Filed", object: "an email", plural: "emails", glyph: "Inbox", kind: "kv", meta: (i) => oneLine(str(i, "label_ids", "message_id")) },
  composio__gmail_remove_label: { present: "Filing", past: "Filed", object: "an email", plural: "emails", glyph: "Inbox", kind: "kv", meta: (i) => oneLine(str(i, "label_ids", "message_id")) },
  composio__gmail_modify_thread_labels: { present: "Filing", past: "Filed", object: "a thread", plural: "threads", glyph: "Inbox", kind: "kv", meta: (i) => oneLine(str(i, "thread_id")) },
  composio__googlecalendar_create_event: { present: "Booking", past: "Booked", object: "an event", plural: "events", glyph: "Calendar", kind: "kv", meta: (i) => oneLine(str(i, "summary", "start_datetime")) },
  composio__github_create_issue: { present: "Opening", past: "Opened", object: "an issue", plural: "issues", glyph: "CircleAlert", kind: "note", meta: (i) => oneLine(str(i, "title")) },
  composio__github_list_issues: { present: "Checking", past: "Checked", object: "GitHub issues", plural: "GitHub issues", glyph: "CircleAlert", kind: "kv", group: timesGroup },
};

function namesOf(input: Rec | undefined): string {
  if (!input) return "";
  const v = input["names"] ?? input["tools"] ?? input["name"];
  if (Array.isArray(v)) return v.filter((x) => typeof x === "string").join(", ");
  return typeof v === "string" ? v : "";
}

/**
 * Tools whose result is a DECISION the boss has to make. §6: these never fold
 * away into a counted run. Enforced structurally (a `solo` spec, checked in
 * coalesce) rather than left to the component to remember.
 */
export const DECISION_TOOLS: ReadonlySet<string> = new Set(
  Object.entries(VERBS)
    .filter(([, spec]) => spec.solo)
    .map(([name]) => name),
);

// ─────────────────────────────────────────────────────────────────────────────
// Unknown tools: humanize, never leak an id
// ─────────────────────────────────────────────────────────────────────────────

/** Display names for the MCP servers in core/config/mcp.yaml. */
const SERVER_LABELS: Record<string, string> = {
  claude_code: "Claude Code",
  github: "GitHub",
  composio: "Composio",
  filesystem: "Filesystem",
  tavily: "Tavily",
};

const SERVER_GLYPHS: Record<string, string> = {
  claude_code: "Terminal",
  github: "GitBranch",
  composio: "Link",
  filesystem: "FolderOpen",
  tavily: "Globe",
};

/** "COMPOSIO" / "google_calendar" / "googlecalendar" → readable toolkit names. */
const TOOLKIT_LABELS: Record<string, string> = {
  gmail: "Gmail",
  googlecalendar: "Google Calendar",
  google_calendar: "Google Calendar",
  googledrive: "Google Drive",
  googlesheets: "Google Sheets",
  googledocs: "Google Docs",
  github: "GitHub",
  slack: "Slack",
  notion: "Notion",
  linear: "Linear",
  x: "X",
  twitter: "X",
  stripe: "Stripe",
};

function titleCase(word: string): string {
  return word.charAt(0).toUpperCase() + word.slice(1);
}

/** camelCase / PascalCase / snake_case → lowercase words. */
function words(raw: string): string[] {
  return raw
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/[_\-.]+/g, " ")
    .trim()
    .split(/\s+/)
    .filter(Boolean)
    .map((w) => w.toLowerCase());
}

/**
 * Composio ids are `composio__<TOOLKIT>_<VERB>_<OBJECT…>`. Map the leading verb
 * to a real English pair so a row reads "Sending an invite · Google Calendar"
 * rather than "COMPOSIO GOOGLECALENDAR SEND INVITE".
 */
const COMPOSIO_VERBS: Record<string, [string, string]> = {
  fetch: ["Fetching", "Fetched"],
  get: ["Fetching", "Fetched"],
  list: ["Checking", "Checked"],
  search: ["Searching", "Searched"],
  find: ["Looking for", "Looked for"],
  send: ["Sending", "Sent"],
  create: ["Creating", "Created"],
  add: ["Adding", "Added"],
  update: ["Updating", "Updated"],
  modify: ["Updating", "Updated"],
  patch: ["Updating", "Updated"],
  edit: ["Editing", "Edited"],
  reply: ["Replying to", "Replied to"],
  delete: ["Deleting", "Deleted"],
  remove: ["Removing", "Removed"],
  archive: ["Archiving", "Archived"],
  move: ["Moving", "Moved"],
  post: ["Posting", "Posted"],
  upload: ["Uploading", "Uploaded"],
  download: ["Downloading", "Downloaded"],
};

function composioSpec(rest: string): VerbSpec {
  const parts = words(rest);
  const toolkitKey = (parts[0] ?? "").toLowerCase();
  const toolkit = TOOLKIT_LABELS[toolkitKey] ?? titleCase(toolkitKey || "Composio");
  const verbKey = parts[1] ?? "";
  const pair = COMPOSIO_VERBS[verbKey];
  const objectWords = pair ? parts.slice(2) : parts.slice(1);
  const object = objectWords.length > 0 ? objectWords.join(" ") : toolkit;
  const [present, past] = pair ?? ["Using", "Used"];
  return {
    present,
    past,
    object,
    plural: object,
    glyph: "Link",
    kind: "kv",
    group: (n, p) => `${p} ${object} ${n} times`,
    meta: () => toolkit,
  };
}

/**
 * fallbackSpec - the humanizer of last resort. `server__VERB` → "Server · verb"
 * (MAJORDOMO §6). A raw tool id can never come out of here: even a bare,
 * unnamespaced id becomes a sentence-cased phrase.
 */
function fallbackSpec(name: string): VerbSpec {
  const sep = name.indexOf("__");
  if (sep > 0) {
    const server = name.slice(0, sep).toLowerCase();
    const rest = name.slice(sep + 2);
    if (server === "composio") return composioSpec(rest);
    const label = SERVER_LABELS[server] ?? words(server).map(titleCase).join(" ");
    const verb = words(rest).join(" ") || "call";
    const phrase = `${label} · ${verb}`;
    return {
      present: phrase,
      past: phrase,
      object: "",
      plural: "",
      glyph: SERVER_GLYPHS[server] ?? "Wrench",
      kind: "kv",
      group: (n) => `${phrase} ×${n}`,
    };
  }
  const phrase = titleCase(words(name).join(" ")) || "Working";
  return {
    present: phrase,
    past: phrase,
    object: "",
    plural: "",
    glyph: "Wrench",
    kind: "kv",
    group: (n) => `${phrase} ×${n}`,
  };
}

function specFor(name: string): VerbSpec {
  const known = VERBS[name.toLowerCase()];
  if (known) return known;
  const spec = fallbackSpec(name);
  // An unknown tool can still be a code write / repo write (a new bridge verb,
  // a GitHub MCP name we have not enumerated). Reuse the canvas detectors so a
  // brand-new editor verb still renders as a diff and still counts as heavy
  // work in the summary, instead of degrading to a grey "kv" row.
  if (isCodeChangeTool(name)) {
    return { ...spec, kind: "diff", glyph: "FilePen", subject: fileSubject, meta: fileMeta };
  }
  if (isRepoWriteTool(name)) {
    return { ...spec, kind: "commit", glyph: "GitCommitHorizontal" };
  }
  return spec;
}

// ─────────────────────────────────────────────────────────────────────────────
// describeStep
// ─────────────────────────────────────────────────────────────────────────────

function phrase(stem: string, tail: string): string {
  return tail ? `${stem} ${tail}` : stem;
}

/**
 * describeStep - the words for ONE tool call.
 *
 * `result` is consulted only to sharpen the detail `kind`: a call that failed
 * wants the failure inset, a call the gate blocked wants the approval inset.
 * The verbs never change with the result: "Edited migrate.go" is what happened
 * whether or not it worked, and the status dot carries the outcome.
 */
export function describeStep(
  call: WSToolEvent | undefined,
  result?: WSToolEvent,
): StepDescription {
  const name = call?.name?.trim() ?? "";
  if (!name) {
    return { verb: "Working", verbPast: "Worked", glyph: "Wrench", meta: "", kind: "kv" };
  }
  const spec = specFor(name);
  const input = call?.input;
  const subject = spec.subject?.(input) ?? "";
  const tail = subject || spec.object;
  const verb = phrase(spec.present, tail);
  const verbPast = phrase(spec.past, tail);
  const meta = (spec.meta ? spec.meta(input) : defaultMeta(input)) || defaultMeta(input);

  let kind = spec.kind;
  if (result) {
    if (detectGated(result.output).gated) kind = "approval";
    else if (result.is_error || detectRefusal(result.output)) kind = "failure";
  } else if (call?.awaiting_approval) {
    kind = "approval";
  }

  return { verb, verbPast, glyph: spec.glyph, meta, kind };
}

/** The preview text a running code write streams into the detail inset. */
export function previewFor(call: WSToolEvent | undefined): string {
  return call ? extractToolPreview(call.input) : "";
}

// ─────────────────────────────────────────────────────────────────────────────
// coalesce
// ─────────────────────────────────────────────────────────────────────────────

/**
 * A message belongs in the ledger when it is churn: a tool call, a thinking
 * block, or the interim narration that streamed before a tool call. The boss's
 * own messages and Jarvis's final reply are not steps.
 */
function isStep(m: ChatMessage): boolean {
  if (m.role === "tool") return !!m.toolCall;
  if (m.role === "thinking") return true;
  return m.role === "assistant" && !!m.interim && !m.error;
}

const THINKING_SPEC: VerbSpec = {
  present: "Thinking",
  past: "Thought it through",
  object: "",
  plural: "thoughts",
  glyph: "Brain",
  kind: "thought",
  group: (n) => `Thought it through ${n} times`,
};

const NARRATION_SPEC: VerbSpec = {
  present: "Talking it through",
  past: "Talked it through",
  object: "",
  plural: "notes",
  glyph: "MessageSquare",
  kind: "note",
  group: (n) => `Talked it through ${n} times`,
};

type Described = {
  message: ChatMessage;
  spec: VerbSpec;
  desc: StepDescription;
  status: StepStatusInfo;
  /** Identity for folding: same stem + same counted noun. */
  key: string;
  solo: boolean;
};

function describeMessage(m: ChatMessage): Described {
  if (m.role === "thinking" || (m.role === "assistant" && m.interim)) {
    const spec = m.role === "thinking" ? THINKING_SPEC : NARRATION_SPEC;
    // Thinking and narration are PROSE, and the runtime brain writes prose in
    // markdown - so the preview showed the boss `**Planning agent
    // termination**` with the asterisks in it. Stripped here, on the prose
    // branch only: a tool's meta is a path or a command, where `*` is a glob
    // and must survive untouched.
    // A thinking row whose text is REDACTED (Claude Code sends the block and
    // empty deltas) has nothing to quote, so the meta carries the one real
    // fact available: how much it has reasoned so far. Without it the boss
    // reads "Thinking" over an empty box for two minutes, which looks exactly
    // like a hang - and he read it as one, more than once.
    let meta = oneLine(stripMarkdown(m.text ?? ""));
    if (!meta && m.role === "thinking" && (m.thinkingTokens ?? 0) > 0) {
      meta = `${formatCount(m.thinkingTokens as number)} tokens of reasoning so far`;
    }
    return {
      message: m,
      spec,
      desc: { verb: spec.present, verbPast: spec.past, glyph: spec.glyph, meta, kind: spec.kind },
      // A pending narration / thinking bubble is live work; a settled one is done.
      status: m.pending
        ? { status: "running", awaiting: false, gated: false, refused: false }
        : { status: "done", awaiting: false, gated: false, refused: false },
      key: `${spec.present} ${spec.plural}`,
      solo: false,
    };
  }
  const call = m.toolCall;
  const spec = specFor(call?.name ?? "");
  return {
    message: m,
    spec,
    desc: describeStep(call, m.toolResult),
    status: deriveStatus(m),
    key: `${spec.present} ${spec.plural}`,
    solo: !!spec.solo,
  };
}

function groupLabel(spec: VerbSpec, n: number, tense: "present" | "past"): string {
  const stem = tense === "present" ? spec.present : spec.past;
  if (spec.group) return spec.group(n, stem);
  return spec.plural ? `${stem} ${n} ${spec.plural}` : `${stem} ×${n}`;
}

/**
 * coalesce - the ledger. Consecutive calls doing the same KIND of work fold
 * into one counted row ("Ran 3 commands", "Read 5 files", "Edited 2 files").
 *
 * A run breaks on any of (MAJORDOMO §6):
 *   • a different verb        - obviously different work
 *   • a failure               - the boss must see WHICH call broke, not
 *                               "3 commands" with one silently red inside
 *   • an approval             - a decision he has to make can never be
 *                               counted away into a neighbouring row
 *   • a stop                  - a call nobody knows the outcome of must not
 *                               be absorbed into a row that reads "done"
 *   • a decision-card tool    - plan/skill proposals and team starts stand
 *                               alone (`solo`), enforced here, not in the UI
 *
 * Every underlying ChatMessage is preserved on `messages`, in order, so the
 * component can render the whole group's detail without going back to the
 * transcript.
 */
export function coalesce(messages: ChatMessage[]): ActivityItem[] {
  const steps = messages.filter(isStep).map(describeMessage);
  const out: ActivityItem[] = [];

  for (const step of steps) {
    const breaks =
      step.solo ||
      step.status.status === "error" ||
      step.status.status === "approval" ||
      step.status.status === "stopped";
    const prev = out.length > 0 ? out[out.length - 1] : undefined;
    const prevBreaks =
      !!prev &&
      (prev.status === "error" ||
        prev.status === "approval" ||
        prev.status === "stopped" ||
        prev.kind === "failure" ||
        DECISION_TOOLS.has(prev.messages[0]?.toolCall?.name?.toLowerCase() ?? ""));

    if (!breaks && !prevBreaks && prev && prev.key === step.key) {
      prev.messages.push(step.message);
      prev.count += 1;
      // A run is live while any member of it is.
      if (step.status.status === "running") prev.status = "running";
      prev.startedAt = Math.min(prev.startedAt, step.message.createdAt);
      const ended = endOf(step.message);
      prev.endedAt = prev.endedAt == null ? ended : Math.max(prev.endedAt, ended ?? prev.endedAt);
      prev.verb = groupLabel(prev0Spec(prev), prev.count, "present");
      prev.verbPast = groupLabel(prev0Spec(prev), prev.count, "past");
      prev.label = prev.status === "running" ? prev.verb : prev.verbPast;
      // A counted row's meta is the count's subjects, not the first one's.
      prev.meta = groupMeta(prev);
      continue;
    }

    out.push({
      id: step.message.id,
      key: step.key,
      kind: step.desc.kind,
      glyph: step.desc.glyph,
      verb: step.desc.verb,
      verbPast: step.desc.verbPast,
      label: step.status.status === "running" ? step.desc.verb : step.desc.verbPast,
      meta: step.desc.meta,
      status: step.status.status,
      awaiting: step.status.awaiting,
      gated: step.status.gated,
      refused: step.status.refused,
      contractId: step.status.contractId,
      count: 1,
      messages: [step.message],
      startedAt: step.message.createdAt,
      endedAt: endOf(step.message),
      weight: step.spec.weight ?? KIND_WEIGHT[step.desc.kind],
    });
  }
  return out;
}

/** The spec behind an already-built item, for relabelling on fold. */
function prev0Spec(item: ActivityItem): VerbSpec {
  const first = item.messages[0];
  if (first.role === "thinking") return THINKING_SPEC;
  if (first.role === "assistant") return NARRATION_SPEC;
  return specFor(first.toolCall?.name ?? "");
}

/**
 * A folded row's meta names EVERY member, not just the first. "Ran 3 commands"
 * whose meta showed only the first command would be a quiet lie about the other
 * two; the row is the only place the boss sees them without tapping.
 */
function groupMeta(item: ActivityItem): string {
  const spec = prev0Spec(item);
  const parts: string[] = [];
  for (const m of item.messages) {
    const input = m.toolCall?.input;
    const subject = spec.subject?.(input) ?? "";
    const piece =
      subject ||
      (m.role === "tool"
        ? (spec.meta ? spec.meta(input) : defaultMeta(input))
        : oneLine(m.text ?? ""));
    if (piece && !parts.includes(piece)) parts.push(piece);
  }
  return parts.length > 0 ? clamp(parts.join(", "), MAX_META) : item.meta;
}

function endOf(m: ChatMessage): number | undefined {
  const iso = m.toolResult?.ended_at;
  if (iso) {
    const t = new Date(iso).getTime();
    if (!Number.isNaN(t)) return t;
  }
  if (m.endedAt) return m.endedAt;
  return undefined;
}

// ─────────────────────────────────────────────────────────────────────────────
// headlineFor / summaryFor
// ─────────────────────────────────────────────────────────────────────────────

const MAX_HEADLINE = 140;

/** The first sentence of a block of prose, capped. */
export function firstSentence(text: string, max = MAX_HEADLINE): string {
  const t = (text ?? "").trim();
  if (!t) return "";
  const line = t.split("\n").find((l) => l.trim().length > 0) ?? "";
  const m = line.match(/^.*?[.!?](?=\s|$)/);
  return clamp((m ? m[0] : line).replace(/\s+/g, " "), max);
}

/**
 * headlineFor - the one line at the top of the ledger while a turn runs.
 *
 * §6: the first sentence of the turn's interim narration, else the plan's
 * current step, else "Working". `narration` carries whichever of those two the
 * caller has (the interim bubble's text, or the plan dock's current step).
 * When the caller has neither, we still do better than "Working": the ledger
 * itself knows what is happening right now, so a live row's present-tense verb
 * ("Editing migrate.go") becomes the headline. "Working" is the true last
 * resort, when there is nothing at all to say.
 */
export function headlineFor(items: ActivityItem[], narration?: string): string {
  const fromCaller = firstSentence(narration ?? "");
  if (fromCaller) return fromCaller;

  // The turn's own narration, newest first: an interim assistant note.
  for (let i = items.length - 1; i >= 0; i--) {
    const item = items[i];
    if (item.kind !== "note") continue;
    if (item.messages[0]?.role !== "assistant") continue;
    const s = firstSentence(item.messages[0].text ?? "");
    if (s) return s;
  }

  const live = items.find((i) => i.status === "running");
  if (live) return live.verb;

  const waiting = items.find((i) => i.status === "approval");
  if (waiting) return waiting.verb;

  return "Working";
}

/** "under a second" / "42s" / "2m 14s". */
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "under a second";
  if (ms < 1000) return "under a second";
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  return `${m}m ${s % 60}s`;
}

/**
 * Lowercase the leading verb so it reads inside a sentence ("Ran 3 commands"
 * → "ran 3 commands"), but never touch a brand the vocabulary put first
 * ("GitHub · search repositories" must not become "gitHub · …").
 */
function lowerFirst(s: string): string {
  if (!s) return s;
  const first = s.split(" ", 1)[0];
  if (first === first.toUpperCase()) return s; // "X", "PR"
  if (/[A-Z]/.test(first.slice(1))) return s; // "GitHub", "GmailFetch"
  return s.charAt(0).toLowerCase() + s.slice(1);
}

/**
 * summaryFor - the settled row: "Worked for 2m 14s · revised the plan, ran 3
 * commands, edited 2 files".
 *
 * Three most TELLING verbs, not the first three: a turn that read nine files
 * and shipped one commit is about the commit. Weight ranks by kind (a commit
 * outranks a diff outranks a search outranks a read); ties go to the busier
 * row. The three chosen are then put back in the order they happened so the
 * sentence reads like a story.
 *
 * `now` exists so the function stays pure and testable; a live turn's last row
 * has no end stamp yet.
 */
export function summaryFor(items: ActivityItem[], now?: number): string {
  if (items.length === 0) return "Worked for a moment";
  const at = now ?? Date.now();
  const start = Math.min(...items.map((i) => i.startedAt));
  const end = Math.max(...items.map((i) => i.endedAt ?? i.startedAt));
  const duration = formatDuration(Math.max(0, (end || at) - start));

  const ranked = items
    .map((item, index) => ({ item, index }))
    .filter(({ item }) => item.kind !== "thought")
    .sort((a, b) => {
      if (b.item.weight !== a.item.weight) return b.item.weight - a.item.weight;
      if (b.item.count !== a.item.count) return b.item.count - a.item.count;
      return a.index - b.index;
    })
    .slice(0, 3)
    .sort((a, b) => a.index - b.index)
    .map(({ item }) => lowerFirst(item.verbPast));

  if (ranked.length === 0) return `Worked for ${duration}`;
  return `Worked for ${duration} · ${ranked.join(", ")}`;
}

/** formatCount renders a running total the way a person reads one: 950, 1.2k. */
export function formatCount(n: number): string {
  if (n < 1000) return String(n);
  // Round on the tenth explicitly. `(1450/1000).toFixed(1)` is "1.4", because
  // 1.45 is not 1.45 in binary floating point, and a counter that reads low is
  // a counter he cannot trust.
  return `${(Math.round(n / 100) / 10).toFixed(1).replace(/\.0$/, "")}k`;
}
