"use client";

import { useEffect, useRef, useState } from "react";
import { ArrowDown, Sparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ChatBubble } from "@/components/ChatBubble";
import { ToolCallCard } from "@/components/ToolCallCard";
import { ThinkingBlock } from "@/components/ThinkingBlock";
import { SkillProposalCard } from "@/components/SkillProposalCard";
import type { ChatMessage } from "@/hooks/useChat";

const SKILL_TOOL_NAMES = new Set(["skill_propose", "skill_optimize"]);

export function ConversationStream({ messages }: { messages: ChatMessage[] }) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [showJump, setShowJump] = useState(false);
  const stickToBottomRef = useRef(true);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    if (stickToBottomRef.current) {
      el.scrollTop = el.scrollHeight;
    }
  }, [messages]);

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
      // Shared empty-state shape across the workspace columns: icon in a
      // muted circle, semibold title, soft sub-copy. Centered vertically so
      // it lines up with the Canvas-preview empty state in the rightmost
      // column.
      // Anchored ~224px from the column top so it visually aligns with the
      // Files / Preview empty states next door. Those columns sit ~84px
      // below their column edge (tabs + filter / tabs + URL bar); the chat
      // column has no header above it, so we fake the same offset here.
      <div
        ref={scrollRef}
        className="flex h-full flex-col items-center justify-start gap-3 p-6 pt-56 text-center"
      >
        <span className="inline-flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
          <Sparkles className="size-5" aria-hidden />
        </span>
        <div className="max-w-md space-y-1">
          <h3 className="text-sm font-semibold">A fresh session</h3>
          <p className="text-xs leading-relaxed text-muted-foreground">
            Tell Infinity what you want to build, write, or talk through. An app,
            a document, a spreadsheet, or just a thought. Anything material shows
            up in the panels to the right.
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="relative flex flex-1 min-h-0 flex-col">
      <div
        ref={scrollRef}
        onScroll={onScroll}
        className="flex-1 space-y-3 overflow-y-auto px-3 py-3 scroll-touch sm:px-4"
      >
        {messages.map((m) => (
          <div key={m.id}>
            {m.role === "tool" ? (
              // Skill-pipeline tool calls (skill_propose, skill_optimize)
              // render as a rich proposal card so "new skill proposed" is
              // glanceable. Everything else falls back to the generic
              // tool-call card.
              SKILL_TOOL_NAMES.has(m.toolCall?.name ?? "") ? (
                <div className="flex justify-start">
                  <div className="w-full sm:w-3/4">
                    <SkillProposalCard message={m} />
                  </div>
                </div>
              ) : (
                // ToolCallCard wraps to a left-anchored 50% so it doesn't pretend
                // to be the conversation. Same width as ThinkingBlock for visual
                // rhythm — both are "system" feedback, not chat.
                <div className="flex justify-start">
                  <div className="w-full sm:w-1/2">
                    <ToolCallCard message={m} />
                  </div>
                </div>
              )
            ) : m.role === "thinking" ? (
              <div className="flex justify-start">
                <div className="w-full sm:w-3/4">
                  <ThinkingBlock message={m} />
                </div>
              </div>
            ) : (
              <ChatBubble message={m} />
            )}
          </div>
        ))}
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
