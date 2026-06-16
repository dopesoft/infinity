import { getAccessToken } from "@/lib/auth/session";
import type { Plan } from "@/lib/dashboard/types";

export type CoreStatus = {
  version: string;
  provider: string;
  model: string;
  tools: string[];
};

export type ToolDescriptor = {
  name: string;
  description: string;
  schema: Record<string, unknown>;
};

export type MCPStatus = {
  name: string;
  connected: boolean;
  tools: string[];
  error?: string;
  tested: string;
};

export type ChatSettings = {
  agent_teams: "off" | "ask" | "auto";
  team_aggressiveness: "conservative" | "balanced" | "full_tilt";
  show_team_activity: "off" | "compact" | "detailed";
  default_team_card_state: "collapsed" | "expanded";
  max_agents_per_team: number;
  max_parallel_teams: number;
  max_runtime_seconds: number;
  max_team_tokens: number;
  max_tool_calls: number;
  allow_artifact_agents: boolean;
  allow_code_agents: boolean;
  allow_connector_agents: boolean;
  require_action_approval: boolean;
  model_policy: "same_as_chat" | string;
  show_token_usage: boolean;
  show_worker_summaries: boolean;
  show_artifacts: boolean;
};

export type SessionDTO = {
  id: string;
  name?: string;
  started_at: string;
  ended_at?: string;
  project?: string;
  project_path?: string;
  project_template?: string;
  dev_port?: number;
  last_run_at?: string;
  message_count: number;
  live?: boolean;
};

export async function renameSession(id: string, name: string): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/sessions/${encodeURIComponent(id)}/rename`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    return res.ok;
  } catch {
    return false;
  }
}

// postSurfaceAction fires a surfaced-item action button (the surface
// return-path). The server looks up the item + action, then seeds an
// explicit action turn tracked in mem_runs (kind="surface.action",
// targetId=item id) - watch it with useRuns/RunIndicator. For send_reply,
// draftText carries the boss-edited reply body.
export type SurfaceActionResult = {
  ok: boolean;
  kind?: string;
  target_id?: string;
  session_id?: string;
};

export async function postSurfaceAction(
  id: string,
  actionId: string,
  opts: { draftText?: string } = {},
): Promise<SurfaceActionResult | null> {
  try {
    const res = await authedFetch(`/api/surface/action`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id, action_id: actionId, draft_text: opts.draftText }),
    });
    if (!res.ok) return null;
    return (await res.json()) as SurfaceActionResult;
  } catch {
    return null;
  }
}

// ── Agent Work: cancel/stop a running or awaiting item ───────────────────────
// Kills a plan / cron run / agent turn from the Agent Work board: aborts the
// in-flight turn (so the agent actually stops), cancels the owning plan, and
// closes its lingering runs so the card clears. Works for running AND awaiting
// items. `id` is the WorkItem id with its "plan-"/"run-" prefix stripped.
export async function cancelWork(input: {
  kind: string;
  id: string;
  sessionId?: string;
}): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/work/cancel`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        kind: input.kind,
        id: input.id,
        session_id: input.sessionId ?? "",
      }),
    });
    return res.ok;
  } catch {
    return false;
  }
}

// ── Plans (the Cortex - mem_plans) ───────────────────────────────────────────
// The active/paused plan for a chat session, powering the pinned dock so it
// shows the same plan the dashboard Agent Work board does. Read-only from
// Studio; the agent owns writes via its plan_* / todo_write tools.
export async function fetchActivePlan(sessionId: string, signal?: AbortSignal): Promise<Plan | null> {
  if (!sessionId) return null;
  try {
    const res = await authedFetch(`/api/plans/active?session_id=${encodeURIComponent(sessionId)}`, { signal });
    if (!res.ok) return null;
    const data = (await res.json()) as { plan?: Plan | null };
    return data.plan ?? null;
  } catch {
    return null;
  }
}

// ── Todos (mem_tasks) ────────────────────────────────────────────────────────
// Manual todo writes from the dashboard. Both endpoints land in the SAME
// mem_tasks table Jarvis reads/writes via his task_* tools, so a todo the
// boss types is immediately visible to the agent. The dashboard's realtime
// subscription on mem_tasks repaints the card once the row lands - no manual
// refetch needed on the happy path.

export type CreateTodoInput = {
  title: string;
  body?: string;
  priority?: "low" | "med" | "high";
  /** RFC3339 timestamp or YYYY-MM-DD. Omit for no due date. */
  due_at?: string;
};

export async function createTodo(input: CreateTodoInput): Promise<{ id: string } | null> {
  try {
    const res = await authedFetch(`/api/tasks/create`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
    if (!res.ok) return null;
    const data = (await res.json()) as { ok?: boolean; id?: string };
    return data.id ? { id: data.id } : null;
  } catch {
    return null;
  }
}

export type UpdateTodoInput = {
  id: string;
  title?: string;
  body?: string;
  priority?: "low" | "med" | "high";
  due_at?: string;
  status?: "open" | "done" | "dropped";
};

export async function updateTodo(input: UpdateTodoInput): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/tasks/update`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
    return res.ok;
  } catch {
    return false;
  }
}

export async function setSessionProject(
  id: string,
  body: { project_path?: string; project_template?: string; dev_port?: number; mark_run?: boolean },
): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/sessions/${encodeURIComponent(id)}/project`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return res.ok;
  } catch {
    return false;
  }
}

export type ProjectStatus = "idle" | "booting" | "running" | "crashed";

export type ProjectDTO = {
  project_path: string;
  template?: string;
  dev_port?: number;
  status: ProjectStatus;
  started_at?: string;
  last_ready_at?: string;
  last_error?: string;
  last_used?: string;
  // "cloud" when the preview is served by the cloud workspace bridge - Studio
  // then points the preview iframe at Core's /api/canvas/preview proxy rather
  // than the Mac dev-server tunnel.
  bridge?: string;
};

export async function canvasProjectStart(body: {
  project_path: string;
  template?: string;
  activate?: boolean;
}): Promise<ProjectDTO | null> {
  try {
    const res = await authedFetch(`/api/canvas/project/start`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) return null;
    return (await res.json()) as ProjectDTO;
  } catch {
    return null;
  }
}

export async function canvasProjectActivate(body: {
  project_path: string;
  template?: string;
  session_id?: string;
}): Promise<ProjectDTO | null> {
  try {
    const res = await authedFetch(`/api/canvas/project/active`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) return null;
    return (await res.json()) as ProjectDTO;
  } catch {
    return null;
  }
}

export async function canvasProjectStatus(projectPath?: string): Promise<ProjectDTO | { projects: ProjectDTO[] } | null> {
  try {
    const qs = projectPath ? `?project_path=${encodeURIComponent(projectPath)}` : "";
    const res = await authedFetch(`/api/canvas/project/status${qs}`);
    if (!res.ok) return null;
    return (await res.json()) as ProjectDTO | { projects: ProjectDTO[] };
  } catch {
    return null;
  }
}

export type MemoryCounts = {
  observations: number;
  memories: number;
  graph_nodes: number;
  graph_edges: number;
  stale: number;
  sessions: number;
};

export type SearchResult = {
  observation_id: string;
  session_id: string;
  hook_name: string;
  raw_text: string;
  created_at: string;
  score: number;
  streams: string[];
};

export type ObservationDTO = {
  id: string;
  session_id: string;
  hook_name: string;
  raw_text: string;
  importance: number;
  created_at: string;
};

export type MemoryDTO = {
  id: string;
  title: string;
  content: string;
  tier: "working" | "episodic" | "semantic" | "procedural";
  version: number;
  superseded_by?: string | null;
  status: string;
  strength: number;
  importance: number;
  project?: string;
  forget_after?: string | null;
  created_at: string;
  updated_at: string;
  last_accessed_at: string;
  // True when this memory was observed under an older commit than what's
  // running now — the Memory tab badges it "pre-deploy" so stale failure
  // narratives stop reading as current.
  predates_deploy?: boolean;
};

export type ProvenanceSource = {
  observation_id: string;
  session_id: string;
  excerpt: string;
  created_at: string;
  confidence: number;
};

export type ProvenanceChain = {
  memory: MemoryDTO;
  sources: ProvenanceSource[];
  confidence: number;
};

export type ReflectionDTO = {
  id: string;
  session_id?: string;
  kind: string;
  critique: string;
  lessons: { text: string; confidence: number }[];
  quality_score: number;
  importance: number;
  created_at: string;
};

export type ReflectionChainDTO = {
  id: string;
  topic: string;
  lesson: string;
  source_reflection_ids: string[];
  occurrences: number;
  confidence: number;
  first_seen_at: string;
  last_seen_at: string;
  updated_at: string;
};

export type PredictionDTO = {
  id: string;
  session_id?: string;
  tool_call_id: string;
  tool_name: string;
  expected: string;
  actual?: string;
  matched: boolean;
  surprise_score: number;
  created_at: string;
  resolved_at?: string;
};

export function coreBaseURL(): string {
  if (typeof window === "undefined") return "";
  const explicit = process.env.NEXT_PUBLIC_CORE_URL;
  if (explicit) return explicit.replace(/\/$/, "");
  return "";
}

// authedFetch wraps fetch() so every Core call carries the latest Supabase
// JWT. On a 401 (token raced an inflight refresh, server clock skew, etc.)
// retry once with a freshly-fetched token before surfacing the error.
export async function authedFetch(path: string, init: RequestInit = {}): Promise<Response> {
  async function send(): Promise<Response> {
    const token = await getAccessToken();
    const headers = new Headers(init.headers);
    if (token) headers.set("Authorization", `Bearer ${token}`);
    return fetch(`${coreBaseURL()}${path}`, { ...init, headers });
  }
  const first = await send();
  if (first.status !== 401) return first;
  return send();
}

// closeBrowserSession tears down a live cloud-browser session (Studio's
// "Stop" button on the Browser tab). The agent normally closes its own
// session via browser_close; this is the boss's manual kill switch.
export async function closeBrowserSession(id: string): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/browser/session/${encodeURIComponent(id)}/close`, {
      method: "POST",
    });
    return res.ok;
  } catch {
    return false;
  }
}

// navigateBrowserSession points a live cloud-browser session at a new URL —
// the boss typing into the live-browser toolbar's URL bar.
export async function navigateBrowserSession(id: string, url: string): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/browser/session/${encodeURIComponent(id)}/navigate`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ url }),
    });
    return res.ok;
  } catch {
    return false;
  }
}

// BrowserInputEvent mirrors core/internal/browser.InputEvent — one raw human
// interaction forwarded to the live session when the boss takes over the
// screencast by hand (click / type / scroll).
export type BrowserInputEvent = {
  type: "click" | "move" | "scroll" | "text" | "key" | "resize";
  x?: number;
  y?: number;
  button?: "left" | "right" | "middle";
  delta_x?: number;
  delta_y?: number;
  text?: string;
  key?: string;
  width?: number;
  height?: number;
};

// sendBrowserInput forwards one manual interaction to the live session. Fire-
// and-forget for high-frequency events (the screencast reflects the result).
export async function sendBrowserInput(id: string, ev: BrowserInputEvent): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/browser/session/${encodeURIComponent(id)}/input`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(ev),
    });
    return res.ok;
  } catch {
    return false;
  }
}

// activateExtension drives a cli extension's install + auth flow on demand so
// the device-login URL is captured without waiting for a Core reboot. Returns
// quickly (202) — the result lands via the mem_extensions realtime channel.
export async function activateExtension(name: string): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/extensions/${encodeURIComponent(name)}/activate`, {
      method: "POST",
    });
    return res.ok;
  } catch {
    return false;
  }
}

// fetchWorkspaceBlob pulls a file's raw bytes from the CLOUD workspace via
// the cloud-direct proxy (works on any device, independent of the session
// bridge). Returns a Blob so binaries never round-trip as text. Used for
// generated-document download + inline PDF preview.
export async function fetchWorkspaceBlob(path: string): Promise<Blob | null> {
  try {
    const res = await authedFetch(`/api/workspace/download?path=${encodeURIComponent(path)}`);
    if (!res.ok) return null;
    return await res.blob();
  } catch {
    return null;
  }
}

// downloadWorkspaceFile triggers a browser "save as" for a workspace file.
export async function downloadWorkspaceFile(path: string, filename: string): Promise<boolean> {
  const blob = await fetchWorkspaceBlob(path);
  if (!blob) return false;
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
  return true;
}

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T | null> {
  try {
    const res = await authedFetch(path, { signal });
    if (!res.ok) return null;
    return (await res.json()) as T;
  } catch {
    return null;
  }
}

export const fetchCoreStatus = (signal?: AbortSignal) =>
  getJSON<CoreStatus>("/api/status", signal);

export const fetchChatSettings = (signal?: AbortSignal) =>
  getJSON<ChatSettings>("/api/settings/chat", signal);

export async function saveChatSettings(settings: ChatSettings): Promise<ChatSettings | null> {
  try {
    const res = await authedFetch("/api/settings/chat", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(settings),
    });
    if (!res.ok) return null;
    return (await res.json()) as ChatSettings;
  } catch {
    return null;
  }
}

// Server-tracked long-action progress. Every long server action (cron,
// skill, heartbeat, voyager.optimize, gym.extract, sentinel, …) books
// a mem_runs row via runs.Track on the Go side and pushes updates over
// the supabase_realtime publication. Surfaces in Studio via useRuns().
// See CLAUDE.md → "Server-tracked progress".
export type RunDTO = {
  id: string;
  kind: string;            // 'cron' | 'skill' | 'heartbeat' | 'voyager.optimize' | ...
  target_id: string;       // row id of the thing running (cron uuid, skill name, ...)
  label: string;
  source: "manual" | "scheduled" | "agent" | "heartbeat" | "sentinel";
  status: "running" | "ok" | "error";
  progress?: number;       // 0..1 when known, omitted when indeterminate
  progress_label?: string;
  started_at: string;      // RFC3339
  ended_at?: string;
  duration_ms?: number;
  error?: string;
  result_summary?: string;
  // Boss-facing translation of `error` (errs.Humanize). Present only on failed
  // runs. The UI prefers human_error.title/.summary, raw `error` on expand.
  human_error?: HumanError;
  // Generic JSONB blob. For background.build runs it carries the agent's live
  // checklist: { todos, repo, currentFile }. Absent on other run kinds.
  meta?: RunMeta;
};

// HumanError mirrors core/internal/errs.Human — a plain-language failure
// explanation so the boss never has to read raw provider JSON.
export type HumanError = {
  category?: string;
  title?: string;
  summary?: string;
  impact?: string;
  action?: string;
  raw?: string;
};

// RunMeta is the typed view of mem_runs.meta the dock relies on. The agent
// authors todos + repo via the todo_write tool; the background loop writes
// currentFile per tool call. All optional — a run with no checklist has none.
export type TodoStatus = "pending" | "in_progress" | "completed";
export type RunTodo = { text: string; status: TodoStatus };
// MediaItem is one asset produced by a media.generate run (the media_job tool).
// Stamped onto mem_runs.meta.media so the Media tab renders finished assets from
// the same useRuns stream that drives the in-progress spinner.
export type MediaItem = {
  id?: string;
  kind: "image" | "video";
  mime?: string;
  name?: string;
  url: string; // browser-loadable src (public CDN url, or /api/workspace/download)
  path?: string;
};
export type RunMeta = {
  todos?: RunTodo[];
  repo?: string;
  currentFile?: string;
  worker?: string;
  backend?: string;
  media?: MediaItem[];
};

export type FetchRunsOpts = {
  kind?: string;
  targetId?: string;
  status?: "running" | "ok" | "error";
  limit?: number;
};

export async function fetchRuns(opts: FetchRunsOpts = {}, signal?: AbortSignal): Promise<RunDTO[]> {
  const q = new URLSearchParams();
  if (opts.kind) q.set("kind", opts.kind);
  if (opts.targetId) q.set("target_id", opts.targetId);
  if (opts.status) q.set("status", opts.status);
  if (opts.limit) q.set("limit", String(opts.limit));
  const qs = q.toString() ? `?${q.toString()}` : "";
  return (await getJSON<RunDTO[]>(`/api/runs${qs}`, signal)) ?? [];
}
export const fetchTools = (signal?: AbortSignal) =>
  getJSON<ToolDescriptor[]>("/api/tools", signal);
export const fetchMCP = (signal?: AbortSignal) => getJSON<MCPStatus[]>("/api/mcp", signal);
export const fetchSessions = (signal?: AbortSignal) =>
  getJSON<SessionDTO[]>("/api/sessions", signal);

export type SessionMessageDTO = {
  role: "user" | "assistant";
  text: string;
  created_at: string;
  // kind discriminates non-plain transcript rows. Absent for ordinary
  // turns; "dashboard_seed" for the context block injected by
  // Discuss-with-Jarvis (rendered as a distinct card, not a user bubble).
  kind?: string;
  // seed_kind is the originating dashboard item kind (e.g. "activity")
  // for a "dashboard_seed" row - used as the card's header label.
  seed_kind?: string;
  // curiosity_id links a "dashboard_seed" row to an open curiosity
  // question (best-effort title match) - when present the card renders
  // an "Approve & fix" action.
  curiosity_id?: string;
};

// deleteSession soft-deletes a session via POST /api/sessions/{id}/delete.
// The row is tombstoned (deleted_at = NOW()), its observations stay so
// memories built from them remain grounded, and the list / messages /
// hydrate endpoints all filter it out. Returns true on success, false on
// any failure - callers can still optimistically remove the row.
export async function deleteSession(id: string): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/sessions/${encodeURIComponent(id)}/delete`, {
      method: "POST",
    });
    return res.ok;
  } catch {
    return false;
  }
}

export const fetchSessionMessages = (id: string, signal?: AbortSignal) =>
  getJSON<SessionMessageDTO[]>(`/api/sessions/${encodeURIComponent(id)}/messages`, signal);

export const fetchMemoryCounts = (signal?: AbortSignal) =>
  getJSON<MemoryCounts>("/api/memory/counts", signal);

export const fetchObservations = (signal?: AbortSignal) =>
  getJSON<ObservationDTO[]>("/api/memory/observations", signal);

export const fetchReflections = (signal?: AbortSignal) =>
  getJSON<ReflectionDTO[]>("/api/memory/reflections", signal);

export const fetchReflectionChains = (signal?: AbortSignal) =>
  getJSON<ReflectionChainDTO[]>("/api/memory/reflection-chains", signal);

export const fetchPredictions = (signal?: AbortSignal) =>
  getJSON<PredictionDTO[]>("/api/memory/predictions", signal);

export const fetchMemories = (
  params: { tier?: string; project?: string } = {},
  signal?: AbortSignal,
) => {
  const qs = new URLSearchParams();
  if (params.tier) qs.set("tier", params.tier);
  if (params.project) qs.set("project", params.project);
  const suffix = qs.toString() ? `?${qs.toString()}` : "";
  return getJSON<MemoryDTO[]>(`/api/memory/memories${suffix}`, signal);
};

export const searchMemory = (q: string, signal?: AbortSignal) =>
  getJSON<SearchResult[]>(`/api/memory/search?q=${encodeURIComponent(q)}`, signal);

export const fetchProvenance = (memoryId: string, signal?: AbortSignal) =>
  getJSON<ProvenanceChain>(`/api/memory/cite/${memoryId}`, signal);

// ---- Knowledge graph -------------------------------------------------------

export type GraphNodeDTO = {
  id: string;
  type: string;
  name: string;
  degree: number;
  stale: boolean;
  metadata?: unknown;
};

export type GraphEdgeDTO = {
  id: string;
  source: string;
  target: string;
  type: string;
  confidence: number;
};

export type GraphResponse = {
  nodes: GraphNodeDTO[];
  edges: GraphEdgeDTO[];
  total_nodes: number;
  total_edges: number;
  node_types: string[];
};

export const fetchGraph = (
  opts: { limit?: number; type?: string; includeStale?: boolean } = {},
  signal?: AbortSignal,
) => {
  const params = new URLSearchParams();
  if (opts.limit) params.set("limit", String(opts.limit));
  if (opts.type) params.set("type", opts.type);
  if (opts.includeStale) params.set("include_stale", "1");
  const qs = params.toString();
  return getJSON<GraphResponse>(`/api/memory/graph${qs ? "?" + qs : ""}`, signal);
};

// ---- Boss profile (always-on identity primer) ------------------------------

export type ProfileFactDTO = {
  id: string;
  title: string;
  content: string;
  importance: number;
};

export const fetchProfile = (signal?: AbortSignal) =>
  getJSON<ProfileFactDTO[]>("/api/memory/profile", signal);

export async function upsertProfileFact(input: {
  title: string;
  content: string;
  importance?: number;
}): Promise<{ id: string } | null> {
  try {
    const res = await authedFetch(`/api/memory/profile`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
    if (!res.ok) return null;
    return (await res.json()) as { id: string };
  } catch {
    return null;
  }
}

export async function deleteProfileFact(id: string): Promise<boolean> {
  try {
    const res = await authedFetch(
      `/api/memory/profile?id=${encodeURIComponent(id)}`,
      { method: "DELETE" },
    );
    return res.ok;
  } catch {
    return false;
  }
}

// ---- Meta (lightweight key/value flags) ------------------------------------

export async function getMeta(key: string): Promise<string | null> {
  try {
    const res = await authedFetch(`/api/meta?key=${encodeURIComponent(key)}`);
    if (res.status === 404) return null;
    if (!res.ok) return null;
    const body = (await res.json()) as { value?: string };
    return body.value ?? null;
  } catch {
    return null;
  }
}

export async function setMeta(key: string, value: string): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/meta`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ key, value }),
    });
    return res.ok;
  } catch {
    return false;
  }
}

// ---- Voyager (auto-skill loop) ---------------------------------------------

export type VoyagerStatusDTO = {
  enabled: boolean;
  status: string;
  open_sessions: number;
  tracked_triplets: number;
};

export type SkillProposalDTO = {
  id: string;
  name: string;
  description: string;
  reasoning: string;
  skill_md: string;
  risk_level: "low" | "medium" | "high" | "critical";
  importance: number;
  importance_reason?: string;
  test_pass_rate: number;
  status: "candidate" | "promoted" | "rejected";
  parent_skill?: string;
  parent_version?: string;
  /** The version this revision would REPLACE (the parent's current active
   *  version), and that version's body — so the UI can show a real diff. */
  parent_active_version?: string;
  parent_body?: string;
  proposal_kind?: "draft" | "standalone" | string;
  revision?: number;
  changes_log?: Array<Record<string, unknown>>;
  conflicts?: string[];
  frontier_run_id?: string;
  score?: number;
  pareto_rank?: number;
  gepa_metadata?: Record<string, unknown>;
  parent_proposal_id?: string;
  created_at: string;
  decided_at?: string | null;
  last_merged_at?: string | null;
};

export const fetchVoyagerStatus = (signal?: AbortSignal) =>
  getJSON<VoyagerStatusDTO>("/api/voyager/status", signal);

export type NavCountsDTO = {
  dashboard: number;
  chat: number;
  memory: number;
  skills: number;
  overflow: {
    lab: number;
    heartbeat: number;
    logs: number;
  };
};

export const fetchNavCounts = (signal?: AbortSignal) =>
  getJSON<NavCountsDTO>("/api/nav/counts", signal);

export const fetchSkillProposals = (
  status = "candidate",
  signal?: AbortSignal,
  filters?: { frontier?: string; parent_skill?: string; proposal_kind?: string },
) => {
  const qs = new URLSearchParams();
  if (status) qs.set("status", status);
  if (filters?.frontier) qs.set("frontier", filters.frontier);
  if (filters?.parent_skill) qs.set("parent_skill", filters.parent_skill);
  if (filters?.proposal_kind) qs.set("proposal_kind", filters.proposal_kind);
  return getJSON<SkillProposalDTO[]>(`/api/voyager/proposals?${qs.toString()}`, signal);
};

export async function decideSkillProposal(id: string, decision: "promoted" | "rejected"): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/voyager/proposals/${id}/decide`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ decision }),
    });
    return res.ok;
  } catch {
    return false;
  }
}

// ---- Code proposals (Voyager source extractor) ----------------------------
//
// Code proposals are Voyager's source-refactor counterpart to skill proposals.
// When a session has the boss fighting the same file (multiple edits +
// failures), the source_extract hook drafts a refactor sketch via Haiku and
// lands a row in mem_code_proposals. The boss reviews here and decides whether
// the agent should attempt the change - actual edits still flow through
// ClaudeCodeGate → Trust queue.

export type CodeProposalStatus = "candidate" | "approved" | "rejected" | "applied";
export type CodeProposalDecision = "approved" | "rejected" | "applied";

export type CodeProposalDTO = {
  id: string;
  target_path: string;
  title: string;
  rationale: string;
  proposed_change: string;
  evidence: Record<string, unknown>;
  risk_level: "low" | "medium" | "high" | "critical";
  status: CodeProposalStatus;
  source_session?: string;
  created_at: string;
  decided_at?: string | null;
  decision_note?: string;
};

export const fetchCodeProposals = (status = "candidate", signal?: AbortSignal) =>
  getJSON<CodeProposalDTO[]>(
    `/api/voyager/code-proposals?status=${encodeURIComponent(status)}`,
    signal,
  );

export async function decideCodeProposal(
  id: string,
  decision: CodeProposalDecision,
  note = "",
): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/voyager/code-proposals/${id}/decide`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ decision, note }),
    });
    return res.ok;
  } catch {
    return false;
  }
}

// ---- Skills ----------------------------------------------------------------

export type SkillRiskLevel = "low" | "medium" | "high" | "critical";
export type SkillStatus = "active" | "candidate" | "archived";
export type SkillSource =
  | "manual"
  | "openclaw_imported"
  | "hermes_imported"
  | "auto_evolved"
  | "curriculum_proposed";

export type SkillSummaryDTO = {
  name: string;
  version: string;
  description: string;
  risk_level: SkillRiskLevel;
  confidence: number;
  importance: number;
  importance_reason?: string;
  source: SkillSource;
  status: SkillStatus;
  network_egress: string[];
  last_run_at?: string | null;
  success_rate: number;
};

export type SkillIODef = {
  name: string;
  type: string;
  default?: unknown;
  required?: boolean;
  doc?: string;
};

export type SkillDTO = {
  name: string;
  version: string;
  description: string;
  trigger_phrases: string[];
  inputs: SkillIODef[];
  outputs: SkillIODef[];
  risk_level: SkillRiskLevel;
  network_egress: string[];
  confidence: number;
  importance: number;
  importance_reason?: string;
  last_evolved?: string;
  body: string;
  impl_path?: string;
  impl_language?: string;
  source: SkillSource;
  status: SkillStatus;
  path?: string;
  // Detail-endpoint extras (server flattens mem_skills + last run).
  created_at?: string | null;
  updated_at?: string | null;
  last_run?: SkillRunDTO | null;
  total_runs?: number;
};

export type SkillVersionEntry = {
  version: string;
  skill_md: string;
  created_at: string;
  active: boolean;
  source?: string;
  confidence: number;
};

export type SkillRunDTO = {
  id: string;
  skill_name: string;
  version?: string;
  session_id?: string;
  trigger_source: string;
  input: Record<string, unknown>;
  output: string;
  success: boolean;
  duration_ms: number;
  started_at: string;
  ended_at?: string | null;
};

export type SkillTestDTO = {
  id: string;
  skill_name: string;
  description: string;
  inputs: Record<string, unknown>;
  expected: string;
  last_run_at?: string | null;
  last_passed?: boolean | null;
  source: string;
  created_at: string;
};

export const fetchSkills = (signal?: AbortSignal) =>
  getJSON<SkillSummaryDTO[]>("/api/skills", signal);

export const fetchSkill = (name: string, signal?: AbortSignal) =>
  getJSON<SkillDTO>(`/api/skills/${encodeURIComponent(name)}`, signal);

export const fetchSkillRuns = (name: string, limit = 25, signal?: AbortSignal) =>
  getJSON<SkillRunDTO[]>(
    `/api/skills/${encodeURIComponent(name)}/runs?limit=${limit}`,
    signal,
  );

export const fetchSkillTests = (name: string, signal?: AbortSignal) =>
  getJSON<SkillTestDTO[]>(`/api/skills/${encodeURIComponent(name)}/tests`, signal);

export const fetchSkillVersions = (name: string, signal?: AbortSignal) =>
  getJSON<SkillVersionEntry[]>(
    `/api/skills/${encodeURIComponent(name)}/versions`,
    signal,
  );

export async function promoteSkillVersion(
  name: string,
  version: string,
): Promise<boolean> {
  try {
    const res = await authedFetch(
      `/api/skills/${encodeURIComponent(name)}/promote`,
      {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ version }),
      },
    );
    return res.ok;
  } catch {
    return false;
  }
}

export async function generateSkillTests(name: string): Promise<SkillTestDTO[] | null> {
  try {
    const res = await authedFetch(`/api/skills/${encodeURIComponent(name)}/tests/generate`, {
      method: "POST",
    });
    if (!res.ok) return null;
    return (await res.json()) as SkillTestDTO[];
  } catch {
    return null;
  }
}

export async function reloadSkills(): Promise<{ count: number; errors: unknown[] } | null> {
  try {
    const res = await authedFetch(`/api/skills/reload`, { method: "POST" });
    if (!res.ok) return null;
    return (await res.json()) as { count: number; errors: unknown[] };
  } catch {
    return null;
  }
}

// ---- Audit log -------------------------------------------------------------

export type AuditRowDTO = {
  id: string;
  operation: string;
  actor: string;
  target: string;
  diff?: Record<string, unknown>;
  created_at: string;
};

export const fetchAuditLog = (limit = 100, op = "", signal?: AbortSignal) => {
  const qs = new URLSearchParams();
  qs.set("limit", String(limit));
  if (op) qs.set("op", op);
  return getJSON<AuditRowDTO[]>(`/api/memory/audit?${qs.toString()}`, signal);
};

// ---- Gym / plasticity ------------------------------------------------------

export type GymSummaryDTO = {
  ready: boolean;
  reflex_on: boolean;
  examples: number;
  datasets: number;
  runs: number;
  candidates: number;
  active: number;
  regressions: number;
  last_run_at?: string;
};

export type GymExampleDTO = {
  id: string;
  source_kind: string;
  source_id?: string;
  task_kind: string;
  label: string;
  score: number;
  privacy_class: string;
  metadata?: Record<string, unknown>;
  created_at: string;
};

export type GymDatasetDTO = {
  id: string;
  name: string;
  status: string;
  example_count: number;
  artifact_uri?: string;
  checksum?: string;
  filters?: Record<string, unknown>;
  updated_at: string;
};

export type GymRunDTO = {
  id: string;
  dataset_id?: string;
  adapter_id?: string;
  status: string;
  trigger: string;
  reason?: string;
  base_model?: string;
  metrics?: Record<string, unknown>;
  error?: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
};

export type GymAdapterDTO = {
  id: string;
  name: string;
  base_model: string;
  status: string;
  task_scope: string[];
  metrics?: Record<string, unknown>;
  created_at: string;
  promoted_at?: string;
  rolled_back_at?: string;
};

export type GymEvalDTO = {
  id: string;
  adapter_id?: string;
  eval_name: string;
  baseline_score: number;
  candidate_score: number;
  regression_count: number;
  passed: boolean;
  metrics?: Record<string, unknown>;
  created_at: string;
};

export type GymRouteDTO = {
  id: string;
  route: string;
  task_kind: string;
  active_adapter_id?: string;
  status: string;
  confidence: number;
  min_score: number;
  metadata?: Record<string, unknown>;
  updated_at: string;
};

export type GymSnapshotDTO = {
  summary: GymSummaryDTO;
  examples: GymExampleDTO[];
  datasets: GymDatasetDTO[];
  runs: GymRunDTO[];
  adapters: GymAdapterDTO[];
  evals: GymEvalDTO[];
  routes: GymRouteDTO[];
};

export const fetchGym = (limit = 50, signal?: AbortSignal) =>
  getJSON<GymSnapshotDTO>(`/api/gym?limit=${limit}`, signal);

export type GymExtractResultDTO = {
  inserted: number;
  evals: number;
  lessons: number;
  surprise: number;
};

export async function extractGymExamples(limit = 100): Promise<GymExtractResultDTO | null> {
  try {
    const res = await authedFetch("/api/gym", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ action: "extract_examples", limit }),
    });
    if (!res.ok) return null;
    return (await res.json()) as GymExtractResultDTO;
  } catch {
    return null;
  }
}

// ---- Heartbeat / Trust / IntentFlow ----------------------------------------

export type HeartbeatRunDTO = {
  id: string;
  started_at: string;
  ended_at?: string | null;
  duration_ms: number;
  findings: number;
  status: string;
  summary: string;
};

export type HeartbeatListDTO = {
  interval_seconds: number;
  runs: HeartbeatRunDTO[];
};

export type FindingDTO = {
  kind: string;
  title: string;
  detail?: string;
  pre_approved: boolean;
};

export type HeartbeatRunSummaryDTO = {
  id?: string;
  started_at: string;
  ended_at: string;
  duration_ms: number;
  findings: FindingDTO[];
  status: string;
  error?: string;
};

export const fetchHeartbeats = (signal?: AbortSignal) =>
  getJSON<HeartbeatListDTO>("/api/heartbeat", signal);

export type HeartbeatFindingDTO = {
  id: string;
  heartbeat_id: string;
  curiosity_id?: string;
  started_at: string;
  kind: string;
  title: string;
  detail?: string;
  pre_approved: boolean;
};

export const fetchHeartbeatFindings = (
  limit = 50,
  kind?: string,
  signal?: AbortSignal,
) => {
  const qs = new URLSearchParams({ limit: String(limit) });
  if (kind && kind !== "all") qs.set("kind", kind);
  return getJSON<HeartbeatFindingDTO[]>(`/api/heartbeat/findings?${qs.toString()}`, signal);
};

export async function decideCuriosityQuestion(
  id: string,
  decision: "asked" | "answered" | "dismissed" | "approved",
  answer = "",
): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/curiosity/questions/${id}/decide`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ decision, answer }),
    });
    return res.ok;
  } catch {
    return false;
  }
}

// dismissHeartbeatFinding marks a heartbeat finding (and every open
// duplicate sharing the same kind+title) as status='dismissed' so the
// dashboard activity feed stops surfacing it. Used by the ObjectViewer
// dismiss action on activity rows whose id starts with "hb-".
export async function dismissHeartbeatFinding(id: string): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/heartbeat/findings/${id}/dismiss`, {
      method: "POST",
    });
    return res.ok;
  } catch {
    return false;
  }
}

export async function runHeartbeatNow(): Promise<HeartbeatRunSummaryDTO | null> {
  try {
    const res = await authedFetch(`/api/heartbeat/run`, { method: "POST" });
    if (!res.ok) return null;
    return (await res.json()) as HeartbeatRunSummaryDTO;
  } catch {
    return null;
  }
}

export type TrustContractDTO = {
  id: string;
  title: string;
  risk_level: "low" | "medium" | "high" | "critical";
  source: string;
  action_spec: Record<string, unknown>;
  reasoning: string;
  cited_memory_ids: string[];
  risk_assessment: Record<string, unknown>;
  preview: string;
  status: "pending" | "approved" | "consumed" | "denied" | "snoozed";
  decided_at?: string | null;
  decision_note?: string;
  created_at: string;
  // batch_id groups contracts that came from the same skill run
  // (inbox triage, calendar prep, etc.). NULL/empty rows render
  // ungrouped with the single-row UX; non-empty rows fold into a
  // batch group that supports "Approve all N".
  batch_id?: string;
};

export const fetchTrustContracts = (status = "pending", signal?: AbortSignal) =>
  getJSON<TrustContractDTO[]>(`/api/trust-contracts?status=${encodeURIComponent(status)}`, signal);

export async function decideTrust(id: string, decision: string, note = ""): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/trust-contracts/${id}/decide`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ decision, note }),
    });
    return res.ok;
  } catch {
    return false;
  }
}

// decideTrustBatch flips every pending contract sharing a batch_id to the
// same decision in one round trip. Returns the count of contracts updated
// (zero on error) so the UI can render "Approved N of M" feedback. Pair
// with /api/trust-contracts?status=pending to refresh after.
export async function decideTrustBatch(
  batchId: string,
  decision: string,
  note = "",
): Promise<number> {
  try {
    const res = await authedFetch(
      `/api/trust-contracts/batch/${encodeURIComponent(batchId)}/decide`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ decision, note }),
      },
    );
    if (!res.ok) return 0;
    const json = (await res.json()) as { updated?: number };
    return json.updated ?? 0;
  } catch {
    return 0;
  }
}

export type IntentRecordDTO = {
  id: string;
  session_id?: string;
  user_msg: string;
  token: "silent" | "fast_intervention" | "full_assistance";
  confidence: number;
  reason: string;
  suggested_action?: string;
  created_at: string;
};

export const fetchIntentRecent = (limit = 50, signal?: AbortSignal) =>
  getJSON<IntentRecordDTO[]>(`/api/intent/recent?limit=${limit}`, signal);

// ---- Cron + Sentinels ------------------------------------------------------

export type CronJobDTO = {
  id: string;
  name: string;
  schedule: string;
  schedule_natural?: string;
  // All four kinds the Go scheduler accepts (core/internal/cron/types.go).
  // system_task / connector_poll are system-managed; the create form only
  // offers the two agent kinds, but the list/detail must render any of them.
  job_kind:
    | "system_event"
    | "isolated_agent_turn"
    | "connector_poll"
    | "system_task";
  target: string;
  enabled: boolean;
  max_retries: number;
  backoff_seconds: number;
  last_run_at?: string | null;
  last_run_status?: string;
  last_run_duration_ms?: number;
  next_run_at?: string | null;
  failure_count: number;
  created_at: string;
};

export const fetchCrons = (signal?: AbortSignal) =>
  getJSON<CronJobDTO[]>("/api/crons", signal);

export async function previewCron(schedule: string, count = 3): Promise<{ next: string[] } | { error: string } | null> {
  try {
    const res = await authedFetch(`/api/crons/preview`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ schedule, count }),
    });
    return (await res.json()) as { next: string[] } | { error: string };
  } catch {
    return null;
  }
}

export async function upsertCron(j: Partial<CronJobDTO>): Promise<{ id: string } | null> {
  try {
    const res = await authedFetch(`/api/crons`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(j),
    });
    if (!res.ok) return null;
    return (await res.json()) as { id: string };
  } catch {
    return null;
  }
}

export async function deleteCron(id: string): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/crons/${id}`, { method: "DELETE" });
    return res.ok;
  } catch {
    return false;
  }
}

// triggerCron fires a cron job immediately, regardless of its schedule.
// The next regular fire still happens at the cron-expression's next
// tick. Use this to test a freshly-edited job before its schedule rolls
// around - the run goes through the full agent loop, writes to mem_turns,
// surfaces in the agent-work feed exactly like a scheduled fire, and
// updates last_run_status so failures are visible. Returns { ok, error? }
// when reachable, null when the request itself failed.
export async function triggerCron(
  id: string,
): Promise<{ ok: boolean; error?: string } | null> {
  try {
    const res = await authedFetch(`/api/crons/${id}/run`, { method: "POST" });
    if (!res.ok) return null;
    return (await res.json()) as { ok: boolean; error?: string };
  } catch {
    return null;
  }
}

// ── Workflows (saved pipeline definitions) ───────────────────────────────────
// A workflow is a durable, repeatable multi-step pipeline (the Go engine runs
// the steps in fixed order, the LLM out of the loop). The Workflows tab lists
// these DEFINITIONS (runs surface on the Agent Work board). InputDef declares
// what a run needs, so the Run button collects it via a form instead of firing
// blind.
export type WorkflowInputDef = {
  key: string;
  label?: string;
  type?: "text" | "enum" | "number";
  options?: string[];
  required?: boolean;
  default?: string;
  doc?: string;
};

export type WorkflowStepDef = {
  name: string;
  kind: "tool" | "skill" | "agent" | "checkpoint";
  spec?: Record<string, unknown>;
  max_attempts?: number;
};

export type WorkflowDTO = {
  id: string;
  name: string;
  description: string;
  steps: WorkflowStepDef[];
  inputs?: WorkflowInputDef[];
  source: string;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
};

export const fetchWorkflows = (signal?: AbortSignal) =>
  getJSON<WorkflowDTO[]>("/api/workflows", signal);

// runWorkflow starts a run of a saved workflow with collected inputs. The
// engine claims it on the next tick; the run surfaces on the Agent Work board.
export async function runWorkflow(
  workflow: string,
  input: Record<string, unknown>,
): Promise<{ ok: boolean; run_id?: string; error?: string } | null> {
  try {
    const res = await authedFetch(`/api/workflows/run`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ workflow, input }),
    });
    const body = (await res.json()) as { ok?: boolean; run_id?: string; error?: string };
    if (!res.ok) return { ok: false, error: body.error || `HTTP ${res.status}` };
    return { ok: true, run_id: body.run_id };
  } catch (e) {
    return { ok: false, error: String(e) };
  }
}

export type SentinelDTO = {
  id: string;
  name: string;
  watch_type: "webhook" | "file_change" | "memory_event" | "external_api_poll" | "threshold";
  watch_config: Record<string, unknown>;
  action_chain: Array<Record<string, unknown>>;
  cooldown_seconds: number;
  last_triggered_at?: string | null;
  fire_count: number;
  enabled: boolean;
  created_at: string;
};

export const fetchSentinels = (signal?: AbortSignal) =>
  getJSON<SentinelDTO[]>("/api/sentinels", signal);

export async function upsertSentinel(s: Partial<SentinelDTO>): Promise<{ id: string } | null> {
  try {
    const res = await authedFetch(`/api/sentinels`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(s),
    });
    if (!res.ok) return null;
    return (await res.json()) as { id: string };
  } catch {
    return null;
  }
}

export async function deleteSentinel(id: string): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/sentinels/${id}`, { method: "DELETE" });
    return res.ok;
  } catch {
    return false;
  }
}

export async function invokeSkill(
  name: string,
  args: Record<string, unknown>,
): Promise<{ result?: { stdout?: string; success?: boolean }; error?: string } | null> {
  try {
    const res = await authedFetch(`/api/skills/${encodeURIComponent(name)}/invoke`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ args }),
    });
    return (await res.json()) as { result?: { stdout?: string; success?: boolean }; error?: string };
  } catch (e) {
    return { error: String(e) };
  }
}

// ---- Context usage (composer meter) ---------------------------------------

export type ContextCategoryDTO = {
  id: "system_prompt" | "tools" | "messages" | "free" | string;
  label: string;
  tokens: number;
};

export type ContextUsageDTO = {
  model: string;
  context_window: number;
  used_tokens: number;
  categories: ContextCategoryDTO[];
};

export const fetchContextUsage = (sessionId?: string, signal?: AbortSignal) => {
  const qs = sessionId ? `?session_id=${encodeURIComponent(sessionId)}` : "";
  return getJSON<ContextUsageDTO>(`/api/context/usage${qs}`, signal);
};

// ---- OpenAI OAuth (ChatGPT-subscription provider) --------------------------
//
// Paste-based PKCE connect flow. Studio renders a "Connect ChatGPT" button
// for the openai_oauth vendor; clicking it calls `startOpenAIOAuth`, opens
// the authorize URL in a new tab, then asks the user to paste the callback
// URL (or the bare code+state) into a box that calls `exchangeOpenAIOAuth`.

export type OpenAIOAuthStartResponse = {
  state: string;
  authorize_url: string;
  redirect_uri: string;
  expires_at: string;
};

export type OpenAIOAuthStatusResponse = {
  connected: boolean;
  provider?: string;
  account_id?: string;
  account_email?: string;
  scope?: string;
  expires_at?: string;
  last_refreshed?: string;
};

export async function startOpenAIOAuth(): Promise<OpenAIOAuthStartResponse | null> {
  try {
    const res = await authedFetch(`/api/auth/openai/start`, { method: "POST" });
    if (!res.ok) return null;
    return (await res.json()) as OpenAIOAuthStartResponse;
  } catch {
    return null;
  }
}

export async function exchangeOpenAIOAuth(input: {
  code?: string;
  state?: string;
  callback_url?: string;
}): Promise<OpenAIOAuthStatusResponse | { error: string }> {
  try {
    const res = await authedFetch(`/api/auth/openai/exchange`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
    const body = (await res.json()) as OpenAIOAuthStatusResponse & { error?: string };
    if (!res.ok) return { error: body.error ?? `HTTP ${res.status}` };
    return body;
  } catch (e) {
    return { error: String(e) };
  }
}

export async function fetchOpenAIOAuthStatus(
  signal?: AbortSignal,
): Promise<OpenAIOAuthStatusResponse | null> {
  return getJSON<OpenAIOAuthStatusResponse>(`/api/auth/openai/status`, signal);
}

export async function disconnectOpenAIOAuth(): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/auth/openai/disconnect`, { method: "POST" });
    return res.ok;
  } catch {
    return false;
  }
}

/**
 * Submit a thumbs-up / thumbs-down on an assistant message. Pass null to
 * clear the existing rating. Fire-and-forget - UI optimistically updates,
 * server captures into mem_observations so the memory layer can surface
 * "the boss tends to like this kind of response" on future turns.
 */
export async function submitMessageFeedback(
  messageId: string,
  rating: "up" | "down" | null,
): Promise<boolean> {
  let sessionId = "";
  if (typeof window !== "undefined") {
    try {
      sessionId = window.localStorage.getItem("infinity:sessionId") ?? "";
    } catch {
      /* ignore */
    }
  }
  try {
    const res = await authedFetch(`/api/messages/${encodeURIComponent(messageId)}/feedback`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ rating, session_id: sessionId }),
    });
    return res.ok;
  } catch {
    return false;
  }
}

// ─────────────────────────────────────────────────────────────────────────
// Composio connectors
//
// All four calls hit core's /api/connectors/composio/* proxy so the API
// key never leaves the server. Types are loose because Composio's response
// shape evolves - the /connectors page reads fields defensively rather
// than locking us to a specific schema version.
// ─────────────────────────────────────────────────────────────────────────

export type ComposioToolkit = {
  slug: string;
  name?: string;
  meta?: {
    description?: string;
    logo?: string;
    categories?: Array<{ slug?: string; name?: string }>;
  };
  no_auth?: boolean;
  is_local_toolkit?: boolean;
  auth_schemes?: string[];
};

export type ComposioConnectedAccount = {
  id: string;
  status?: string;
  toolkit?: { slug?: string; name?: string; logo?: string };
  user_id?: string;
  created_at?: string;
  updated_at?: string;
  // Composio's response carries auth metadata that often includes the
  // OAuth identity (Gmail address, Slack workspace name, GitHub login).
  // We don't strongly type it because each toolkit puts the identity
  // under a different path; the Studio row best-effort extracts it.
  meta?: Record<string, unknown>;
  data?: Record<string, unknown>;
};

export type ComposioPage<T> = {
  items: T[];
  next_cursor?: string | null;
  total_pages?: number;
  current_page?: number;
};

// parseComposioResponse reads the proxy response defensively. The
// happy path is JSON in both 2xx and 4xx (Composio errors are JSON,
// our proxy mirrors them). The unhappy path is when core itself
// hasn't deployed the route yet (Go's default mux returns
// "404 page not found\n" as text/plain) or when Cloudflare is in
// front and returns an HTML error page - JSON.parse on either of
// those is what produces the cryptic "Unexpected character at
// position 4" message. Distinguish so the user gets a useful hint.
async function parseComposioResponse(
  res: Response,
  what: string,
): Promise<{ error: string } | { value: Record<string, unknown> }> {
  const text = await res.text();
  if (!text) {
    return { error: `Empty response from core (${res.status}). Endpoint may not be deployed.` };
  }
  let body: Record<string, unknown>;
  try {
    body = JSON.parse(text) as Record<string, unknown>;
  } catch {
    // Non-JSON body - almost always means the route doesn't exist on
    // core yet (deploy pending) or a proxy/CDN error page intercepted.
    const sample = text.slice(0, 80).replace(/\s+/g, " ");
    if (text.startsWith("404")) {
      return {
        error: `Core hasn't deployed the /api/connectors/composio/* routes yet. Push & redeploy core. (got: "${sample}")`,
      };
    }
    return {
      error: `Non-JSON response from core (${res.status}, ${what}): "${sample}". Likely a proxy or undeployed route.`,
    };
  }
  if (!res.ok) {
    const msg =
      ((body?.error as Record<string, unknown>)?.message as string) ??
      (body?.error as string) ??
      `HTTP ${res.status}`;
    return { error: msg };
  }
  return { value: body };
}

export async function fetchComposioToolkits(params: {
  q?: string;
  cursor?: string;
  limit?: number;
  category?: string;
  signal?: AbortSignal;
}): Promise<ComposioPage<ComposioToolkit> | { error: string }> {
  const qs = new URLSearchParams();
  if (params.q) qs.set("search", params.q);
  if (params.cursor) qs.set("cursor", params.cursor);
  if (params.limit) qs.set("limit", String(params.limit));
  if (params.category) qs.set("category", params.category);
  try {
    const res = await authedFetch(
      `/api/connectors/composio/toolkits${qs.toString() ? `?${qs}` : ""}`,
      { signal: params.signal },
    );
    const body = await parseComposioResponse(res, "toolkits");
    if ("error" in body) return body;
    return body.value as ComposioPage<ComposioToolkit>;
  } catch (err) {
    return { error: err instanceof Error ? err.message : "network error" };
  }
}

export async function fetchComposioConnected(
  signal?: AbortSignal,
): Promise<ComposioPage<ComposioConnectedAccount> | { error: string }> {
  try {
    const res = await authedFetch("/api/connectors/composio/connected", { signal });
    const body = await parseComposioResponse(res, "connected accounts");
    if ("error" in body) return body;
    return body.value as ComposioPage<ComposioConnectedAccount>;
  } catch (err) {
    return { error: err instanceof Error ? err.message : "network error" };
  }
}

export async function initiateComposioConnect(
  toolkitSlug: string,
  opts?: { userId?: string; alias?: string },
): Promise<{ redirect_url?: string; id?: string; error?: string }> {
  try {
    const reqBody: Record<string, unknown> = { toolkit_slug: toolkitSlug };
    if (opts?.userId) reqBody.user_id = opts.userId;
    if (opts?.alias) reqBody.alias = opts.alias;
    const res = await authedFetch("/api/connectors/composio/connect", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(reqBody),
    });
    const body = (await res.json()) as Record<string, unknown>;
    if (!res.ok) {
      const msg =
        ((body?.error as Record<string, unknown>)?.message as string) ??
        (body?.error as string) ??
        `HTTP ${res.status}`;
      return { error: msg };
    }
    return {
      redirect_url:
        (body.redirect_url as string | undefined) ??
        (body.redirectUrl as string | undefined) ??
        ((body.connection_data as Record<string, unknown> | undefined)?.redirect_url as string | undefined),
      id: body.id as string | undefined,
    };
  } catch (err) {
    return { error: err instanceof Error ? err.message : "network error" };
  }
}

export async function refreshComposioAccount(
  id: string,
): Promise<{ redirect_url?: string; id?: string; error?: string }> {
  try {
    const res = await authedFetch(
      `/api/connectors/composio/accounts/${encodeURIComponent(id)}/refresh`,
      { method: "POST" },
    );
    const body = (await res.json()) as Record<string, unknown>;
    if (!res.ok) {
      const msg =
        ((body?.error as Record<string, unknown>)?.message as string) ??
        (body?.error as string) ??
        `HTTP ${res.status}`;
      return { error: msg };
    }
    return {
      redirect_url:
        (body.redirect_url as string | undefined) ??
        (body.redirectUrl as string | undefined) ??
        ((body.connection_data as Record<string, unknown> | undefined)?.redirect_url as string | undefined),
      id: body.id as string | undefined,
    };
  } catch (err) {
    return { error: err instanceof Error ? err.message : "network error" };
  }
}

export async function disconnectComposioAccount(id: string): Promise<boolean> {
  try {
    const res = await authedFetch(
      `/api/connectors/composio/accounts/${encodeURIComponent(id)}`,
      { method: "DELETE" },
    );
    return res.ok;
  } catch {
    return false;
  }
}

// Aliases are the boss-assigned human labels per connected account
// ("personal", "work", "support inbox"). Stored in infinity_meta as a
// single JSON map keyed by Composio's account id; the agent loop reads
// them via connectors.Cache and renders them into the per-turn system
// prompt so the model can route by name.
export type ComposioAliasMap = Record<string, string>;

export async function fetchComposioAliases(
  signal?: AbortSignal,
): Promise<ComposioAliasMap> {
  try {
    const res = await authedFetch("/api/connectors/composio/aliases", { signal });
    if (!res.ok) return {};
    const body = (await res.json()) as { aliases?: ComposioAliasMap };
    return body.aliases ?? {};
  } catch {
    return {};
  }
}

export async function setComposioAlias(
  accountId: string,
  alias: string,
): Promise<boolean> {
  try {
    const res = await authedFetch("/api/connectors/composio/aliases", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ account_id: accountId, alias }),
    });
    return res.ok;
  } catch {
    return false;
  }
}

// ---- Voice (OpenAI Realtime) -----------------------------------------------

export type VoiceSessionDTO = {
  client_secret: string;
  expires_at: number;
  model: string;
  voice: string;
  sdp_url: string;
};

export type VoiceToolResult = {
  call_id: string;
  output: string;
  is_error?: boolean;
  gated_for_trust?: boolean;
  contract_id?: string;
  preview?: string;
  /** When the tool mutated the session's active toolset (load_tools /
   *  unload_tools / tool_search), Core returns the new tools list in
   *  OpenAI Realtime's tool shape so the client can session.update. */
  updated_tools?: Array<Record<string, unknown>>;
};

export async function startVoiceSession(
  sessionId: string,
  query = "",
): Promise<VoiceSessionDTO | { error: string }> {
  try {
    const res = await authedFetch("/api/voice/session", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ session_id: sessionId, query }),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) return { error: body?.error ?? `voice/session ${res.status}` };
    return body as VoiceSessionDTO;
  } catch (err) {
    return { error: err instanceof Error ? err.message : String(err) };
  }
}

// reportVoiceError tells Core that a realtime voice session failed to
// connect. The WebRTC SDP exchange happens browser→OpenAI directly, so
// Core never sees these failures (no Railway log, no server signal) -
// this endpoint is the only way a quota/billing/network failure becomes
// observable server-side. Core logs it at error severity AND raises a
// Finding so the boss learns about it even when he's away from devtools.
// Best-effort: never block the UI on it.
export async function reportVoiceError(args: {
  sessionId: string;
  kind: string; // "sdp" | "ice-failed" | "mic-permission" | ...
  message: string;
}): Promise<boolean> {
  try {
    const res = await authedFetch("/api/voice/error", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        session_id: args.sessionId,
        kind: args.kind,
        message: args.message,
      }),
    });
    return res.ok;
  } catch {
    return false;
  }
}

// ---- Logs / traces (LangSmith-style turn-by-turn) -------------------------

export type TraceStatus = "in_flight" | "ok" | "empty" | "errored" | "interrupted";

export type TurnRowDTO = {
  id: string;
  session_id: string;
  session_name?: string;
  user_text: string;
  assistant_text?: string;
  model?: string;
  status: TraceStatus;
  stop_reason?: string;
  summary?: string;
  error?: string;
  started_at: string;
  ended_at?: string;
  input_tokens: number;
  output_tokens: number;
  tool_call_count: number;
  latency_ms: number;
  // origin of the session this turn belongs to: "chat" (default), "cron",
  // "heartbeat", … so /logs can badge a cron run instead of rendering it
  // chat-style. origin_label is the human name (e.g. the cron "inbox-triage").
  session_kind?: string;
  origin_label?: string;
};

export type TraceEventDTO = {
  id: string;
  kind: string; // user | thinking | tool_call | tool_result | tool_error | gate | prediction | assistant | session_start | session_end | ...
  source: "observation" | "prediction" | "trust_contract";
  timestamp: string;
  hook_name?: string;
  tool_name?: string;
  tool_call_id?: string;
  input?: string;
  output?: string;
  expected?: string;
  actual?: string;
  error?: string;
  reason?: string;
  raw_text?: string;
  surprise?: number;
  payload?: Record<string, unknown>;
};

export type TraceDetailDTO = {
  turn: TurnRowDTO;
  events: TraceEventDTO[];
};

export const fetchTraces = (
  opts: { sessionId?: string; status?: TraceStatus; limit?: number } = {},
  signal?: AbortSignal,
) => {
  const qs = new URLSearchParams();
  if (opts.sessionId) qs.set("session_id", opts.sessionId);
  if (opts.status) qs.set("status", opts.status);
  if (opts.limit) qs.set("limit", String(opts.limit));
  const suffix = qs.toString() ? `?${qs.toString()}` : "";
  return getJSON<TurnRowDTO[]>(`/api/traces${suffix}`, signal);
};

export const fetchTraceDetail = (turnId: string, signal?: AbortSignal) =>
  getJSON<TraceDetailDTO>(`/api/traces/${encodeURIComponent(turnId)}`, signal);

// ── Custom extensions (runtime self-register: mcp · http_tool · cli) ────────
// The manual half of "bring your own tool" - the agent's autonomous half goes
// through the extension_* tools. Both land in mem_extensions.

export type ExtensionKind = "mcp" | "http_tool" | "cli";

export interface Extension {
  id: string;
  name: string;
  kind: ExtensionKind;
  description: string;
  config?: Record<string, unknown>;
  enabled: boolean;
  source: string;
  status: string; // active | pending_auth | error | disabled | installing
  last_error?: string;
  auth_url?: string;
  auth_instructions?: string;
  resume_intent?: string;
  tool_name?: string;
  last_checked_at?: string;
  created_at: string;
  updated_at: string;
}

export async function fetchExtensions(
  signal?: AbortSignal,
): Promise<Extension[] | { error: string }> {
  try {
    const res = await authedFetch("/api/extensions", { signal });
    if (!res.ok) return { error: `HTTP ${res.status}` };
    const body = (await res.json()) as { extensions?: Extension[] };
    return body.extensions ?? [];
  } catch (err) {
    return { error: err instanceof Error ? err.message : "network error" };
  }
}

export async function registerExtension(body: {
  name: string;
  kind: ExtensionKind;
  description?: string;
  config: Record<string, unknown>;
  resume_intent?: string;
}): Promise<{ ok: boolean; async?: boolean; error?: string; extension?: Extension }> {
  try {
    const res = await authedFetch("/api/extensions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok && res.status !== 202) {
      const txt = await res.text();
      return { ok: false, error: txt || `HTTP ${res.status}` };
    }
    return (await res.json()) as { ok: boolean; async?: boolean; error?: string; extension?: Extension };
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : "network error" };
  }
}

export async function removeExtension(name: string): Promise<boolean> {
  try {
    const res = await authedFetch(`/api/extensions/${encodeURIComponent(name)}/remove`, {
      method: "POST",
    });
    return res.ok;
  } catch {
    return false;
  }
}

export async function checkExtension(
  name: string,
): Promise<{ ok: boolean; ready?: boolean; error?: string; extension?: Extension }> {
  try {
    const res = await authedFetch(`/api/extensions/${encodeURIComponent(name)}/check`, {
      method: "POST",
    });
    if (!res.ok) return { ok: false, error: `HTTP ${res.status}` };
    return (await res.json()) as { ok: boolean; ready?: boolean; extension?: Extension };
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : "network error" };
  }
}

// ── Compass / Mandate / Gauge / Wards (planning & verification primitives) ──
//
// These back the PAI-inspired building blocks: Compass (authored north-star),
// Mandate (per-task definition of done), Wards (privacy zones). Crosscheck and
// Gauge surface through existing channels (mem_runs / the WS gauge frame).

export type CompassSection = {
  section: string;
  label: string;
  content: string;
  position: number;
};

export const fetchCompass = (signal?: AbortSignal) =>
  getJSON<CompassSection[]>("/api/compass", signal);

export async function putCompassSection(
  section: string,
  content: string,
  position: number,
): Promise<boolean> {
  try {
    const res = await authedFetch("/api/compass", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ section, content, position }),
    });
    return res.ok;
  } catch {
    return false;
  }
}

export type MandateCriterion = {
  id: string;
  text: string;
  status: "pending" | "pass" | "fail";
  evidence?: string;
};

export type MandateCrosscheck = {
  overall?: string;
  confidence?: number;
  notes?: string;
  auditor?: string;
  criteria?: Array<{ id: string; pass: boolean; note: string }>;
};

export type MandateDTO = {
  id: string;
  session_id?: string;
  title: string;
  summary: string;
  status: "open" | "verifying" | "done" | "abandoned";
  criteria: MandateCriterion[];
  high_stakes: boolean;
  importance?: number;
  source: string;
  verified_at?: string;
  crosscheck?: MandateCrosscheck;
  created_at: string;
  updated_at: string;
};

export const fetchMandates = (opts: { active?: boolean; limit?: number } = {}, signal?: AbortSignal) => {
  const q = new URLSearchParams();
  if (opts.active) q.set("active", "1");
  if (opts.limit) q.set("limit", String(opts.limit));
  const qs = q.toString();
  return getJSON<MandateDTO[]>(`/api/mandates${qs ? `?${qs}` : ""}`, signal);
};

export const fetchMandate = (id: string, signal?: AbortSignal) =>
  getJSON<MandateDTO>(`/api/mandates/${encodeURIComponent(id)}`, signal);

export type WardDTO = {
  id?: string;
  glob: string;
  level: "private" | "sensitive";
  note?: string;
};

export const fetchWards = (signal?: AbortSignal) =>
  getJSON<WardDTO[]>("/api/wards", signal);

export async function putWard(ward: WardDTO): Promise<boolean> {
  try {
    const res = await authedFetch("/api/wards", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(ward),
    });
    return res.ok;
  } catch {
    return false;
  }
}

export async function deleteWard(idOrGlob: { id?: string; glob?: string }): Promise<boolean> {
  try {
    const q = new URLSearchParams();
    if (idOrGlob.id) q.set("id", idOrGlob.id);
    else if (idOrGlob.glob) q.set("glob", idOrGlob.glob);
    const res = await authedFetch(`/api/wards?${q.toString()}`, { method: "DELETE" });
    return res.ok;
  } catch {
    return false;
  }
}

export type GaugeReadDTO = {
  id: string;
  session_id?: string;
  turn_excerpt: string;
  tier: "glance" | "standard" | "deep";
  reason: string;
  created_at: string;
};

export const fetchGaugeRecent = (limit = 5, signal?: AbortSignal) =>
  getJSON<GaugeReadDTO[]>(`/api/gauge/recent?limit=${limit}`, signal);
