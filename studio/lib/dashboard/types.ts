/* Dashboard domain types.
 *
 * These types describe the artifacts surfaced on the Dashboard tab - they
 * deliberately mirror the shapes that will exist in Postgres (mem_tasks,
 * mem_pursuits, mem_followups, mem_saved, etc.) so that switching from the
 * mock layer to live data is a near-mechanical swap of the loader.
 *
 * The ObjectViewer uses `DashboardItem` as a discriminated union - every
 * kind that can be opened from a dashboard card lands here.
 */

// ── Pursuits (habits + goals + objectives, one unified surface) ─────────────
export type PursuitCadence =
  | "daily"
  | "weekly"
  | "goal" // dated long-term objective (book count, ship date, etc.)
  | "quarterly";

export type Pursuit = {
  id: string;
  title: string;
  cadence: PursuitCadence;
  // for habits: today's completion
  doneToday?: boolean;
  doneAt?: string;
  streakDays?: number;
  // for goals: progress toward target
  progress?: { current: number; target: number; unit?: string };
  dueAt?: string; // ISO date for goals/quarterly
  status?: "on_track" | "slow" | "at_risk" | "ahead";
  createdAt?: string; // ISO timestamp the pursuit was created
};

// ── Todos ────────────────────────────────────────────────────────────────────
export type Todo = {
  id: string;
  title: string;
  done: boolean;
  dueAt?: string; // ISO
  priority?: "low" | "med" | "high";
  source: "manual" | "agent" | "email" | "cron";
};

// ── Calendar / Upcoming ──────────────────────────────────────────────────────
export type CalendarEventClass =
  | "meeting"
  | "concert"
  | "flight"
  | "dinner"
  | "appointment"
  | "travel"
  | "social"
  | "personal"
  | "other";

export type PrepItem = {
  id: string;
  label: string; // "Book parking near MSG"
  done: boolean;
  // optional agent rationale shown in ObjectViewer
  rationale?: string;
};

export type CalendarEvent = {
  id: string;
  title: string;
  startsAt: string; // ISO
  endsAt?: string;
  location?: string;
  attendees?: string[];
  classification: CalendarEventClass;
  prep: PrepItem[]; // can be empty for routine events
};

// ── Reflection-of-the-day ────────────────────────────────────────────────────
export type Reflection = {
  id: string;
  title: string;
  body: string;
  capturedAt: string;
  evidenceCount: number;
};

// ── Approvals ────────────────────────────────────────────────────────────────
export type ApprovalKind =
  | "trust_bash"
  | "trust_edit"
  | "trust_write"
  | "code_proposal"
  | "curiosity";

export type Approval = {
  id: string;
  kind: ApprovalKind;
  title: string;
  subtitle?: string;
  createdAt: string;
  // populated for trust requests
  toolCall?: { name: string; args: Record<string, unknown> };
  // populated for code proposals
  diff?: string;
  filePath?: string;
  riskLevel?: "low" | "medium" | "high" | "critical";
  rationale?: string;
  // populated for curiosity
  question?: string;
  context?: string;
};

// ── Follow-ups (humans waiting on you, surfaced by the agent) ────────────────
export type FollowUpSource = "gmail" | "slack" | "imessage" | "linear" | "other";

export type FollowUp = {
  id: string;
  source: FollowUpSource;
  account?: string; // "khaya@malabieindustries.com"
  from: string; // "Adam Foster" or "#infra"
  subject?: string;
  preview: string;
  receivedAt: string;
  unread?: boolean;
  // full content surfaced in ObjectViewer
  body?: string;
  threadUrl?: string;
  // optional pre-drafted reply from the agent
  draft?: string;
};

// ── Surface items (the generic dashboard surface contract) ───────────────────
// Mirror of core/internal/surface.Item. ANY producer - a skill recipe, a
// connector poll, a cron, the agent mid-conversation - writes items through
// the `surface_item` tool; the dashboard groups them by `surface` and renders
// each group with one generic SurfaceCard. A new surface the agent invents
// appears with zero new frontend code.
export type SurfaceItem = {
  id: string;
  surface: string; // dashboard region: "followups" | "alerts" | "digest" | …
  kind: string; // semantic type for the icon: "email" | "alert" | "metric" | …
  source: string; // skill name / connector slug / cron name / "agent"
  externalId?: string;
  title: string;
  subtitle?: string;
  body?: string;
  url?: string;
  importance?: number; // 0-100, undefined = unranked
  importanceReason?: string;
  metadata: Record<string, unknown>;
  status: "open" | "snoozed" | "done" | "dismissed";
  createdAt: string;
};

// ── Agent Work Board (Kanban) ────────────────────────────────────────────────
export type WorkColumn = "queued" | "running" | "awaiting" | "done";

export type WorkItemKind =
  | "cron_run"
  | "voyager_opt"
  | "sentinel"
  | "skill_run"
  | "workflow"
  | "trust"
  | "code_proposal"
  | "curiosity"
  | "memory_op"
  | "reflection";

// One step of a workflow run's state-machine - carried inline on a
// workflow WorkItem so tapping the Kanban card opens the drawer with the
// full workflow, no second fetch.
export type WorkflowStep = {
  index: number;
  name: string;
  kind: "tool" | "skill" | "agent" | "checkpoint";
  status: "pending" | "running" | "done" | "failed" | "skipped" | "awaiting";
  output?: string;
  error?: string;
};

export type WorkItem = {
  id: string;
  kind: WorkItemKind;
  title: string;
  subtitle?: string;
  column: WorkColumn;
  scheduledFor?: string;
  startedAt?: string;
  finishedAt?: string;
  durationMs?: number;
  // links to existing routes for "see in /trust" / "see in /cron" etc.
  detailHref?: string;
  // populated only for kind === "workflow" - the run's step state-machine.
  workflowSteps?: WorkflowStep[];
};

// ── Saved (articles, links, notes, quotes) ───────────────────────────────────
export type SavedKind = "article" | "link" | "note" | "quote";

export type Saved = {
  id: string;
  kind: SavedKind;
  title: string;
  body?: string; // full text for articles/notes/quotes
  url?: string;
  source?: string; // domain or author
  readingMinutes?: number;
  savedAt: string;
};

// ── Activity feed ────────────────────────────────────────────────────────────
export type ActivityKind =
  | "scheduled"
  | "completed"
  | "alert"
  | "memory"
  | "reflection";

export type ActivityEvent = {
  id: string;
  kind: ActivityKind;
  title: string;
  detail?: string;
  at: string;
  future?: boolean;
};

// ── Memory telemetry footer ──────────────────────────────────────────────────
export type MemoryStats = {
  newToday: number;
  promotedToday: number;
  procedural: number;
  streakDays: number;
};

// ── ObjectViewer routing ─────────────────────────────────────────────────────
// Discriminated union of every item type that can be opened in the
// ObjectViewer modal/drawer. The viewer renders a kind-specific body.
export type DashboardItem =
  | { kind: "pursuit"; data: Pursuit }
  | { kind: "todo"; data: Todo }
  | { kind: "event"; data: CalendarEvent }
  | { kind: "reflection"; data: Reflection }
  | { kind: "approval"; data: Approval }
  | { kind: "followup"; data: FollowUp }
  | { kind: "surface"; data: SurfaceItem }
  | { kind: "work"; data: WorkItem }
  | { kind: "saved"; data: Saved }
  | { kind: "activity"; data: ActivityEvent };
