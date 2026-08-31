"use client";

import { useState } from "react";
import { Info, Brain, Activity } from "lucide-react";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { Chip, ChipTrigger } from "@/components/ui/chip";
import { Tabs, TabsContent } from "@/components/ui/tabs";
import { PageTabsList, PageTabsTrigger } from "@/components/ui/page-tabs";
import { LeftPanels, RightPanels } from "@/components/LiveSidePanels";
import type { ChatMessage } from "@/hooks/useChat";

/**
 * InfoModal - the read-only "glance" surface for the workspace.
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
    <Chip
      iconOnly
      icon={<Info />}
      aria-label="Workspace info"
      title="Brain · Activity"
      onClick={() => setOpen(true)}
    />
  );

  return (
    <>
      {/* Trigger lives outside the modal so callers can pass an arbitrary
          node and the click-to-open is wired locally. */}
      {trigger ? (
        <ChipTrigger onOpen={() => setOpen(true)}>{triggerNode}</ChipTrigger>
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
          <PageTabsList columns={2} className="mx-4 w-auto">
            <PageTabsTrigger value="brain">
              <Brain className="size-3.5" />
              Brain
            </PageTabsTrigger>
            <PageTabsTrigger value="activity">
              <Activity className="size-3.5" />
              Activity
            </PageTabsTrigger>
          </PageTabsList>
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
