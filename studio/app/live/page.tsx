"use client";

import { useEffect, useMemo, useState } from "react";
import { Gauge } from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { SessionHeader } from "@/components/SessionHeader";
import { ConversationStream } from "@/components/ConversationStream";
import { Composer } from "@/components/Composer";
import { CodingSessionBanner } from "@/components/CodingSessionBanner";
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
import { fetchSessions } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";

export default function LivePage() {
  const chat = useChat();
  const [statusOpen, setStatusOpen] = useState(false);
  const [sessionName, setSessionName] = useState<string>("");

  // Pull just the current session's name from the listing endpoint, then
  // keep it fresh via the same realtime channel the drawer uses. Cheap —
  // the listing already runs whenever the drawer opens, this is just one
  // extra fetch on mount + name updates.
  useEffect(() => {
    if (!chat.sessionId) {
      setSessionName("");
      return;
    }
    const ac = new AbortController();
    fetchSessions(ac.signal).then((rows) => {
      if (!rows) return;
      const me = rows.find((r) => r.id === chat.sessionId);
      setSessionName(me?.name ?? "");
    });
    return () => ac.abort();
  }, [chat.sessionId]);

  useRealtime("mem_sessions", async () => {
    if (!chat.sessionId) return;
    const rows = await fetchSessions();
    if (!rows) return;
    const me = rows.find((r) => r.id === chat.sessionId);
    setSessionName(me?.name ?? "");
  });

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

        {/* Center — chat. */}
        <section className="flex min-h-0 min-w-0 flex-col overflow-hidden rounded-xl border bg-muted/60 dark:bg-zinc-800/60">
          <SessionHeader
            sessionId={chat.sessionId}
            sessionName={sessionName}
            startedAt={startedAt}
            onNew={chat.newSession}
            onClear={chat.clear}
            onSwitch={chat.switchSession}
            onRewind={undefined}
            extraActions={
              <Drawer open={statusOpen} onOpenChange={setStatusOpen}>
                <DrawerTrigger asChild>
                  <Button
                    variant="ghost"
                    size="sm"
                    aria-label="Open live status"
                    title="Status"
                    className="md:hidden"
                  >
                    <Gauge className="size-4" />
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
            }
          />
          <CodingSessionBanner sessionId={chat.sessionId} />
          <ConversationStream messages={chat.messages} />
          <Composer
            onSend={chat.send}
            onSlash={(cmd) => (cmd === "new" ? chat.newSession() : chat.clear())}
            disabled={chat.isStreaming || chat.status !== "connected"}
          />
        </section>

        {/* Right rail — md and up */}
        <aside className="scroll-touch hidden rounded-xl bg-muted/60 p-2 md:block md:overflow-y-auto dark:bg-zinc-800/60">
          <RightPanels wsConnected={chat.status === "connected"} />
        </aside>
      </div>
    </TabFrame>
  );
}
