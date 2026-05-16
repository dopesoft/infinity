"use client";

import { useEffect, useState } from "react";
import { Loader2, RefreshCw } from "lucide-react";
import {
  fetchBridgeSession,
  fetchBridgeWorkspaceGitStatus,
  type BridgeSessionView,
  type BridgeWorkspaceGitStatus,
} from "@/lib/canvas/api";
import { cn } from "@/lib/utils";

/**
 * CloudWorkspaceStalenessRow — surfaces "the cloud workspace volume's
 * local checkout is behind main on GitHub" the same way the deploy
 * banner surfaces "Core's binary is behind main."
 *
 * Renders ONLY when:
 *   1. There's a current session
 *   2. The active bridge for that session is the cloud workspace
 *   3. The cloud workspace's local HEAD differs from origin/main
 *
 * Silent otherwise. Polls every 60s — the cloud workspace pulls slowly
 * compared to Core's deploy, so we don't need 30s granularity.
 */
export function CloudWorkspaceStalenessRow({
  sessionId,
}: {
  sessionId: string | null;
}) {
  const [bridge, setBridge] = useState<BridgeSessionView | null>(null);
  const [status, setStatus] = useState<BridgeWorkspaceGitStatus | null>(null);
  const [refreshing, setRefreshing] = useState(false);

  useEffect(() => {
    if (!sessionId) {
      setBridge(null);
      setStatus(null);
      return;
    }
    let alive = true;
    const tick = async () => {
      const session = await fetchBridgeSession(sessionId);
      if (!alive) return;
      setBridge(session);
      // Only ask for workspace git-status when cloud is the active
      // bridge; on mac, the staleness concept doesn't apply (boss
      // controls his own checkout).
      if (session?.active_kind !== "cloud") {
        setStatus(null);
        return;
      }
      const gs = await fetchBridgeWorkspaceGitStatus();
      if (!alive) return;
      setStatus(gs);
    };
    void tick();
    const id = setInterval(() => void tick(), 60_000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, [sessionId]);

  // Gate render on all the prerequisites. The deploy banner has its
  // own row right above this — these two never both shout at once
  // unless both the binary AND the workspace are stale, which is
  // legitimate and worth seeing.
  if (!sessionId) return null;
  if (bridge?.active_kind !== "cloud") return null;
  if (!status || !status.behind) return null;

  const onRefresh = async () => {
    setRefreshing(true);
    try {
      const next = await fetchBridgeWorkspaceGitStatus();
      if (next) setStatus(next);
    } finally {
      setRefreshing(false);
    }
  };

  return (
    <div className="flex shrink-0 items-center gap-2 border-b border-warning/30 bg-warning/10 px-3 py-1.5 text-[11px] text-warning">
      <Loader2 className="size-3.5 animate-spin" aria-hidden />
      <span className="min-w-0 flex-1 truncate">
        Cloud workspace behind{" "}
        <span className="font-mono">{status.branch || "main"}</span> by{" "}
        <span className="font-semibold">
          {status.commits_behind || "≥1"} commit
          {status.commits_behind === 1 ? "" : "s"}
        </span>{" "}
        — ask Jarvis to{" "}
        <span className="font-mono">git_pull</span>{" "}
        <span className="font-mono opacity-70">
          ({shortSHA(status.local_sha)} → {shortSHA(status.remote_sha)})
        </span>
      </span>
      <button
        type="button"
        onClick={() => void onRefresh()}
        disabled={refreshing}
        aria-label="Check workspace git status now"
        title="Check now"
        className={cn(
          "inline-flex size-6 shrink-0 items-center justify-center rounded text-warning transition-colors hover:bg-warning/20",
          refreshing && "opacity-50",
        )}
      >
        <RefreshCw className={cn("size-3", refreshing && "animate-spin")} aria-hidden />
      </button>
    </div>
  );
}

function shortSHA(sha: string): string {
  return sha ? sha.slice(0, 7) : "—";
}
