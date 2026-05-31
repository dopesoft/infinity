"use client";

import { useEffect, useRef, useState } from "react";
import { fetchCanvasGitStatus } from "@/lib/canvas/api";

/**
 * useGitPendingCount — polls `git status` for the current canvas repo and
 * returns the count of changed FILES in git for the current canvas repo.
 *
 * Badge semantics are intentionally file-based, not deploy-state based:
 *
 *   - entries (staged + unstaged + untracked = files differing from HEAD)
 *   - ahead commits DO NOT increment the badge
 *
 * Why this hook exists separately from CanvasGitPanel's own poll: the
 * panel only polls when it's the active tab. The Changes-tab badge has
 * to render a live count even when the Files tab is the active one, so
 * we need polling at a higher level than the panel. Cost is one extra
 * cheap GET every 4s while Canvas is open — small price for the boss
 * being able to glance at the tab and see "3 changes waiting."
 *
 * Returns 0 when there's no root yet (project not bound) or polls fail.
 */
export function useGitPendingCount(root: string | null, sessionId: string | null, intervalMs = 4_000): number {
  const [count, setCount] = useState(0);
  const inFlight = useRef(false);

  useEffect(() => {
    if (!root) {
      setCount(0);
      return;
    }
    let cancelled = false;

    const tick = async () => {
      if (inFlight.current) return;
      inFlight.current = true;
      try {
        const res = await fetchCanvasGitStatus(root, sessionId ?? "");
        if (cancelled || !res) return;
        // Badge = raw changed-file count in git. Opening/viewing files must
        // never decrement it, and pushed-vs-unpushed commit state is shown
        // elsewhere in the git panel/status bar.
        const entries = res.entries?.length ?? 0;
        setCount(entries);
      } catch {
        // Network blip — keep last known count rather than zeroing out;
        // a missing badge is more confusing than a stale one.
      } finally {
        inFlight.current = false;
      }
    };

    void tick();
    const id = window.setInterval(tick, intervalMs);
    return () => {
      cancelled = true;
      window.clearInterval(id);
    };
  }, [root, sessionId, intervalMs]);

  return count;
}
