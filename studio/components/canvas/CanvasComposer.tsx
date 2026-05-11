"use client";

import { Hash, Loader2 } from "lucide-react";
import { Composer } from "@/components/Composer";
import { Button } from "@/components/ui/button";
import { useCanvasStore } from "@/lib/canvas/store";
import { cn } from "@/lib/utils";
import type { useChat } from "@/hooks/useChat";

type ChatHook = ReturnType<typeof useChat>;

/**
 * CanvasComposer — thin wrapper around the existing Composer.
 *
 * The actual chat input is unchanged; we just frame it with a header that
 * shows the active session id and how many files have been touched. The
 * composer's send/clear/new-session controls all route through useChat()
 * exactly the same way Live does, so a new prompt typed here streams back
 * into both surfaces in real time.
 */
export function CanvasComposer({ chat }: { chat: ChatHook }) {
  const store = useCanvasStore();
  const shortId = chat.sessionId ? chat.sessionId.slice(0, 8) : "—";

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex items-center gap-2 border-b px-2 py-1.5 text-[11px] text-muted-foreground">
        <Hash className="size-3 shrink-0" />
        <span className="font-mono truncate" title={chat.sessionId}>
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
      <div
        className={cn(
          "flex min-h-0 flex-1 flex-col",
          // Wrap the Composer so the textarea grows up to the panel ceiling
          // when the user resizes the divider — instead of overflowing the
          // panel. The Composer itself caps at ~4 lines internally; everything
          // beyond that scrolls.
        )}
      >
        <Composer
          onSend={chat.send}
          onSlash={(cmd) => (cmd === "new" ? chat.newSession() : chat.clear())}
          disabled={chat.isStreaming || chat.status !== "connected"}
        />
      </div>
    </div>
  );
}
