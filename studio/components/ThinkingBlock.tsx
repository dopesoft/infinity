"use client";

import { useEffect, useRef, useState } from "react";
import {
  Brain,
  ChevronDown,
  ChevronRight,
} from "lucide-react";
import { cn } from "@/lib/utils";
import type { ChatMessage } from "@/hooks/useChat";

function formatElapsed(ms: number) {
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

/**
 * ThinkingBlock — shown above an assistant turn while Jarvis is preparing a
 * response. Renders the live "extended thinking" stream (when available) with
 * an auto-scroll preview, a shimmering "Jarvis is thinking" header, and an
 * elapsed timer. After the agent moves on it stays as a collapsed "Thought
 * for Xs" pill that the user can re-expand to inspect the trace.
 *
 * If the message ends with no thinking content (e.g. extended thinking is
 * disabled on the provider) the block hides itself entirely so the stream
 * doesn't pile up empty cards.
 */
export function ThinkingBlock({ message }: { message: ChatMessage }) {
  const isPending = !!message.pending;
  const hasContent = message.text.trim().length > 0;

  const [open, setOpen] = useState<boolean>(isPending);
  const [now, setNow] = useState<number>(() => Date.now());
  const previewRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!isPending) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [isPending]);

  // Auto-collapse the moment thinking completes (matches ToolCallCard UX).
  // User can re-open to inspect the full trace.
  useEffect(() => {
    if (!isPending) setOpen(false);
  }, [isPending]);

  // While streaming, keep the preview pinned to the bottom so the latest
  // thinking content is always visible.
  useEffect(() => {
    if (!isPending || !open) return;
    const el = previewRef.current;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
  }, [isPending, open, message.text]);

  // Hide collapsed-and-empty blocks: nothing useful to surface. Placed AFTER
  // hooks so hook order stays stable across renders.
  if (!isPending && !hasContent) return null;

  const elapsedMs = isPending
    ? now - message.createdAt
    : (message.endedAt ?? now) - message.createdAt;

  return (
    <div
      className={cn(
        "rounded-xl border bg-card text-card-foreground transition-colors",
        isPending && "border-info/40",
      )}
    >
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left"
        aria-expanded={open}
      >
        <Brain
          className={cn(
            "size-4 shrink-0",
            isPending ? "text-info animate-pulse" : "text-muted-foreground",
          )}
          aria-hidden
        />
        <span
          className={cn(
            "whitespace-nowrap text-xs sm:text-sm",
            isPending ? "thinking-shimmer" : "text-muted-foreground",
          )}
        >
          {isPending ? "Jarvis is thinking" : "Thought"}
        </span>
        <span className="ml-auto flex items-center gap-2 text-[11px] text-muted-foreground">
          <span suppressHydrationWarning>{formatElapsed(elapsedMs)}</span>
          {hasContent ? (
            open ? (
              <ChevronDown className="size-4" aria-hidden />
            ) : (
              <ChevronRight className="size-4" aria-hidden />
            )
          ) : null}
        </span>
      </button>

      {open && hasContent && (
        <div className="border-t px-3 py-2">
          <div className="relative">
            <div
              ref={previewRef}
              className={cn(
                "max-h-40 overflow-y-auto whitespace-pre-wrap break-words font-mono text-[11px] leading-snug text-muted-foreground scroll-touch sm:text-xs",
                isPending && "thinking-fade-mask",
              )}
            >
              {message.text}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
