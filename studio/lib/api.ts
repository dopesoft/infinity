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
  started_at: string;
  message_count: number;
};

export type MemoryCounts = {
  observations: number;
  memories: number;
  graph_nodes: number;
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

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T | null> {
  try {
    const res = await fetch(`${coreBaseURL()}${path}`, { signal });
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

// ---- Skills (Phase 4) ------------------------------------------------------

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
    const res = await fetch(`${coreBaseURL()}/api/skills/reload`, { method: "POST" });
    if (!res.ok) return null;
    return (await res.json()) as { count: number; errors: unknown[] };
  } catch {
    return null;
  }
}

export async function invokeSkill(
  name: string,
  args: Record<string, unknown>,
): Promise<{ result?: { stdout?: string; success?: boolean }; error?: string } | null> {
  try {
    const res = await fetch(`${coreBaseURL()}/api/skills/${encodeURIComponent(name)}/invoke`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ args }),
    });
    return (await res.json()) as { result?: { stdout?: string; success?: boolean }; error?: string };
  } catch (e) {
    return { error: String(e) };
  }
}
