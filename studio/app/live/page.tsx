"use client";

import { useMemo, useState } from "react";
import { IconLayoutSidebarRightExpand } from "@tabler/icons-react";
import { TabFrame } from "@/components/TabFrame";
import { SessionHeader } from "@/components/SessionHeader";
import { ConversationStream } from "@/components/ConversationStream";
import { Composer } from "@/components/Composer";
import { Button } from "@/components/ui/button";
import {
  Drawer,
  DrawerContent,
  DrawerHeader,
  DrawerTitle,
  DrawerTrigger,
} from "@/components/ui/drawer";
import { LeftPanels, RightPanels, MobileStatusStack } from "@/components/LiveSidePanels";
import { useChat } from "@/hooks/useChat";
import type { AgentState } from "@/components/StatusPill";

export default function LivePage() {
  const chat = useChat();
  const [statusOpen, setStatusOpen] = useState(false);

  const startedAt = useMemo(() => {
    const first = chat.messages[0];
    return first ? first.createdAt : null;
  }, [chat.messages]);

  const usedTokens = chat.usage.input + chat.usage.output;

  const agentState: AgentState =
    chat.status === "disconnected"
      ? "offline"
      : chat.isStreaming
        ? "thinking"
        : chat.status === "connected"
          ? "listening"
          : "idle";

  return (
    <TabFrame agentState={agentState}>
      {/* Three-column responsive grid:
          mobile  → 1 col (chat only, status via bottom drawer)
          md      → 2 col (chat + right rail)
          lg+     → 3 col (left rail + chat + right rail)

          Side rails get a subtle muted background so the chat card pops as the
          focal surface. On light: outer = very light grey, chat = white. On
          dark: outer = subtle dark grey over true black, chat = lifted card. */}
      <div className="grid min-h-0 flex-1 grid-cols-1 gap-3 px-3 py-3 sm:px-4 md:grid-cols-[minmax(0,1fr)_18rem] md:gap-4 lg:grid-cols-[16rem_minmax(0,1fr)_18rem] xl:grid-cols-[17rem_minmax(0,1fr)_19rem]">
        {/* Left rail — desktop only */}
        <aside className="scroll-touch hidden rounded-xl bg-muted/60 p-2 lg:block lg:overflow-y-auto dark:bg-zinc-800/60">
          <LeftPanels messages={chat.messages} usedTokens={usedTokens} />
        </aside>

        {/* Center — chat. overflow-hidden is required so child borders/bgs
            (SessionHeader, Composer) clip to the rounded corners — without it
            the rounded card visibly breaks at the four corners. */}
        <section className="flex min-h-0 min-w-0 flex-col overflow-hidden rounded-xl border bg-card">
          <SessionHeader
            sessionId={chat.sessionId}
            startedAt={startedAt}
            onNew={chat.newSession}
            onClear={chat.clear}
            onRewind={undefined}
          />
          <ConversationStream messages={chat.messages} />
          <Composer
            onSend={chat.send}
            onSlash={(cmd) => (cmd === "new" ? chat.newSession() : chat.clear())}
            disabled={chat.isStreaming || chat.status !== "connected"}
          />

          {/* Mobile-only Status trigger pinned just above the composer.
              Opens a bottom drawer with the full panel stack so the panels
              stay reachable on touch devices without crowding the chat. */}
          <div className="border-t bg-background/60 px-3 py-1.5 md:hidden">
            <Drawer open={statusOpen} onOpenChange={setStatusOpen}>
              <DrawerTrigger asChild>
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-8 w-full justify-between text-[11px] font-medium uppercase tracking-wider text-muted-foreground"
                >
                  Status
                  <IconLayoutSidebarRightExpand className="size-4" aria-hidden />
                </Button>
              </DrawerTrigger>
              <DrawerContent>
                <DrawerHeader className="text-left">
                  <DrawerTitle>Live status</DrawerTitle>
                </DrawerHeader>
                <div className="max-h-[70dvh] overflow-y-auto scroll-touch">
                  <MobileStatusStack
                    messages={chat.messages}
                    usedTokens={usedTokens}
                    wsConnected={chat.status === "connected"}
                  />
                </div>
              </DrawerContent>
            </Drawer>
          </div>
        </section>

        {/* Right rail — md and up */}
        <aside className="scroll-touch hidden rounded-xl bg-muted/60 p-2 md:block md:overflow-y-auto dark:bg-zinc-800/60">
          <RightPanels wsConnected={chat.status === "connected"} />
        </aside>
      </div>
    </TabFrame>
  );
}
