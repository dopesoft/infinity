"use client";

import { useEffect, useState } from "react";

/**
 * live-tool - the tool call the agent is executing RIGHT NOW in the boss's
 * chat, published by useChat and read by status chrome (the bridge pill).
 *
 * Why a module-scoped store, not props: the pill lives in the canvas header
 * and the chat lives in another subtree; this mirrors use-model's pub/sub so
 * any chrome can ask "what is he doing this second" without prop drilling.
 * It is per-tab and ephemeral by design: durable "is it running" state is
 * mem_runs (useRuns); this is only the sub-second "which tool is in flight"
 * signal the WS stream already carries.
 */

export type LiveTool = {
  id: string;
  name: string;
  startedAt: number;
};

let current: LiveTool | null = null;
const subscribers = new Set<(t: LiveTool | null) => void>();

export function publishLiveTool(next: LiveTool | null) {
  if (next === current) return;
  if (next && current && next.id === current.id && next.name === current.name) return;
  current = next;
  for (const fn of subscribers) fn(next);
}

/** Clear the live tool when the call with this id finishes. */
export function clearLiveTool(id?: string) {
  if (!current) return;
  if (id && current.id !== id) return;
  publishLiveTool(null);
}

export function useLiveTool(): LiveTool | null {
  const [tool, setTool] = useState<LiveTool | null>(current);
  useEffect(() => {
    const fn = (t: LiveTool | null) => setTool(t);
    subscribers.add(fn);
    setTool(current);
    return () => {
      subscribers.delete(fn);
    };
  }, []);
  return tool;
}

// Tools where the CHAT MODEL itself is authoring or running code on a
// bridge (as opposed to code_agent, which hands the work to Claude Code).
const CODING_TOOL_RE = /^(claude_code__(Edit|Write|MultiEdit|NotebookEdit|Bash)|fs_save|fs_edit|bash_run|git_(commit|stage|push|pull)|code_agent|background_build)$/;

export function isCodingTool(name: string | undefined | null): boolean {
  return !!name && CODING_TOOL_RE.test(name);
}
