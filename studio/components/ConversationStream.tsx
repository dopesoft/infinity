"use client";

import { useEffect, useRef, useState } from "react";
import { ArrowDown, Sparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ChatBubble } from "@/components/ChatBubble";
import { ToolCallCard } from "@/components/ToolCallCard";
import { AgentTeamCard } from "@/components/AgentTeamCard";
import { ThinkingBlock } from "@/components/ThinkingBlock";
import { SkillProposalCard } from "@/components/SkillProposalCard";
import { DashboardContextCard } from "@/components/DashboardContextCard";
import { WorkingIndicator } from "@/components/WorkingIndicator";
import type { ChatMessage } from "@/hooks/useChat";

const SKILL_TOOL_NAMES = new Set(["skill_propose", "skill_optimize"]);

// workingState decides whether to show the persistent "working" row and what
// it should say. The row is the consistent activity cue for the gaps the
// per-message affordances don't cover (after a tool result, while the model
// reasons before the next step). It is suppressed when a live affordance is
// already on screen: a pending thinking block (owns its own shimmer) or a
// tool call parked awaiting the boss's approval (the agent is blocked, not
// working). Otherwise the label reflects the current step.
function workingState(
  messages: ChatMessage[],
  working: boolean,
): { show: boolean; label: string } {
  if (!working) return { show: false, label: "" };
  const last = messages[messages.length - 1];
  if (last) {
    if (last.role === "thinking" && last.pending) return { show: false, label: "" };
    if (last.role === "tool" && last.pending) {
      if (last.toolCall?.awaiting_approval) return { show: false, label: "" };
      const name = last.toolCall?.name?.trim();
      return { show: true, label: name ? `Running ${name}…` : "Running tool…" };
    }
    if (last.role === "assistant" && last.pending) {
      return { show: true, label: "Responding…" };
    }
  }
  return { show: true, label: "Working…" };
}

export function ConversationStream({
  messages,
  // onQuickReply sends a canned message into the current session. Used by
  // the "Approve & fix" action on heartbeat finding cards - it routes
  // through chat.send so the agent acts in this same conversation.
  onQuickReply,
  // working is the turn-in-flight flag (chat.isStreaming). Drives the
  // persistent WorkingIndicator so the boss always has a "still going" cue.
  working = false,
}: {
  messages: ChatMessage[];
  onQuickReply?: (text: string) => void;
  working?: boolean;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [showJump, setShowJump] = useState(false);
  const stickToBottomRef = useRef(true);
  const work = workingState(messages, working);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    if (stickToBottomRef.current) {
      el.scrollTop = el.scrollHeight;
    }
    // Re-pin when the working row toggles/changes too, so the indicator
    // doesn't appear just below the fold.
  }, [messages, work.show, work.label]);

  function onScroll() {
    const el = scrollRef.current;
    if (!el) return;
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    const stick = distanceFromBottom < 80;
    stickToBottomRef.current = stick;
    setShowJump(!stick);
  }

  function jumpToBottom() {
    const el = scrollRef.current;
    if (!el) return;
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
    stickToBottomRef.current = true;
    setShowJump(false);
  }

  if (messages.length === 0) {
    return (
      // The stream area excludes the composer, while Files / Preview
      // do not have a bottom composer taking space. Nudge the desktop
      // empty state down so the placeholders line up across columns.
      <div
        ref={scrollRef}
        className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center lg:translate-y-[6.5rem]"
      >
        <span className="inline-flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
          <Sparkles className="size-5" aria-hidden />
        </span>
        <div className="max-w-md space-y-1">
          <h3 className="text-sm font-semibold">A fresh session</h3>
          <p className="text-xs leading-relaxed text-muted-foreground">
            Tell Jarvis what to build, write, or think through.
          </p>
        </div>
      </div>
    );
  }

  return (
    // min-w-0 on the flex column AND on the inner scroller is what stops
    // a long un-wrapping line (e.g. a voice transcript without natural
    // word breaks at the right place) from pushing the column wider than
    // its parent. Without it, the message bubbles' max-w-[%] computes
    // against an inflated parent and the bubble overflows the viewport.
    <div className="relative flex min-h-0 min-w-0 flex-1 flex-col">
      <div
        ref={scrollRef}
        onScroll={onScroll}
        // pb-8 gives the last message (often an error or thinking bubble) clear
        // breathing room before the composer - otherwise auto-scroll
        // pins it flush against the prompt input with zero padding.
        //
        // overflow-x-hidden is load-bearing, not cosmetic: a scroll region
        // declared `overflow-y-auto` with no explicit overflow-x computes
        // overflow-x to `auto` (CSS spec: a non-visible value on one axis
        // promotes `visible` on the other to `auto`). That silently makes
        // THIS element the chat's one horizontally-scrollable surface, so a
        // long URL/path/token whose min-content width briefly exceeds the
        // column - common mid-stream before a break opportunity lands -
        // scrolls the whole conversation sideways no matter how hardened the
        // message renderers are. Pinning overflow-x to hidden stops the
        // promotion: y scrolls, x clips. Inner scrollers that legitimately
        // need to pan (code blocks, wide tables) own their own
        // overflow-x-auto inside their own clipped wrappers, so this never
        // steals their internal scroll.
        className="flex-1 min-w-0 space-y-3 overflow-y-auto overflow-x-hidden px-3 pt-3 pb-8 scroll-touch sm:px-4"
      >
        {messages.map((m) => (
          <div key={m.id} className="min-w-0 max-w-full" data-message>
            {m.role === "tool" ? (
              // Skill-pipeline tool calls (skill_propose, skill_optimize)
              // render as a rich proposal card so "new skill proposed" is
              // glanceable. Everything else falls back to the generic
              // tool-call card.
              m.toolCall?.name === "agent_team_start" ? (
                <div className="flex justify-start">
                  <div className="w-full min-w-0 max-w-full sm:max-w-[80%]">
                    <AgentTeamCard message={m} />
                  </div>
                </div>
              ) : SKILL_TOOL_NAMES.has(m.toolCall?.name ?? "") ? (
                <div className="flex justify-start">
                  <div className="w-full min-w-0 max-w-full sm:max-w-[80%]">
                    <SkillProposalCard message={m} />
                  </div>
                </div>
              ) : (
                // Tool/Thinking/Context cards all share the same left-
                // anchored max-w-[80%] as the assistant ChatBubble so the
                // "agent voice" column reads as one consistent rail -
                // earlier slim widths (1/2 / 3/4) made long INPUT/OUTPUT
                // payloads unreadable, especially the indented JSON.
                <div className="flex justify-start">
                  <div className="w-full min-w-0 max-w-full sm:max-w-[80%]">
                    <ToolCallCard message={m} />
                  </div>
                </div>
              )
            ) : m.role === "thinking" ? (
              <div className="flex justify-start">
                <div className="w-full min-w-0 max-w-full sm:max-w-[80%]">
                  <ThinkingBlock message={m} />
                </div>
              </div>
            ) : m.seeded ? (
              // Discuss-with-Jarvis context block - a left-anchored card
              // (same rhythm as the thinking / skill cards) so it reads as
              // "something the boss brought in", not a typed user message.
              <div className="flex justify-start">
                <div className="w-full min-w-0 max-w-full sm:max-w-[80%]">
                  <DashboardContextCard message={m} onQuickReply={onQuickReply} />
                </div>
              </div>
            ) : (
              <ChatBubble message={m} onQuickReply={onQuickReply} />
            )}
          </div>
        ))}
        {work.show && (
          <div className="min-w-0 max-w-full" data-message>
            <div className="w-full min-w-0 max-w-full sm:max-w-[80%]">
              <WorkingIndicator label={work.label} />
            </div>
          </div>
        )}
      </div>
      {showJump && (
        <div className="pointer-events-none absolute inset-x-0 bottom-2 flex justify-center">
          <Button
            size="sm"
            variant="secondary"
            className="pointer-events-auto shadow-sm"
            onClick={jumpToBottom}
          >
            <ArrowDown className="size-4" /> Jump to latest
          </Button>
        </div>
      )}
    </div>
  );
}
