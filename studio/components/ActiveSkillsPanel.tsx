"use client";

import { useMemo } from "react";
import { Loader2 } from "lucide-react";
import { SidePanelCard } from "@/components/SidePanelCard";
import { ListRow } from "@/components/ui/list-row";
import type { ChatMessage } from "@/hooks/useChat";

function formatMs(ms?: number) {
  if (!ms && ms !== 0) return "";
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

function durationFor(m: ChatMessage): number | undefined {
  const start = m.toolCall?.started_at ? new Date(m.toolCall.started_at).getTime() : m.createdAt;
  const end = m.toolResult?.ended_at
    ? new Date(m.toolResult.ended_at).getTime()
    : m.pending
      ? Date.now()
      : undefined;
  if (!end) return undefined;
  return Math.max(0, end - start);
}

/**
 * ActiveSkillsPanel — the last few tool calls of the live session.
 *
 * Majordomo §1.4 ("one alive signal") and §5: the tinted status tile per row
 * (green check / red triangle / blue spinner on a filled square) is now a
 * `ListRow` tone dot, so the only thing that draws the eye is whatever is
 * still running. Every other row is grey, which is what makes the running one
 * readable at a glance.
 */
export function ActiveSkillsPanel({ messages }: { messages: ChatMessage[] }) {
  const recent = useMemo(() => {
    return messages
      .filter((m) => m.role === "tool" && m.toolCall)
      .slice(-6)
      .reverse();
  }, [messages]);

  return (
    <SidePanelCard label="Active skills">
      {recent.length === 0 ? (
        <p className="text-[12.5px] text-quiet">No tool calls this session.</p>
      ) : (
        <div className="min-w-0">
          {recent.map((m) => {
            const isError = m.toolResult?.is_error;
            const running = m.pending;
            const dur = durationFor(m);
            return (
              <ListRow
                key={m.id}
                tone={running ? "brand" : isError ? "danger" : "quiet"}
                live={running}
                leading={
                  running ? (
                    <Loader2 className="size-3.5 animate-spin text-brand" aria-hidden />
                  ) : undefined
                }
                title={
                  <span className="font-mono text-[12px]">{m.toolCall?.name ?? "unknown"}</span>
                }
                trailing={
                  <span className="font-mono text-[11px] tabular-nums text-quiet">
                    {running ? "running" : formatMs(dur)}
                  </span>
                }
                chevron={false}
              />
            );
          })}
        </div>
      )}
    </SidePanelCard>
  );
}
