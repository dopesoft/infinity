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

export const fetchReflections = (signal?: AbortSignal) =>
  getJSON<ReflectionDTO[]>("/api/memory/reflections", signal);

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

// ---- Code proposals (Voyager source extractor) ----------------------------
//
// Code proposals are Voyager's source-refactor counterpart to skill proposals.
// When a session has the boss fighting the same file (multiple edits +
// failures), the source_extract hook drafts a refactor sketch via Haiku and
// lands a row in mem_code_proposals. The boss reviews here and decides whether
// the agent should attempt the change — actual edits still flow through
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
  decision: "asked" | "answered" | "dismissed",
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

// ─────────────────────────────────────────────────────────────────────────
// Composio connectors
//
// All four calls hit core's /api/connectors/composio/* proxy so the API
// key never leaves the server. Types are loose because Composio's response
// shape evolves — the /connectors page reads fields defensively rather
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
// front and returns an HTML error page — JSON.parse on either of
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
    // Non-JSON body — almost always means the route doesn't exist on
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

export async function runVoiceTool(args: {
  sessionId: string;
  callId: string;
  name: string;
  input: Record<string, unknown>;
}): Promise<VoiceToolResult | { error: string }> {
  try {
    const res = await authedFetch("/api/voice/tool", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        session_id: args.sessionId,
        call_id: args.callId,
        name: args.name,
        input: args.input,
      }),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) return { error: body?.error ?? `voice/tool ${res.status}` };
    return body as VoiceToolResult;
  } catch (err) {
    return { error: err instanceof Error ? err.message : String(err) };
  }
}

export async function recordVoiceTurn(args: {
  sessionId: string;
  role: "user" | "assistant";
  text: string;
}): Promise<boolean> {
  try {
    const res = await authedFetch("/api/voice/turn", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        session_id: args.sessionId,
        role: args.role,
        text: args.text,
      }),
    });
    return res.ok;
  } catch {
    return false;
  }
}
