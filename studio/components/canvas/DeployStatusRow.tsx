"use client";

import { useEffect, useState } from "react";
import { Check, Loader2, RefreshCw } from "lucide-react";
import { fetchDeployStatus, type DeployStatus } from "@/lib/canvas/api";
import { cn } from "@/lib/utils";

/**
 * DeployStatusRow — slim banner inside the Canvas Files column that
 * surfaces "Jarvis is behind a deploy."
 *
 * Compares Railway's deployed commit (RAILWAY_GIT_COMMIT_SHA, baked at
 * deploy time) to GitHub main HEAD via Core's /api/deploy/status. Polls
 * every 30s — fast enough that you watch the "behind by N → caught up"
 * transition happen after `git push`, slow enough not to burn GitHub
 * rate limit. Hides when up-to-date so the column stays clean.
 */
export function DeployStatusRow() {
  const [status, setStatus] = useState<DeployStatus | null>(null);
  const [refreshing, setRefreshing] = useState(false);

  useEffect(() => {
    let alive = true;
    const tick = async () => {
      const next = await fetchDeployStatus();
      if (!alive) return;
      setStatus(next);
    };
    void tick();
    const id = setInterval(() => void tick(), 30_000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  // Don't render anything until we know. The banner is only useful when
  // there's a real signal — silence is fine otherwise.
  if (!status) return null;
  if (!status.running_sha) return null; // not running on Railway / SHA unset

  const onCheckNow = async () => {
    setRefreshing(true);
    try {
      const next = await fetchDeployStatus();
      if (next) setStatus(next);
    } finally {
      setRefreshing(false);
    }
  };

  if (!status.behind) {
    // Subtle up-to-date row — confirms Railway is running the latest main.
    // We used to show the running commit SHA here, but a 7-char hash is
    // meaningless without context — say "Railway" so the boss knows what
    // the green check is confirming.
    return (
      <div className="flex shrink-0 items-center gap-2 border-b bg-success/5 px-3 py-1.5 text-[11px] text-success">
        <Check className="size-3.5" aria-hidden />
        <span>Railway is up to date</span>
      </div>
    );
  }

  return (
    <div className="flex shrink-0 items-center gap-2 border-b border-warning/30 bg-warning/10 px-3 py-1.5 text-[11px] text-warning">
      <Loader2 className="size-3.5 animate-spin" aria-hidden />
      <span className="min-w-0 flex-1 truncate">
        Railway is behind by{" "}
        <span className="font-semibold">
          {status.commits_behind || "≥1"} commit
          {status.commits_behind === 1 ? "" : "s"}
        </span>{" "}
        — waiting for redeploy
      </span>
      <button
        type="button"
        onClick={() => void onCheckNow()}
        disabled={refreshing}
        aria-label="Check deploy status now"
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

