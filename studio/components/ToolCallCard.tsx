"use client";

import { useState } from "react";
import {
  IconCheck,
  IconChevronDown,
  IconChevronRight,
  IconLoader2,
  IconTool,
  IconX,
} from "@tabler/icons-react";
import { cn } from "@/lib/utils";
import type { ChatMessage } from "@/hooks/useChat";

function formatMs(start?: string, end?: string) {
  if (!start) return "";
  const s = new Date(start).getTime();
  const e = end ? new Date(end).getTime() : Date.now();
  const ms = Math.max(0, e - s);
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

export function ToolCallCard({ message }: { message: ChatMessage }) {
  const call = message.toolCall;
  const result = message.toolResult;
  const status: "running" | "success" | "error" = result
    ? result.is_error
      ? "error"
      : "success"
    : "running";

  // Default collapsed on completion (matches PDF spec). Open while running.
  const [open, setOpen] = useState<boolean>(status === "running");

  if (!call) return null;

  return (
    <div className="rounded-xl border bg-card text-card-foreground">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left"
        aria-expanded={open}
      >
        <IconTool className="size-4 shrink-0 text-muted-foreground" aria-hidden />
        <span className="truncate font-mono text-xs sm:text-sm">{call.name}</span>
        <StatusIcon status={status} />
        <span className="ml-auto flex items-center gap-2 text-[11px] text-muted-foreground">
          <span>{formatMs(call.started_at, result?.ended_at)}</span>
          {open ? (
            <IconChevronDown className="size-4" aria-hidden />
          ) : (
            <IconChevronRight className="size-4" aria-hidden />
          )}
        </span>
      </button>

      {open && (
        <div className="space-y-2 border-t px-3 py-2">
          <Section title="Input">
            <pre className="overflow-x-auto whitespace-pre rounded-md bg-muted p-2 font-mono text-[11px] leading-snug scroll-touch sm:text-xs">
              {JSON.stringify(call.input ?? {}, null, 2)}
            </pre>
          </Section>
          {result && (
            <Section title={result.is_error ? "Error" : "Output"}>
              <pre
                className={cn(
                  "max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-md p-2 font-mono text-[11px] leading-snug scroll-touch sm:text-xs",
                  result.is_error ? "bg-danger/10 text-danger" : "bg-muted",
                )}
              >
                {result.output ?? ""}
              </pre>
            </Section>
          )}
        </div>
      )}
    </div>
  );
}

function StatusIcon({ status }: { status: "running" | "success" | "error" }) {
  if (status === "running") return <IconLoader2 className="size-4 animate-spin text-info" aria-hidden />;
  if (status === "success") return <IconCheck className="size-4 text-success" aria-hidden />;
  return <IconX className="size-4 text-danger" aria-hidden />;
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
        {title}
      </div>
      {children}
    </div>
  );
}
