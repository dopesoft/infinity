import { cn } from "@/lib/utils";
import type { TraceStatus } from "@/lib/api";

/* TurnStatusPip - small reusable status indicator for a /logs row.
 *
 * Colors are deliberately desaturated so a list of 50+ rows doesn't read as
 * Christmas lights. Green = ok, amber = empty (boss-visible class of bug),
 * red = errored, gray = interrupted (boss-cancelled, not a failure), info =
 * in_flight so the eye picks up "this is still running."
 */
const STYLES: Record<TraceStatus, { dot: string; label: string }> = {
  ok: { dot: "bg-success", label: "ok" },
  empty: { dot: "bg-warning", label: "empty" },
  errored: { dot: "bg-danger", label: "error" },
  interrupted: { dot: "bg-muted-foreground", label: "stopped" },
  in_flight: { dot: "bg-info animate-pulse", label: "running" },
};

export function TurnStatusPip({
  status,
  showLabel = false,
  className,
}: {
  status: TraceStatus;
  showLabel?: boolean;
  className?: string;
}) {
  const style = STYLES[status] ?? STYLES.ok;
  return (
    <span className={cn("inline-flex items-center gap-1.5", className)}>
      <span className={cn("inline-block size-2 shrink-0 rounded-full", style.dot)} aria-hidden />
      {showLabel && (
        <span className="text-[11px] uppercase tracking-wide text-muted-foreground">
          {style.label}
        </span>
      )}
    </span>
  );
}
