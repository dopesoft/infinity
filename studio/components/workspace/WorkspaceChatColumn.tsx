"use client";

import { useEffect, useRef } from "react";
import { ConversationStream } from "@/components/ConversationStream";
import { CodingSessionBanner } from "@/components/CodingSessionBanner";
import { PromptInputBox } from "@/components/ui/ai-prompt-box";
import { useSessionModel } from "@/lib/use-model";
import type { useChat } from "@/hooks/useChat";

type ChatHook = ReturnType<typeof useChat>;

/**
 * WorkspaceChatColumn — the chat surface in the unified /live workspace.
 *
 * Layout: coding-session banner (one-shot dismissable) at the top, conversation
 * stream filling the middle, AI prompt box pinned to the bottom inside a
 * keyboard-safe container. The prompt box owns its own model state via the
 * <ModelChip> — we wire it through the per-session `useSessionModel` hook so
 * the choice persists across sessions and reloads.
 */
export function WorkspaceChatColumn({
  chat,
  minimalComposer = false,
  scrollRef,
}: {
  chat: ChatHook;
  minimalComposer?: boolean;
  scrollRef?: React.MutableRefObject<HTMLDivElement | null>;
}) {
  const { model, setModel } = useSessionModel(chat.sessionId);
  const localRef = useRef<HTMLDivElement | null>(null);
  const ref = scrollRef ?? localRef;

  // Keep the conversation scrolled to the latest message when new content
  // arrives. ConversationStream renders its own scroller; we just nudge it.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const last = el.querySelector("[data-message]:last-of-type");
    if (last) (last as HTMLElement).scrollIntoView({ block: "end", behavior: "smooth" });
  }, [chat.messages.length, ref]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <CodingSessionBanner sessionId={chat.sessionId} />
      {/* Outer wrapper must be a flex container so ConversationStream's
          empty state (which uses flex-1 to vertically center its content)
          can actually expand. Without flex here, flex-1 has no effect and
          the empty state collapsed to the top edge. */}
      <div ref={ref} className="flex min-h-0 flex-1 flex-col overflow-hidden">
        <ConversationStream messages={chat.messages} />
      </div>
      <div className="shrink-0 border-t bg-background/95 px-3 pt-2 backdrop-blur supports-[backdrop-filter]:bg-background/80 sm:px-4 keyboard-safe-bottom">
        <PromptInputBox
          onSend={(text) => {
            const t = text.trim();
            if (!t) return;
            // `send` itself decides whether to start a new turn or
            // queue a steer based on chat.isStreaming. From the
            // composer's POV, it's still just "send".
            chat.send(t);
          }}
          onSlash={(cmd) => {
            const c = cmd.toLowerCase();
            if (c === "/new") {
              chat.newSession();
              return true;
            }
            if (c === "/clear") {
              chat.clear();
              return true;
            }
            return false;
          }}
          onStop={chat.interrupt}
          isLoading={chat.isStreaming}
          disabled={chat.status !== "connected"}
          placeholder="ask me anything.."
          model={model}
          onModelChange={setModel}
          minimal={minimalComposer}
        />
      </div>
    </div>
  );
}
