"use client";

import { useEffect, useState } from "react";
import { Bot, Check, CornerDownRight, Copy, Sparkles, ThumbsDown, ThumbsUp, Undo2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { submitMessageFeedback } from "@/lib/api";
import type { ChatMessage } from "@/hooks/useChat";
import { Markdown } from "@/components/chat/Markdown";
import { Citations } from "@/components/chat/Citations";
import { FindingActions } from "@/components/FindingActions";

function formatTime(ms: number, now: number): string {
  if (!ms) return "";
  const d = new Date(ms);
  const time = d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  if (!now) return time;
  const diff = now - ms;
  const SEC = 1_000;
  const MIN = 60 * SEC;
  if (diff < 45 * SEC) return "just now";
  if (diff < 60 * MIN) return `${Math.floor(diff / MIN)}m ago`;
  if (diff < 24 * 60 * MIN) return time;
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

const FEEDBACK_KEY = "infinity:messages:feedback";

type FeedbackMap = Record<string, "up" | "down">;

function readFeedback(): FeedbackMap {
  if (typeof window === "undefined") return {};
  try {
    const raw = window.localStorage.getItem(FEEDBACK_KEY);
    return raw ? (JSON.parse(raw) as FeedbackMap) : {};
  } catch {
    return {};
  }
}

function writeFeedback(map: FeedbackMap) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(FEEDBACK_KEY, JSON.stringify(map));
  } catch {
    /* ignore */
  }
}

export function ChatBubble({
  message,
  // onQuickReply sends a canned message into the current session - wired
  // to the "Approve & fix" action on proactive heartbeat findings.
  onQuickReply,
}: {
  message: ChatMessage;
  onQuickReply?: (text: string) => void;
}) {
  // All hooks must run on every render, so early returns live below them.
  // The "tool"/"thinking" branch renders nothing but still pays for hook
  // setup - that's the cost of holding the rules-of-hooks invariant.
  const [copied, setCopied] = useState(false);
  const [vote, setVote] = useState<"up" | "down" | undefined>(undefined);
  const [now, setNow] = useState(0);

  // Hydrate vote from localStorage after mount (SSR-safe).
  useEffect(() => {
    setVote(readFeedback()[message.id]);
  }, [message.id]);

  // Tick "now" once on mount + every 30s so the relative timestamp updates
  // without thrashing renders.
  useEffect(() => {
    setNow(Date.now());
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);

  if (message.role === "tool" || message.role === "thinking") return null;
  const isUser = message.role === "user";

  if (message.error) {
    return (
      <div className="rounded-md border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-danger">
        {message.error}
      </div>
    );
  }

  function doCopy() {
    if (!message.text) return;
    void navigator.clipboard.writeText(message.text).then(
      () => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1200);
      },
      () => undefined,
    );
  }

  function setRating(next: "up" | "down") {
    const map = readFeedback();
    const current = map[message.id];
    if (current === next) {
      // Toggle off.
      delete map[next === "up" ? message.id : message.id];
      delete map[message.id];
      setVote(undefined);
      writeFeedback(map);
      void submitMessageFeedback(message.id, null);
      return;
    }
    map[message.id] = next;
    writeFeedback(map);
    setVote(next);
    void submitMessageFeedback(message.id, next);
  }

  return (
    // group → child action row uses group-hover to fade in. On touch
    // devices (no hover) the row stays visible because the arbitrary
    // [@media(hover:none)] variant pins opacity to 100.
    <div
      className={cn(
        "group flex w-full min-w-0 max-w-full flex-col gap-1",
        isUser ? "items-end" : "items-start",
      )}
    >
      {isUser ? (
        // User messages render as plain right-aligned text (no bubble) so
        // the eye reads them as "input"; the agent's responses get the
        // visible surface treatment. Steered messages get a small left
        // accent + label so the transcript shows that they were typed
        // mid-turn (and routed through the steer channel, not as a fresh
        // top-of-turn prompt).
        // No-bubble user message: text reads naturally left-to-right but
        // the whole block hugs the column's right edge. max-w-[85%] gives
        // long messages more room to breathe than the old 78% (so they
        // don't wrap prematurely) while keeping the wrap point tight
        // enough that the left edge of multi-line text doesn't drift far
        // off-axis from where the eye expects "user content" to live.
        <div className="min-w-0 max-w-[85%] sm:max-w-[80%]">
          {message.steered && (
            <div className="flex items-center justify-end gap-1 pb-0.5 text-[10px] uppercase tracking-wide text-info">
              <CornerDownRight className="size-3" />
              <span>steered mid-turn</span>
            </div>
          )}
          <div
            className={cn(
              "min-w-0 max-w-full whitespace-pre-wrap break-words [overflow-wrap:anywhere] text-sm leading-relaxed text-foreground",
              message.steered && "border-r-2 border-info/60 pr-2",
            )}
          >
            {message.text}
            {message.pending && (
              <span className="ml-0.5 inline-block size-2 animate-pulse rounded-full bg-current align-middle opacity-60" />
            )}
          </div>
        </div>
      ) : (
        // Agent row - pulsing dot OUTSIDE the bubble (left edge), bubble
        // on the right. The dot signals "Jarvis is still working on this
        // reply" even after the ThinkingBlock has collapsed, without
        // crowding the bubble's text content. items-start so the dot
        // anchors to the top of the bubble; a 12px top offset lines it up
        // with the first line of body text rather than the rounded corner.
        <div className="flex w-full min-w-0 max-w-full items-start gap-2">
          {message.pending && (
            <span
              className="relative mt-3 inline-flex size-2.5 shrink-0"
              aria-hidden
            >
              <span className="absolute inset-0 inline-flex animate-ping rounded-full bg-info opacity-60" />
              <span className="relative inline-flex size-2.5 rounded-full bg-info" />
            </span>
          )}
          {/* Agent bubble - a touch darker than the surrounding muted
              column bg so it reads as a distinct surface in both modes.
              Proactive bubbles (heartbeat-initiated) get a subtle accent
              border + origin badge so the boss can tell the agent spoke
              first. */}
          <div
            className={cn(
              "min-w-0 max-w-full rounded-2xl rounded-tl-sm bg-zinc-200/80 px-3 py-2 text-sm leading-relaxed text-foreground sm:max-w-[80%] dark:bg-zinc-800/80",
              message.proactive && "border border-info/40",
              message.proactiveKind === "skill_promoted" &&
                "border-success/40 bg-success/5 dark:bg-success/10",
            )}
          >
            {message.proactive && message.proactiveKind === "skill_promoted" && (
              <div className="mb-1 flex items-center gap-1 text-[10px] uppercase tracking-wide text-success">
                <Bot className="size-3" />
                <span>skill learned</span>
              </div>
            )}
            {message.proactive && message.proactiveKind !== "skill_promoted" && (
              <div className="mb-1 flex items-center gap-1 text-[10px] uppercase tracking-wide text-info">
                <Sparkles className="size-3" />
                <span>
                  {message.proactiveKind
                    ? `heartbeat · ${message.proactiveKind}`
                    : "heartbeat"}
                </span>
              </div>
            )}
            <div className="min-w-0 max-w-full">
              {message.text ? <Markdown text={message.text} /> : null}
            </div>
            {message.interrupted && (
              <div className="mt-1 flex items-center gap-1 text-[10px] uppercase tracking-wide text-danger/80">
                <Undo2 className="size-3" />
                <span>{message.text ? "interrupted" : "stopped before reply"}</span>
              </div>
            )}
            {/* Proactive curiosity findings get the one-tap "Approve & fix"
                row - it tells the agent to act on the finding right here in
                this conversation and confirm what it changed. */}
            {message.proactive && message.curiosityId && (
              <FindingActions
                curiosityId={message.curiosityId}
                onSend={onQuickReply}
              />
            )}
          </div>
        </div>
      )}

      {/* Sources strip - extracted URLs as favicon pills. Lives outside
          the bubble so it visually attributes to the assistant message
          but doesn't crowd the message body. Hidden while pending so it
          doesn't flicker as the agent streams. */}
      {!isUser && !message.pending && message.text && (
        <Citations text={message.text} />
      )}

      {/* Metadata row - timestamp stays visible all the time. Action icons
          (thumbs, copy) live in a child container that fades in on hover
          (and is always visible on touch devices via hover:none media). */}
      {!message.pending && (
        <div
          className={cn(
            "flex w-full items-center gap-1 text-[11px] text-muted-foreground",
            // Right-flush on user rows so the timestamp lines up with the
            // user's message text (which sits at the column's right edge
            // because there's no bubble). Agent row keeps its small left
            // inset since the bubble already provides visual padding.
            isUser ? "justify-end pl-1 pr-0" : "justify-start pl-1 pr-1",
          )}
        >
          <span suppressHydrationWarning>{formatTime(message.createdAt, now)}</span>
          <div
            className={cn(
              "ml-1 flex items-center gap-0.5",
              "opacity-0 transition-opacity duration-150 group-hover:opacity-100 focus-within:opacity-100",
              "[@media(hover:none)]:opacity-100",
            )}
          >
            {!isUser && (
              <>
                <button
                  type="button"
                  onClick={() => setRating("up")}
                  aria-label="Mark as helpful"
                  aria-pressed={vote === "up"}
                  title="Helpful"
                  className={cn(
                    "inline-flex size-6 items-center justify-center rounded transition-colors hover:bg-accent",
                    vote === "up" ? "text-success" : "hover:text-foreground",
                  )}
                >
                  <ThumbsUp className="size-3" />
                </button>
                <button
                  type="button"
                  onClick={() => setRating("down")}
                  aria-label="Mark as unhelpful"
                  aria-pressed={vote === "down"}
                  title="Not helpful"
                  className={cn(
                    "inline-flex size-6 items-center justify-center rounded transition-colors hover:bg-accent",
                    vote === "down" ? "text-danger" : "hover:text-foreground",
                  )}
                >
                  <ThumbsDown className="size-3" />
                </button>
              </>
            )}
            <button
              type="button"
              onClick={doCopy}
              aria-label={copied ? "Copied" : "Copy"}
              title={copied ? "Copied" : "Copy"}
              className="inline-flex size-6 items-center justify-center rounded transition-colors hover:bg-accent hover:text-foreground"
            >
              {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
