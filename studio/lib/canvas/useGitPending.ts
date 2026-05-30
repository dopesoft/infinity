"use client";

import { useEffect, useRef, useState } from "react";
import { fetchCanvasGitStatus } from "@/lib/canvas/api";

/**
 * useGitPendingCount — polls `git status` for the current canvas repo and
 * returns the count of "pending" changes the boss should see before the
 * next deploy.
 *
 * "Pending" here means anything that is NOT yet on the remote:
 *
 *   - entries (staged + unstaged + untracked = files differing from HEAD)
 *   - ahead   (commits on the local branch that haven't been pushed yet)
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
        // Pending = uncommitted files + commits ahead of remote. We don't
        // subtract `behind` — that's "remote has stuff we don't have," not
        // "we have stuff to deploy."
        const entries = res.entries?.length ?? 0;
        const ahead = res.ahead ?? 0;
        setCount(entries + ahead);
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
