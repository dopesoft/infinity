"use client";

// RunIndicator is the canonical Studio primitive for rendering a server-
// tracked long action's state. It pairs with useRuns() from this same
// folder.
//
// Use it anywhere a button fires a long action: cron "Run now", skill
// "Invoke", heartbeat "Run heartbeat", voyager "Optimize", gym "Extract
// now", any future page that fires a server task. The spinner survives
// navigation, refresh, focus loss, and second-device viewing - that's
// the whole point. NEVER track "is this running?" in component-local
// useState; use this. See CLAUDE.md → "Server-tracked progress".
//
// Two render shapes:
//
//   <RunIndicator kind="cron" targetId={cron.id} label="Run now" onRun={fire} />
//     → renders a complete button: idle Play icon → spinning Loader2
//       while server status='running' → checkmark/error chip afterwards.
//       onRun is called when the user clicks; the button auto-disables
//       while running so the user can't double-fire.
//
//   <RunIndicator kind="skill" targetId={skill.name} mode="badge" />
//     → renders just a status badge (no click handler). Use this in
//       lists/cards where the row already has its own action surface
//       and you just need the live status.

import * as React from "react";
import { Loader2, Play, CheckCircle2, AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { useRuns } from "./useRuns";
import type { RunDTO } from "@/lib/api";

export type RunIndicatorProps = {
  kind: string;
  targetId: string;
  // Visible label on the button (idle state). Defaults to "Run".
  label?: string;
  // Title attribute / tooltip hint for the button.
  title?: string;
  // Click handler that actually fires the server action. The handler is
  // responsible for the POST; this component only tracks the resulting
  // mem_runs row. Returning a Promise is fine - the button stays
  // disabled until the promise settles AND server status leaves
  // 'running'. When the server reports the row before the promise
  // settles (the common case), the spinner is driven by realtime.
  onRun?: () => void | Promise<void>;
  // Render shape. 'button' (default) = full clickable affordance.
  // 'badge' = status pill only, no click. 'inline' = tiny spinner +
  // text inline for use inside row metadata.
  mode?: "button" | "badge" | "inline";
  // Button sizing for mode='button'. Defaults to "sm" so it slots into
  // dense row layouts; pass "default" for full-size CTA placements.
  size?: "sm" | "default" | "lg" | "icon";
  className?: string;
  // Override the running indicator's render. Useful when you want to
  // show a custom progress bar instead of the default spinner+label.
  renderRunning?: (run: RunDTO) => React.ReactNode;
};

export function RunIndicator({
  kind,
  targetId,
  label = "Run",
  title,
  onRun,
  mode = "button",
  size = "sm",
  className,
  renderRunning,
}: RunIndicatorProps) {
  // Filter to "this exact target's runs" so the spinner reflects this
  // row only, not "anything of kind=X anywhere."
  const { latest } = useRuns({ kind, targetId, limit: 5 });
  const [optimisticRunning, setOptimisticRunning] = React.useState(false);

  // Optimistic-running covers the gap between the user click and the
  // first realtime UPDATE landing (typically <1s). Once the server row
  // shows status='running', we hand off to that; once it leaves
  // 'running', we clear the optimistic flag.
  React.useEffect(() => {
    // Either branch clears the optimistic flag - the server has spoken,
    // so optimistic state must yield. Kept as one assignment for clarity.
    if (latest) setOptimisticRunning(false);
  }, [latest?.id, latest?.status]);

  const serverRunning = latest?.status === "running";
  const running = serverRunning || optimisticRunning;

  async function handleClick() {
    if (!onRun || running) return;
    setOptimisticRunning(true);
    try {
      await onRun();
    } catch {
      setOptimisticRunning(false);
    }
  }

  if (mode === "badge") {
    return <StatusBadge run={latest} running={running} className={className} />;
  }

  if (mode === "inline") {
    if (running) {
      return (
        <span className={cn("inline-flex items-center gap-1 text-[11px] text-muted-foreground", className)}>
          <Loader2 className="size-3 animate-spin" />
          {latest?.progress_label || "running…"}
        </span>
      );
    }
    if (latest?.status === "error") {
      return (
        <span className={cn("inline-flex items-center gap-1 text-[11px] text-danger", className)}>
          <AlertTriangle className="size-3" />
          error
        </span>
      );
    }
    if (latest?.status === "ok") {
      return (
        <span className={cn("inline-flex items-center gap-1 text-[11px] text-success", className)}>
          <CheckCircle2 className="size-3" />
          ok
        </span>
      );
    }
    return null;
  }

  // mode === 'button'
  return (
    <div className={cn("flex flex-col items-stretch gap-1.5", className)}>
      <Button
        size={size}
        variant="ghost"
        className={cn("gap-1", size === "sm" && "h-7 px-2 text-[11px]")}
        onClick={() => void handleClick()}
        disabled={running || !onRun}
        title={title}
        aria-label={label}
      >
        {running ? (
          <Loader2 className={iconSize(size) + " animate-spin"} />
        ) : (
          <Play className={iconSize(size)} />
        )}
        {running ? (latest?.progress_label || "running…") : label}
      </Button>
      {!running && latest?.status === "error" && (
        <p className="break-words rounded-md border border-danger/40 bg-danger/5 px-2 py-1 text-[11px] text-danger">
          {latest.error || "error"}
        </p>
      )}
      {!running && latest?.status === "ok" && (
        <p className="rounded-md border border-success/40 bg-success/5 px-2 py-1 text-[11px] text-success">
          {latest.result_summary || "completed"}
        </p>
      )}
      {running && renderRunning && latest ? renderRunning(latest) : null}
    </div>
  );
}

function StatusBadge({
  run,
  running,
  className,
}: {
  run: RunDTO | null;
  running: boolean;
  className?: string;
}) {
  if (running) {
    return (
      <Badge variant="outline" className={cn("gap-1 font-mono", className)}>
        <Loader2 className="size-3 animate-spin" />
        running
      </Badge>
    );
  }
  if (run?.status === "error") {
    return (
      <Badge variant="danger" className={cn("font-mono", className)}>
        error
      </Badge>
    );
  }
  if (run?.status === "ok") {
    return (
      <Badge variant="success" className={cn("font-mono", className)}>
        ok
      </Badge>
    );
  }
  return null;
}

function iconSize(size: RunIndicatorProps["size"]): string {
  switch (size) {
    case "lg":
      return "size-5";
    case "default":
      return "size-4";
    case "icon":
      return "size-4";
    default:
      return "size-3.5";
  }
}
