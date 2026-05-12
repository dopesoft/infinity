import { getAccessToken } from "@/lib/auth/session";

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

function coreBaseURL(): string {
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
export const fetchTools = (signal?: AbortSignal) =>
  getJSON<ToolDescriptor[]>("/api/tools", signal);
export const fetchMCP = (signal?: AbortSignal) => getJSON<MCPStatus[]>("/api/mcp", signal);
export const fetchSessions = (signal?: AbortSignal) =>
  getJSON<SessionDTO[]>("/api/sessions", signal);

export type SessionMessageDTO = {
  role: "user" | "assistant";
  text: string;
  created_at: string;
};

export const fetchSessionMessages = (id: string, signal?: AbortSignal) =>
  getJSON<SessionMessageDTO[]>(`/api/sessions/${encodeURIComponent(id)}/messages`, signal);

export const fetchMemoryCounts = (signal?: AbortSignal) =>
  getJSON<MemoryCounts>("/api/memory/counts", signal);

export const fetchObservations = (signal?: AbortSignal) =>
  getJSON<ObservationDTO[]>("/api/memory/observations", signal);

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
  test_pass_rate: number;
  status: "candidate" | "promoted" | "rejected";
  parent_skill?: string;
  parent_version?: string;
  created_at: string;
  decided_at?: string | null;
};

export const fetchVoyagerStatus = (signal?: AbortSignal) =>
  getJSON<VoyagerStatusDTO>("/api/voyager/status", signal);

export const fetchSkillProposals = (status = "candidate", signal?: AbortSignal) =>
  getJSON<SkillProposalDTO[]>(
    `/api/voyager/proposals?status=${encodeURIComponent(status)}`,
    signal,
  );

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
  last_evolved?: string;
  body: string;
  impl_path?: string;
  impl_language?: string;
  source: SkillSource;
  status: SkillStatus;
  path?: string;
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

export const fetchSkills = (signal?: AbortSignal) =>
  getJSON<SkillSummaryDTO[]>("/api/skills", signal);

export const fetchSkill = (name: string, signal?: AbortSignal) =>
  getJSON<SkillDTO>(`/api/skills/${encodeURIComponent(name)}`, signal);

export const fetchSkillRuns = (name: string, limit = 25, signal?: AbortSignal) =>
  getJSON<SkillRunDTO[]>(
    `/api/skills/${encodeURIComponent(name)}/runs?limit=${limit}`,
    signal,
  );

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
  status: "pending" | "approved" | "denied" | "snoozed";
  decided_at?: string | null;
  decision_note?: string;
  created_at: string;
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
  job_kind: "system_event" | "isolated_agent_turn";
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

/**
 * Submit a thumbs-up / thumbs-down on an assistant message. Pass null to
 * clear the existing rating. Fire-and-forget — UI optimistically updates,
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
