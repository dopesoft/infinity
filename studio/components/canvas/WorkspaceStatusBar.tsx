"use client";

import { useEffect, useState } from "react";
import { ArrowDown, ArrowUp, Check, RefreshCw } from "lucide-react";
import {
  fetchBridgeSession,
  fetchBridgeStatus,
  fetchBridgeWorkspaceGitStatus,
  pullBridgeWorkspace,
  type BridgeSessionView,
  type BridgeWorkspaceGitStatus,
} from "@/lib/canvas/api";
import { useCanvasStore } from "@/lib/canvas/store";
import { cn } from "@/lib/utils";

/**
 * WorkspaceStatusBar - the ONE status line atop the Files column. It answers,
 * for the project the boss is actually in:
 *
 *   • WHERE do these files live?   → Cloud vs Mac (the active bridge)
 *   • WHICH project?               → the active project name
 *   • Is it in sync with its repo? → synced / N behind (+ one-tap pull) / N ahead
 *
 * This replaces the old pile of three separate banners (Core deploy status +
 * a bare "Source: Cloud" label + an infinity-only staleness row) that shouted
 * unrelated things - most confusingly "behind main" about Jarvis's OWN repo
 * while the boss was in an unrelated project. Now every segment is about the
 * repo on screen, and the bar reports per-project (store.root), not infinity.
 *
 * Silent until a session + a configured bridge exist. The git-sync segment is
 * Cloud-only (on Mac the boss owns his checkout; git lives in the Changes tab)
 * and only when there's a real project repo to report on.
 */
export function WorkspaceStatusBar({ sessionId }: { sessionId: string | null }) {
  const store = useCanvasStore();
  const projectRoot = store.root?.trim() || "";
  const [configured, setConfigured] = useState(false);
  const [session, setSession] = useState<BridgeSessionView | null>(null);
  const [git, setGit] = useState<BridgeWorkspaceGitStatus | null>(null);
  const [pulling, setPulling] = useState(false);
  const [pullError, setPullError] = useState<string | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    void (async () => {
      const s = await fetchBridgeStatus(ac.signal);
      if (s) setConfigured(!!s.configured);
    })();
    return () => ac.abort();
  }, []);

  useEffect(() => {
    if (!sessionId) {
      setSession(null);
      setGit(null);
      return;
    }
    let alive = true;
    const tick = async () => {
      const s = await fetchBridgeSession(sessionId);
      if (!alive) return;
      setSession(s);
      // Sync state is Cloud-only and per-project: report on store.root vs its
      // own upstream, never Jarvis's infinity self-repo.
      if (s?.active_kind === "cloud" && projectRoot) {
        const gs = await fetchBridgeWorkspaceGitStatus(projectRoot);
        if (!alive) return;
        setGit(gs);
      } else {
        setGit(null);
      }
    };
    void tick();
    const id = setInterval(() => void tick(), 30_000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, [sessionId, projectRoot]);

  if (!sessionId || !configured || !session?.active_kind) return null;

  const isMac = session.active_kind === "mac";
  const proj = projectLabel(projectRoot);
  const behind = git?.behind ? git.commits_behind || 0 : 0;
  const ahead = git?.ahead ? git.commits_ahead || 0 : 0;
  const dirty = git?.dirty ? git.dirty_count || 0 : 0;
  // "To ship" = anything not yet on GitHub: uncommitted files first (you commit
  // those), then committed-but-unpushed. One label, routes to the Changes tab
  // where the actual commit/push lives.
  const hasShip = dirty > 0 || ahead > 0;
  const shipLabel = dirty > 0 ? `${dirty} to commit` : `${ahead} to push`;
  const openChanges = () => {
    if (typeof window !== "undefined") {
      window.dispatchEvent(new CustomEvent("workspace:open-changes"));
    }
  };

  const onPull = async () => {
    setPulling(true);
    setPullError(null);
    try {
      const res = await pullBridgeWorkspace(projectRoot);
      if (res?.ok && res.status) setGit(res.status);
      else setPullError(res?.error || "pull failed");
    } finally {
      setPulling(false);
    }
  };

  return (
    <div
      className={cn(
        "flex shrink-0 items-center gap-1.5 border-b px-3 py-1.5 text-[11px]",
        isMac ? "bg-success/5" : "bg-info/5",
      )}
    >
      <span
        className={cn("inline-block size-2 shrink-0 rounded-full", isMac ? "bg-success" : "bg-info")}
        aria-hidden
      />
      <span className={cn("shrink-0 font-mono font-medium", isMac ? "text-success" : "text-info")}>
        {isMac ? "Mac" : "Cloud"}
      </span>

      {proj && (
        <>
          <Dot />
          <span className="min-w-0 truncate font-mono text-foreground/80">{proj}</span>
        </>
      )}

      {/* Sync state - cloud projects only. Behind and to-ship can co-exist
          (pull down, then commit/push up), so each shows independently. */}
      {git && behind > 0 && (
        <>
          <Dot />
          <span className="inline-flex shrink-0 items-center gap-0.5 text-warning">
            <ArrowDown className="size-3" aria-hidden />
            {behind} behind
          </span>
          {pullError ? (
            <span className="truncate text-danger">{pullError}</span>
          ) : (
            <button
              type="button"
              onClick={() => void onPull()}
              disabled={pulling}
              title="Pull latest (git pull --ff-only)"
              aria-label="Pull latest into this project"
              className="inline-flex size-5 shrink-0 items-center justify-center rounded text-warning transition-colors hover:bg-warning/20"
            >
              <RefreshCw className={cn("size-3", pulling && "animate-spin")} aria-hidden />
            </button>
          )}
        </>
      )}
      {git && hasShip && (
        <>
          <Dot />
          <button
            type="button"
            onClick={openChanges}
            title="Open Changes to commit & push to GitHub"
            className="inline-flex shrink-0 items-center gap-0.5 text-info transition-colors hover:text-info/80 hover:underline"
          >
            <ArrowUp className="size-3" aria-hidden />
            {shipLabel}
          </button>
        </>
      )}
      {git && behind === 0 && !hasShip && (
        <>
          <Dot />
          <span className="inline-flex shrink-0 items-center gap-0.5 text-muted-foreground">
            <Check className="size-3" aria-hidden />
            synced
          </span>
        </>
      )}
    </div>
  );
}

function Dot() {
  return <span className="shrink-0 text-muted-foreground/40">·</span>;
}

// projectLabel turns the active root into a short, meaningful name. The bare
// volume root isn't a project; the self-repo is Jarvis's own code.
function projectLabel(root: string): string {
  if (!root) return "";
  const seg = root.split("/").filter(Boolean).pop() || "";
  if (seg === "workspace" || seg === "") return "";
  if (seg === "infinity") return "infinity · Jarvis";
  return seg;
}
