"use client";

import { useEffect, useRef, useState } from "react";
import { IconArrowDown, IconMessageOff } from "@tabler/icons-react";
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
        <IconMessageOff className="size-8 opacity-60" aria-hidden />
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
              <ToolCallCard message={m} />
            ) : m.role === "thinking" ? (
              <ThinkingBlock message={m} />
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
            <IconArrowDown className="size-4" /> Jump to latest
          </Button>
        </div>
      )}
    </div>
  );
}
