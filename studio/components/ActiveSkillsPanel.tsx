"use client";

import { useMemo } from "react";
import { IconLoader2, IconCheck, IconAlertTriangle } from "@tabler/icons-react";
import { cn } from "@/lib/utils";
import { SidePanelCard } from "@/components/SidePanelCard";
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
        <p className="text-xs text-muted-foreground">No tool calls this session.</p>
      ) : (
        <ul className="space-y-1.5">
          {recent.map((m) => {
            const isError = m.toolResult?.is_error;
            const running = m.pending;
            const dur = durationFor(m);
            return (
              <li key={m.id} className="flex items-center gap-2">
                <span
                  className={cn(
                    "flex size-5 shrink-0 items-center justify-center rounded",
                    running
                      ? "bg-info/15 text-info"
                      : isError
                        ? "bg-destructive/15 text-destructive"
                        : "bg-success/15 text-success",
                  )}
                >
                  {running ? (
                    <IconLoader2 className="size-3 animate-spin" aria-hidden />
                  ) : isError ? (
                    <IconAlertTriangle className="size-3" aria-hidden />
                  ) : (
                    <IconCheck className="size-3" aria-hidden />
                  )}
                </span>
                <span className="truncate font-mono text-[12px]">
                  {m.toolCall?.name ?? "unknown"}
                </span>
                <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">
                  {running ? "running" : formatMs(dur)}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </SidePanelCard>
  );
}
