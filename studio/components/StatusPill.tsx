import { Chip, ChipGroup, type ChipTone } from "@/components/ui/chip";
import { cn } from "@/lib/utils";

export type AgentState =
  | "awake"
  | "listening"
  | "thinking"
  | "idle"
  | "offline";

/* One row per state: the label, the optional qualifier, the tone the dot
 * carries, how the dot moves, and whether the label shouts too.
 *
 * `loud` is deliberately reserved for the two states that want the boss:
 * Jarvis is working (thinking) or the socket is gone (offline). Awake and
 * idle stay in ink so those two mean something when they appear. */
const stateConfig: Record<
  AgentState,
  {
    label: string;
    sub?: string;
    tone: ChipTone;
    dot: true | "pulse" | "ping";
    loud?: boolean;
  }
> = {
  awake: { label: "Awake", tone: "success", dot: true },
  listening: { label: "Awake", sub: "Listening", tone: "success", dot: "pulse" },
  thinking: { label: "Thinking", tone: "info", dot: "ping", loud: true },
  idle: { label: "Idle", tone: "neutral", dot: true },
  offline: { label: "Offline", tone: "danger", dot: true, loud: true },
};

/**
 * StatusPill - is Jarvis awake, working, or gone.
 *
 * Reports state, it does not take a click, so the chip is non-interactive.
 * Thinking stays lit for the ENTIRE turn (thinking, tool calls, streaming)
 * because chat.isStreaming drives it, not the local ThinkingBlock: a glance
 * at the header has to answer "is he still going".
 */
export function StatusPill({
  state = "idle",
  className,
}: {
  state?: AgentState;
  className?: string;
}) {
  const cfg = stateConfig[state];
  return (
    <ChipGroup className={className}>
      <Chip
        interactive={false}
        raised
        tone={cfg.tone}
        loud={cfg.loud}
        dot={cfg.dot}
        aria-live="polite"
      >
        <span className={cn("inline-flex items-center gap-1")}>
          {cfg.label}
          {cfg.sub ? (
            <>
              <span className="opacity-40" aria-hidden>·</span>
              <span className="opacity-75">{cfg.sub}</span>
            </>
          ) : null}
        </span>
      </Chip>
    </ChipGroup>
  );
}
