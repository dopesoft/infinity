import { cn } from "@/lib/utils";

export type AgentState = "awake" | "thinking" | "idle";

const stateConfig: Record<AgentState, { label: string; dot: string; bg: string; fg: string }> = {
  awake: {
    label: "Awake",
    dot: "bg-success",
    bg: "bg-success/10",
    fg: "text-success",
  },
  thinking: {
    label: "Thinking",
    dot: "bg-info animate-pulse",
    bg: "bg-info/10",
    fg: "text-info",
  },
  idle: {
    label: "Idle",
    dot: "bg-muted-foreground",
    bg: "bg-muted",
    fg: "text-muted-foreground",
  },
};

export function StatusPill({ state = "idle", className }: { state?: AgentState; className?: string }) {
  const cfg = stateConfig[state];
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium",
        cfg.bg,
        cfg.fg,
        className,
      )}
    >
      <span className={cn("size-1.5 rounded-full", cfg.dot)} />
      {cfg.label}
    </span>
  );
}
