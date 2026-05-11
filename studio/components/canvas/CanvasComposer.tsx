"use client";

import { Hash, Loader2 } from "lucide-react";
import { Composer } from "@/components/Composer";
import { ConversationStream } from "@/components/ConversationStream";
import { Button } from "@/components/ui/button";
import { useCanvasStore } from "@/lib/canvas/store";
import type { useChat } from "@/hooks/useChat";

type ChatHook = ReturnType<typeof useChat>;

/**
 * CanvasComposer — the bottom panel in the Canvas left column.
 *
 * Layout: tiny session-info bar at the top, ConversationStream (scrolling)
 * in the middle, Composer pinned at the bottom. Same flex pattern Live uses
 * — header / stream (flex-1, scrollable) / sticky composer — so the input
 * never floats and there's no whitespace below it.
 */
export function CanvasComposer({ chat }: { chat: ChatHook }) {
  const store = useCanvasStore();
  const shortId = chat.sessionId ? chat.sessionId.slice(0, 8) : "—";

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden bg-background">
      <div className="flex shrink-0 items-center gap-2 border-b px-2 py-1.5 text-[11px] text-muted-foreground">
        <Hash className="size-3 shrink-0" />
        <span className="truncate font-mono" title={chat.sessionId}>
          {shortId}
        </span>
        <span className="size-1 shrink-0 rounded-full bg-muted-foreground/40" />
        <span className="whitespace-nowrap">
          {store.dirtyPaths.size} change{store.dirtyPaths.size === 1 ? "" : "s"}
        </span>
        <span className="ml-auto flex items-center gap-1">
          {chat.isStreaming && <Loader2 className="size-3 animate-spin" />}
          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="h-6 px-2 text-[11px]"
            onClick={() => {
              store.clearDirty();
              chat.newSession();
            }}
            disabled={chat.isStreaming}
            title="Start a new session"
          >
            new
          </Button>
        </span>
      </div>
      <ConversationStream messages={chat.messages} />
      <Composer
        onSend={chat.send}
        onSlash={(cmd) => (cmd === "new" ? chat.newSession() : chat.clear())}
        disabled={chat.isStreaming || chat.status !== "connected"}
      />
    </div>
  );
}
