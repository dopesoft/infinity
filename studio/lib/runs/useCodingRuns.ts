"use client";

// useCodingRuns — "what coding work is in flight right now?", as one hook.
//
// Coding runs land in mem_runs under TWO kinds, because there are two ways in:
//
//   code_agent       the boss (or Jarvis) delegating inline, inside a turn
//   background.build a detached build that outlives the conversation
//
// Both are the same thing to a person watching: Claude Code editing his repo.
// Every surface that wants "is a build running?" was therefore calling useRuns
// twice and merging by hand, and each copy picked its own subset — the pinned
// dock above the composer watched only background.build, so a code_agent run
// (the far more common one) was invisible in the exact place the boss looks
// for it.
//
// One named primitive instead, per the reuse-first rule: the kinds live in one
// place, the merge is written once, and a third coding kind is a one-line
// change that every consumer inherits.

import { useMemo } from "react";
import { useRuns } from "@/lib/runs/useRuns";
import type { RunDTO } from "@/lib/api";

/** The mem_runs kinds that mean "Claude Code is working in a repo". */
export const CODING_RUN_KINDS = ["code_agent", "background.build"] as const;

export type UseCodingRunsOpts = {
  /** Only in-flight runs when true (the common case for a live surface). */
  runningOnly?: boolean;
  /** Held empty + no request while false, e.g. before a session id resolves. */
  enabled?: boolean;
  limit?: number;
};

export type UseCodingRunsResult = {
  /** Every matching run, newest first. */
  runs: RunDTO[];
  /** The newest one, or null. What a single-status surface renders. */
  latest: RunDTO | null;
  /**
   * The newest run actually executing on Claude Code. A code_agent run always
   * is; a background.build only when it landed on the Mac (meta.engine), since
   * on the Cloud bridge the same kind runs on the settings model instead.
   */
  claudeRun: RunDTO | null;
};

export function useCodingRuns(opts: UseCodingRunsOpts = {}): UseCodingRunsResult {
  const { runningOnly = false, enabled = true, limit } = opts;
  const status = runningOnly ? "running" : undefined;

  // One useRuns per kind: the hook filters server-side on a single kind, and
  // two cheap subscriptions beat fetching every run in the system and
  // filtering in the browser.
  const { runs: codeRuns } = useRuns({ kind: "code_agent", status, limit, enabled });
  const { runs: buildRuns } = useRuns({ kind: "background.build", status, limit, enabled });

  return useMemo(() => {
    const runs = [...codeRuns, ...buildRuns].sort((a, b) =>
      (b.started_at ?? "").localeCompare(a.started_at ?? ""),
    );
    const claudeRun =
      codeRuns[0] ?? buildRuns.find((r) => r.meta?.engine === "claude_code") ?? null;
    return { runs, latest: runs[0] ?? null, claudeRun };
  }, [codeRuns, buildRuns]);
}
