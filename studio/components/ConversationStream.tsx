"use client";

import { useEffect, useRef, useState } from "react";
import { ArrowDown, MessageSquareOff } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ChatBubble } from "@/components/ChatBubble";
import { ToolCallCard } from "@/components/ToolCallCard";
import { ThinkingBlock } from "@/components/ThinkingBlock";
import type { ChatMessage } from "@/hooks/useChat";

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
      <div
        ref={scrollRef}
        className="flex flex-1 flex-col items-center justify-center gap-2 px-4 text-center text-muted-foreground"
      >
        <MessageSquareOff className="size-8 opacity-60" aria-hidden />
        <p className="text-sm">Send a message to wake the agent.</p>
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
              // ToolCallCard wraps to a left-anchored 50% so it doesn't pretend
              // to be the conversation. Same width as ThinkingBlock for visual
              // rhythm — both are "system" feedback, not chat.
              <div className="flex justify-start">
                <div className="w-full sm:w-1/2">
                  <ToolCallCard message={m} />
                </div>
              </div>
            ) : m.role === "thinking" ? (
              <div className="flex justify-start">
                <div className="w-full sm:w-1/2">
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
