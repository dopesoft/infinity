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
  body?: string;
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

// Google Calendar invitation response — the value the boss can set
// (accepted/declined/tentative) plus needsAction for "RSVP pending".
// Null = the boss is the organizer (no self-response slot).
export type EventResponseStatus =
  | "accepted"
  | "declined"
  | "tentative"
  | "needsAction";

export type EventAttendee = {
  email: string;
  displayName?: string;
  responseStatus?: EventResponseStatus;
  self?: boolean;
  organizer?: boolean;
  optional?: boolean;
};

export type EventOrganizer = {
  email?: string;
  displayName?: string;
  self?: boolean;
};

export type EventConferenceEntryPoint = {
  type: string; // "video" | "phone" | "sip" | "more"
  uri: string;
  label?: string;
};

export type EventConference = {
  solutionName?: string;
  iconUri?: string;
  entryPoints?: EventConferenceEntryPoint[];
};

export type CalendarEvent = {
  id: string;
  title: string;
  startsAt: string; // ISO
  endsAt?: string;
  allDay?: boolean;
  location?: string;
  description?: string;
  attendees?: EventAttendee[];
  organizer?: EventOrganizer;
  conference?: EventConference;
  recurrence?: string[];
  htmlLink?: string;
  hangoutLink?: string;
  responseStatus?: EventResponseStatus;
  accountId?: string;
  accountLabel?: string;
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
  source: FollowUpSource | string; // free-string after surface_item merge
  account?: string; // "khaya@malabieindustries.com"
  from: string; // "Adam Foster" or "#infra"
  subject?: string;
  preview: string;
  receivedAt: string;
  unread?: boolean;
  // full content surfaced in ObjectViewer (plain text; connector-poll rows
  // carry this, surface rows fetch the full email lazily on open).
  body?: string;
  // Agent triage summary (urgency + what it's about). Rendered in the
  // ObjectViewer "Context" pane, ABOVE the email itself. Absent on raw
  // connector-poll rows that haven't been triaged.
  summary?: string;
  // Rendered HTML email body. NOT in the dashboard payload - fetched lazily
  // via GET /api/followups/message when the viewer opens. Typed here so the
  // fetch result is shaped against the same model.
  html?: string;
  threadUrl?: string;
  // optional pre-drafted reply from the agent
  draft?: string;
  // The exact reply text the boss sent (persisted server-side the moment Send
  // is tapped). When present, the viewer renders a read-only "Response" section
  // instead of the editable draft box; it survives refresh and device switch.
  sentReply?: string;
  // Which table the row came from. "followup" → mem_followups (connector
  // poll), "surface" → mem_surface_items with surface='followups'. The
  // dismiss endpoint reads this to update the right table.
  origin?: "followup" | "surface";
  // Generic chip carrier. Studio renders chips for known keys (intent,
  // mode, classification) when present. The agent can attach anything;
  // unknown keys just don't render as chips but stay visible in the
  // ObjectViewer.
  metadata?: Record<string, unknown>;
  // Boss-tappable actions on this email (Draft reply / Archive / Snooze, …).
  // Surface-origin rows carry the surface item's actions; tapping POSTs to
  // /api/surface/action keyed by `id` (which is the surface item id), running
  // the action's server-side intent as an agent turn. intent never reaches
  // the client — only id/label/style do.
  actions?: SurfaceAction[];
};

// ── Surface items (the generic dashboard surface contract) ───────────────────
// Mirror of core/internal/surface.Item. ANY producer - a skill recipe, a
// connector poll, a cron, the agent mid-conversation - writes items through
// the `surface_item` tool; the dashboard merges every surface into the one
// "Surfaced by Jarvis" card (SurfacedCard), rendering each via SurfaceRow. A
// new surface the agent invents appears with zero new frontend code.
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
  // Boss-tappable buttons (the surface return-path). Tapping one POSTs
  // {id, action_id} to /api/surface/action, which runs the action's
  // server-side `intent` as an autonomous agent turn against this item. The
  // `intent` never reaches the client - only id/label/style do.
  actions?: SurfaceAction[];
  status: "open" | "snoozed" | "done" | "dismissed";
  createdAt: string;
};

export type SurfaceAction = {
  id: string;
  label: string;
  style?: "primary" | "default" | "danger";
};

// ── Agent Work Board (Kanban) ────────────────────────────────────────────────
export type WorkColumn = "queued" | "running" | "awaiting" | "done";

export type WorkItemKind =
  | "cron_run"
  | "voyager_opt"
  | "sentinel"
  | "skill_run"
  | "workflow"
  | "plan"
  | "mandate"
  | "trust"
  | "code_proposal"
  | "curiosity"
  | "memory_op"
  | "reflection";

// One acceptance criterion of a mandate WorkItem — carried inline on the card
// so the ObjectViewer shows the full definition-of-done with no second fetch.
export type MandateCriterion = {
  id: string;
  text: string;
  status: "pending" | "pass" | "fail";
  evidence?: string;
};

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
  // the run's narrative ("what it did / how it went / outcome") from
  // mem_runs.result_summary - populated for finished cron runs so the Done
  // card and ObjectViewer read as a report, not a bare "ok".
  summary?: string;
  // engine = the subsystem behind the item ("Voyager", "GEPA", "Schedule",
  // "Sentinel", "Skill", …) and ref = the raw technical id. Both stay OFF the
  // card title (readable plain English) and show in the detail view.
  engine?: string;
  ref?: string;
  // the agent turn behind a running/awaiting item, when known — used by the
  // Stop button to cancel the live turn and close the right runs.
  sessionId?: string;
  scheduledFor?: string;
  startedAt?: string;
  finishedAt?: string;
  durationMs?: number;
  // links to existing routes for "see in /trust" / "see in /cron" etc.
  detailHref?: string;
  // populated only for kind === "workflow" - the run's step state-machine.
  workflowSteps?: WorkflowStep[];
  // populated only for kind === "plan" - the durable plan's ordered steps,
  // carried inline so the Kanban card opens the full step timeline in
  // ObjectViewer with no second fetch. doneCount/totalCount drive the "4/7"
  // progress bar on the running card.
  planSteps?: PlanStep[];
  doneCount?: number;
  totalCount?: number;
  // populated only for kind === "mandate" - the binary acceptance criteria with
  // pass/fail + evidence, carried inline so the card opens the full
  // definition-of-done in ObjectViewer. doneCount/totalCount drive the same
  // progress bar as a plan.
  mandateCriteria?: MandateCriterion[];
  // verification verdict for a high-stakes mandate (overall/confidence/auditor/
  // notes), shown in ObjectViewer. Empty until mandate_verify has run.
  crosscheck?: {
    overall?: string;
    confidence?: number;
    notes?: string;
    auditor?: string;
  };
  // the skill(s) this item runs — for plans, what the agent actually invoked;
  // for crons, what the job is set to invoke. Shown as chips under the headline.
  skills?: string[];
  // plain-English "what this job does": a cron's instruction or a plan's goal.
  // Lets a queued/running card explain itself without a trip to /cron.
  instruction?: string;
};

// ── Plans (the durable, verifiable plan substrate - "the Cortex") ────────────
// Mirror of core/internal/dashboard.PlanStep. The agent lays out a plan with
// plan_create and works it step by step; a plan surfaces on the Agent Work
// board as a WorkItem of kind "plan" (steps ride inline on WorkItem.planSteps),
// and the PlanTimeline component renders them inside ObjectViewer.
export type PlanStatus =
  | "active"
  | "paused"
  | "completed"
  | "failed"
  | "cancelled";

export type PlanStepStatus =
  | "pending"
  | "in_progress"
  | "blocked"
  | "done"
  | "failed"
  | "skipped";

export type PlanStep = {
  id: string;
  idx: number;
  title: string;
  detail?: string;
  status: PlanStepStatus;
  isCheckpoint: boolean;
  verifyRequired: boolean;
  // { verdict: "pass"|"fail", evidence, method, at }
  verifyResult?: Record<string, unknown>;
  resultSummary?: string;
  // mem_runs row id for this step's execution - drives the live spinner.
  runId?: string;
  startedAt?: string;
  endedAt?: string;
};

// A plan surfaces on the Agent Work board as a WorkItem of kind "plan" (steps
// inline on the work item). The chat dock renders the SAME plan via the
// session-scoped GET /api/plans/active, typed below - one substrate, two views.
export type Plan = {
  id: string;
  title: string;
  goal?: string;
  status: PlanStatus;
  currentStep: number;
  doneCount: number;
  totalCount: number;
  steps: PlanStep[];
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

// ── Artifact (Made by Jarvis) ────────────────────────────────────────────────
// A generated deliverable Jarvis shipped — an app/project, doc, dataset, etc.
// Sourced from mem_artifacts (the canonical "things I've made" store that
// project_create / document_create / image_generate already populate) and
// surfaced on the dashboard's Saved card under the "Made by Jarvis" segment.
// The storage fields are what the ObjectViewer routes on to reopen it live.
export type Artifact = {
  id: string;
  kind: string; // project | document | dataset | other (curated for the dashboard)
  name: string;
  description?: string;
  virtualPath: string; // boss-facing Library path
  storageKind: "filesystem" | "object_store" | "postgres" | "inline";
  storagePath?: string; // real FS path on a bridge OR object-store URL
  storageMime?: string;
  bridge?: "mac" | "cloud";
  githubUrl?: string;
  sourceTool?: string; // 'project_create' | 'document_create' | …
  createdAt: string;
  // Document-preview fields (mirror the canvas DocMeta) so a generated doc
  // opens straight into a fully-rendered canvas tab, not a download card.
  format?: string;
  bytes?: number;
  pdfPath?: string;
  htmlPath?: string;
  markdown?: string;
  // Origin chat: reopen the doc in its own session (with history) when it still
  // exists, or seed a fresh one with the doc dropped in when it was deleted.
  sourceSessionId?: string;
  sessionAlive?: boolean;
};

// ── Activity feed ────────────────────────────────────────────────────────────
export type ActivityKind =
  | "scheduled"
  | "completed"
  | "alert"
  | "memory"
  | "reflection"
  | "system"; // operational agent notes folded in from the old System card

export type ActivityEvent = {
  id: string;
  kind: ActivityKind;
  title: string;
  detail?: string;
  at: string;
  future?: boolean;
  // Populated only for rows the boss can clear from their detail view
  // (folded system notes). Maps back to the dismiss endpoint's table.
  dismiss?: { origin: "surface"; id: string };
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
  | { kind: "artifact"; data: Artifact }
  | { kind: "activity"; data: ActivityEvent };
