"use client";

import { useEffect, useState } from "react";
import { GitBranch, Loader2, RefreshCw } from "lucide-react";
import {
  fetchBridgeSession,
  fetchBridgeWorkspaceGitStatus,
  pullBridgeWorkspace,
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
  const [pulling, setPulling] = useState(false);
  const [pullError, setPullError] = useState<string | null>(null);

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

  const onPull = async () => {
    setPulling(true);
    setPullError(null);
    try {
      const res = await pullBridgeWorkspace();
      if (res?.ok && res.status) {
        setStatus(res.status);
      } else if (res && !res.ok) {
        setPullError(res.error || "Pull failed");
      } else {
        setPullError("Couldn't reach Core");
      }
    } finally {
      setPulling(false);
    }
  };

  return (
    <div className="flex shrink-0 items-center gap-2 border-b border-warning/30 bg-warning/10 px-3 py-1.5 text-[11px] text-warning">
      {pulling ? (
        <Loader2 className="size-3.5 shrink-0 animate-spin" aria-hidden />
      ) : (
        <GitBranch className="size-3.5 shrink-0" aria-hidden />
      )}
      <span className="min-w-0 flex-1 truncate">
        Cloud workspace behind{" "}
        <span className="font-mono">{status.branch || "main"}</span> by{" "}
        <span className="font-semibold">
          {status.commits_behind || "≥1"} commit
          {status.commits_behind === 1 ? "" : "s"}
        </span>{" "}
        {pullError ? (
          <span className="text-danger">— {pullError}</span>
        ) : (
          <>
            — tap{" "}
            <RefreshCw className="inline size-3 align-[-1px]" aria-hidden /> to{" "}
            <span className="font-mono">git pull --ff-only</span>{" "}
          </>
        )}
        <span className="font-mono opacity-70">
          ({shortSHA(status.local_sha)} → {shortSHA(status.remote_sha)})
        </span>
      </span>
      <button
        type="button"
        onClick={() => void onPull()}
        disabled={pulling}
        aria-label="Pull main into cloud workspace"
        title="Pull main now (git pull --ff-only)"
        className={cn(
          "inline-flex size-6 shrink-0 items-center justify-center rounded text-warning transition-colors hover:bg-warning/20",
          pulling && "opacity-50",
        )}
      >
        <RefreshCw className={cn("size-3", pulling && "animate-spin")} aria-hidden />
      </button>
    </div>
  );
}

function shortSHA(sha: string): string {
  return sha ? sha.slice(0, 7) : "—";
}
