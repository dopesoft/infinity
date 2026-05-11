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
