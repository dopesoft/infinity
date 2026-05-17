import { cn } from "@/lib/utils";

export type AgentState =
  | "awake"
  | "listening"
  | "thinking"
  | "idle"
  | "offline";

const stateConfig: Record<
  AgentState,
  { label: string; sub?: string; dot: string; bg: string; fg: string }
> = {
  awake: {
    label: "Awake",
    dot: "bg-success",
    bg: "bg-success/10",
    fg: "text-success",
  },
  listening: {
    label: "Awake",
    sub: "Listening",
    dot: "bg-success animate-pulse",
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
  offline: {
    label: "Offline",
    dot: "bg-destructive",
    bg: "bg-destructive/10",
    fg: "text-destructive",
  },
};

export function StatusPill({
  state = "idle",
  className,
}: {
  state?: AgentState;
  className?: string;
}) {
  const cfg = stateConfig[state];
  // Thinking gets a louder pulse: a real ping-ring around a solid dot,
  // not just an animate-pulse opacity tick. Stays visible for the ENTIRE
  // turn (thinking → tool calls → streaming) because chat.isStreaming
  // drives the state, not just the local ThinkingBlock - so the boss can
  // glance at the header and know Jarvis is still working.
  const isThinking = state === "thinking";
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium",
        cfg.bg,
        cfg.fg,
        className,
      )}
    >
      {isThinking ? (
        <span className="relative inline-flex size-2 shrink-0" aria-hidden>
          <span className="absolute inset-0 inline-flex animate-ping rounded-full bg-info opacity-70" />
          <span className="relative inline-flex size-2 rounded-full bg-info" />
        </span>
      ) : (
        <span className={cn("size-1.5 rounded-full", cfg.dot)} aria-hidden />
      )}
      <span>{cfg.label}</span>
      {cfg.sub ? (
        <>
          <span className="opacity-50">·</span>
          <span className="opacity-80">{cfg.sub}</span>
        </>
      ) : null}
    </span>
  );
}
