import { authedFetch } from "@/lib/api";

/**
 * Canvas API client — typed wrappers around /api/canvas/*. All requests run
 * through authedFetch so the Supabase JWT travels with every call. Mutations
 * that touch the home Mac return a contract_id; the caller polls
 * /api/trust-contracts to know when the boss has approved.
 */

export type FSEntry = {
  name: string;
  type: "dir" | "file" | "symlink";
  size?: number;
  mtime?: string;
};

export type FSListResponse = {
  path: string;
  root: string;
  entries: FSEntry[];
};

export type FSReadResponse = {
  path: string;
  content: string;
  language: string;
  sha: string;
  size: number;
};

export type FSSaveResponse = {
  contract_id?: string;
  status: "pending" | "approved" | "denied" | "conflict" | "saved";
  path: string;
  new_sha?: string;
  reason?: string;
};

export type GitStatusEntry = {
  path: string;
  status: string;
  staged: boolean;
  branch?: string;
};

export type GitStatusResponse = {
  repo: string;
  branch: string;
  ahead: number;
  behind: number;
  entries: GitStatusEntry[];
};

export type GitDiffResponse = {
  path: string;
  staged: boolean;
  diff: string;
};

export type GitMutationResponse = {
  contract_id?: string;
  status: "pending" | "approved" | "denied" | "executed";
  output?: string;
  reason?: string;
};

export type CanvasConfig = {
  root: string;
  root_is_set: boolean;
  preview_url?: string;
  mac_bridge_ok: boolean;
  // Bridge-configured fallback project_path. When a session has no
  // project_path attached, Studio uses this so the Files / Git panels
  // default to Jarvis's own codebase instead of a blank "set workspace
  // root first" state.
  default_project_path?: string;
};

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T | null> {
  try {
    const res = await authedFetch(path, { signal });
    if (!res.ok) return null;
    return (await res.json()) as T;
  } catch {
    return null;
  }
}

async function postJSON<T>(path: string, body: unknown): Promise<T | null> {
  try {
    const res = await authedFetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok && res.status !== 202 && res.status !== 409) return null;
    return (await res.json()) as T;
  } catch {
    return null;
  }
}

export const fetchCanvasConfig = (signal?: AbortSignal) =>
  getJSON<CanvasConfig>("/api/canvas/config", signal);

// Deploy-staleness snapshot. Core polls GitHub main HEAD every 5 min and
// compares to RAILWAY_GIT_COMMIT_SHA. Studio surfaces the gap in the
// Canvas Files column so Jarvis (and the boss) know when a fresher
// build is on its way.
export type DeployStatus = {
  running_sha: string;
  latest_sha: string;
  behind: boolean;
  commits_behind: number;
  branch: string;
  repo: string;
  checked_at: string;
};

export const fetchDeployStatus = (signal?: AbortSignal) =>
  getJSON<DeployStatus>("/api/deploy/status", signal);

// Diagnostic — dumps tool registration + each LS strategy's raw output.
// Surfaced via a button in the Files tab's empty state so the boss can
// trigger it without futzing with auth tokens in DevTools.
export const fetchCanvasDebug = (path: string, signal?: AbortSignal) =>
  getJSON<unknown>(
    `/api/canvas/debug?path=${encodeURIComponent(path)}`,
    signal,
  );

export const fetchCanvasFSList = (path = "", signal?: AbortSignal) =>
  getJSON<FSListResponse>(
    `/api/canvas/fs/ls${path ? `?path=${encodeURIComponent(path)}` : ""}`,
    signal,
  );

export const fetchCanvasFSRead = (path: string, signal?: AbortSignal) =>
  getJSON<FSReadResponse>(
    `/api/canvas/fs/read?path=${encodeURIComponent(path)}`,
    signal,
  );

export const saveCanvasFile = (input: {
  path: string;
  content: string;
  base_sha?: string;
  session_id?: string;
}) => postJSON<FSSaveResponse>(`/api/canvas/fs/save`, input);

export const fetchCanvasGitStatus = (repo: string, signal?: AbortSignal) =>
  getJSON<GitStatusResponse>(
    `/api/canvas/git/status?repo=${encodeURIComponent(repo)}`,
    signal,
  );

export const fetchCanvasGitDiff = (
  input: { repo: string; path?: string; staged?: boolean },
  signal?: AbortSignal,
) => {
  const qs = new URLSearchParams();
  qs.set("repo", input.repo);
  if (input.path) qs.set("path", input.path);
  if (input.staged) qs.set("staged", "1");
  return getJSON<GitDiffResponse>(`/api/canvas/git/diff?${qs.toString()}`, signal);
};

export const canvasGitStage = (input: { repo: string; paths?: string[]; session_id?: string }) =>
  postJSON<GitMutationResponse>(`/api/canvas/git/stage`, input);

export const canvasGitCommit = (input: { repo: string; message: string; session_id?: string }) =>
  postJSON<GitMutationResponse>(`/api/canvas/git/commit`, input);

export const canvasGitPush = (input: {
  repo: string;
  remote?: string;
  branch?: string;
  session_id?: string;
}) => postJSON<GitMutationResponse>(`/api/canvas/git/push`, input);

export const canvasGitPull = (input: {
  repo: string;
  remote?: string;
  branch?: string;
  session_id?: string;
}) => postJSON<GitMutationResponse>(`/api/canvas/git/pull`, input);

// ---- Bridge (Mac vs Cloud workspace) --------------------------------------
//
// The router on Core decides per-session whether a `bridge_*` tool call
// goes to the home Mac (Anthropic Max-subscription Claude Code) or to the
// Railway-hosted workspace container (ChatGPT subscription, no metered
// API). Studio surfaces the active choice via a persistent pill in the
// session header.

export type BridgeStatus = {
  configured: boolean;
  mac_healthy: boolean;
  cloud_healthy: boolean;
  mac_url?: string;
  cloud_url?: string;
  checked_at?: string;
};

export type BridgeSessionView = {
  session_id: string;
  preference: "auto" | "mac" | "cloud";
  active_kind?: "mac" | "cloud";
  active_url?: string;
  why_active?: string;
  bridge_error?: string;
};

export const fetchBridgeStatus = (signal?: AbortSignal) =>
  getJSON<BridgeStatus>("/api/bridge/status", signal);

export const fetchBridgeSession = (id: string, signal?: AbortSignal) =>
  getJSON<BridgeSessionView>(
    `/api/bridge/session/${encodeURIComponent(id)}`,
    signal,
  );

export const setBridgePreference = (
  id: string,
  preference: "auto" | "mac" | "cloud",
) =>
  postJSON<BridgeSessionView>(
    `/api/bridge/session/${encodeURIComponent(id)}`,
    { preference },
  );

export const refreshBridgeStatus = () =>
  postJSON<BridgeStatus>("/api/bridge/refresh", {});

// Cloud-workspace git staleness — answers "is the Railway workspace
// volume's local checkout behind main on GitHub?" Same shape question
// as DeployStatus but pointed at the cloud bridge's working tree, not
// at Core's own binary.
export type BridgeWorkspaceGitStatus = {
  branch: string;
  local_sha: string;
  remote_sha: string;
  behind: boolean;
  commits_behind: number;
  repo: string;
};

export const fetchBridgeWorkspaceGitStatus = (signal?: AbortSignal) =>
  getJSON<BridgeWorkspaceGitStatus>("/api/bridge/workspace/git-status", signal);
