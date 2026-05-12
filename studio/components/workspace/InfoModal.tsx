"use client";

import { useState } from "react";
import { Info, Brain, Activity } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  Drawer,
  DrawerContent,
  DrawerHeader,
  DrawerTitle,
  DrawerTrigger,
} from "@/components/ui/drawer";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useIsDesktop } from "@/lib/use-media-query";
import { LeftPanels, RightPanels } from "@/components/LiveSidePanels";
import type { ChatMessage } from "@/hooks/useChat";

/**
 * InfoModal — the read-only "glance" surface for the workspace.
 *
 *   Brain    Intent stream · Active skills · Context budget
 *   Activity Last heartbeat · Trust queue · System health
 *
 * The split mirrors the old Live left/right rails — Brain is "what is the
 * agent thinking right now", Activity is "what has the agent been doing".
 * For deep edits (manage skills, adjust crons, review trust history),
 * users still navigate to the standalone routes via the desktop overflow
 * kebab or mobile drawer's "More" section.
 *
 * Dialog on lg+ (centered modal), Drawer on <lg (bottom sheet). The Dialog
 * uses our plain-opacity transition — no slide-from-anywhere.
 */
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
  const isDesktop = useIsDesktop();

  const triggerNode = trigger ?? (
    <button
      type="button"
      aria-label="Workspace info"
      title="Brain · Activity"
      className="inline-flex size-9 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
    >
      <Info className="size-4" />
    </button>
  );

  const body = (
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
      <TabsContent value="brain" className="mt-0 max-h-[70dvh] overflow-y-auto scroll-touch px-3 pb-4 pt-2 sm:px-4 lg:max-h-[60vh]">
        <LeftPanels messages={messages} usedTokens={usedTokens} />
      </TabsContent>
      <TabsContent value="activity" className="mt-0 max-h-[70dvh] overflow-y-auto scroll-touch px-3 pb-4 pt-2 sm:px-4 lg:max-h-[60vh]">
        <RightPanels wsConnected={wsConnected} />
      </TabsContent>
    </Tabs>
  );

  if (isDesktop) {
    return (
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogTrigger asChild>{triggerNode}</DialogTrigger>
        <DialogContent className="max-w-2xl gap-0 p-0">
          <DialogHeader>
            <DialogTitle>Workspace info</DialogTitle>
          </DialogHeader>
          {body}
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <Drawer open={open} onOpenChange={setOpen}>
      <DrawerTrigger asChild>{triggerNode}</DrawerTrigger>
      <DrawerContent>
        <DrawerHeader className="text-left">
          <DrawerTitle>Workspace info</DrawerTitle>
        </DrawerHeader>
        {body}
      </DrawerContent>
    </Drawer>
  );
}
