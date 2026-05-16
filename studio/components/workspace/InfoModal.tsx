"use client";

import { useState } from "react";
import { Info, Brain, Activity } from "lucide-react";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { LeftPanels, RightPanels } from "@/components/LiveSidePanels";
import type { ChatMessage } from "@/hooks/useChat";

/**
 * InfoModal — the read-only "glance" surface for the workspace.
 *
 *   Brain    Intent stream · Active skills · Context budget
 *   Activity Last heartbeat · Trust queue · System health
 *
 * Renders through the canonical <ResponsiveModal> (Dialog on lg+, Drawer
 * on <lg, baked-in overflow discipline) so it stays consistent with
 * every other Studio modal surface. */
export function InfoModal({
  messages,
  usedTokens,
  wsConnected,
  trigger,
}: {
  messages: ChatMessage[];
  usedTokens: number;
  wsConnected: boolean;
  trigger?: React.ReactNode;
}) {
  const [open, setOpen] = useState(false);

  const triggerNode = trigger ?? (
    <button
      type="button"
      aria-label="Workspace info"
      title="Brain · Activity"
      onClick={() => setOpen(true)}
      className="inline-flex size-9 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
    >
      <Info className="size-4" />
    </button>
  );

  return (
    <>
      {/* Trigger lives outside the modal so callers can pass an arbitrary
          node and the click-to-open is wired locally. */}
      {trigger ? (
        <span onClick={() => setOpen(true)}>{triggerNode}</span>
      ) : (
        triggerNode
      )}

      <ResponsiveModal
        open={open}
        onOpenChange={setOpen}
        size="lg"
        title="Workspace info"
        bodyClassName="px-0 sm:px-0 pb-4"
      >
        <Tabs defaultValue="brain" className="flex flex-col">
          <TabsList className="grid h-9 w-full grid-cols-2 bg-transparent p-0 px-4">
            <TabsTrigger
              value="brain"
              className="data-[state=active]:bg-accent data-[state=active]:shadow-sm gap-1.5 text-xs"
            >
              <Brain className="size-3.5" />
              Brain
            </TabsTrigger>
            <TabsTrigger
              value="activity"
              className="data-[state=active]:bg-accent data-[state=active]:shadow-sm gap-1.5 text-xs"
            >
              <Activity className="size-3.5" />
              Activity
            </TabsTrigger>
          </TabsList>
          <TabsContent value="brain" className="mt-0 min-w-0 px-3 pt-2 sm:px-4">
            <LeftPanels messages={messages} usedTokens={usedTokens} />
          </TabsContent>
          <TabsContent value="activity" className="mt-0 min-w-0 px-3 pt-2 sm:px-4">
            <RightPanels wsConnected={wsConnected} />
          </TabsContent>
        </Tabs>
      </ResponsiveModal>
    </>
  );
}
