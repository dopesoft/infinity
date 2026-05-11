"use client";

import { useState } from "react";
import { Files, GitBranch } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import { CanvasFileTree } from "@/components/canvas/CanvasFileTree";
import { CanvasGitPanel } from "@/components/canvas/CanvasGitPanel";
import { CanvasComposer } from "@/components/canvas/CanvasComposer";
import { useCanvasStore } from "@/lib/canvas/store";
import type { useChat } from "@/hooks/useChat";

type ChatHook = ReturnType<typeof useChat>;

/**
 * CanvasLeftPane — vertical split: tabbed Files/Git on top, composer on
 * bottom. The composer's panel sets a minimum so the chat input never
 * collapses below a usable height; the tabs panel takes the rest.
 *
 * The Files vs Git split is intentional: file tree gives you anywhere-in-repo
 * read access (open any file in a Monaco tab), while Git shows the *changes*
 * — the ones the agent or the boss is about to ship. They mirror VS Code's
 * Explorer + Source Control split.
 */
export function CanvasLeftPane({ chat }: { chat: ChatHook }) {
  const store = useCanvasStore();
  const [tab, setTab] = useState<"files" | "git">("files");

  return (
    <div className="flex h-full min-h-0 flex-col border-r bg-muted/30 dark:bg-zinc-900/40">
      <ResizablePanelGroup direction="vertical" autoSaveId="canvas:left:v5">
        <ResizablePanel defaultSize={35} minSize={10} maxSize={70}>
          <div className="flex h-full min-h-0 flex-col">
            <Tabs value={tab} onValueChange={(v) => setTab(v as "files" | "git")} className="flex h-full min-h-0 flex-col">
              <div className="border-b px-2 py-1.5">
                <TabsList className="grid h-9 w-full grid-cols-2 bg-transparent p-0">
                  <TabsTrigger
                    value="files"
                    className="data-[state=active]:bg-background data-[state=active]:shadow-sm gap-1.5 text-xs"
                  >
                    <Files className="size-3.5" />
                    Files
                  </TabsTrigger>
                  <TabsTrigger
                    value="git"
                    className="data-[state=active]:bg-background data-[state=active]:shadow-sm gap-1.5 text-xs"
                  >
                    <GitBranch className="size-3.5" />
                    Changes
                    {store.dirtyPaths.size > 0 && (
                      <span className="ml-0.5 inline-flex h-4 min-w-[16px] items-center justify-center rounded-full bg-warning/20 px-1 font-mono text-[10px] font-semibold leading-none text-warning">
                        {store.dirtyPaths.size > 99 ? "99+" : store.dirtyPaths.size}
                      </span>
                    )}
                  </TabsTrigger>
                </TabsList>
              </div>
              <TabsContent value="files" className="mt-0 flex-1 overflow-hidden">
                <CanvasFileTree />
              </TabsContent>
              <TabsContent value="git" className="mt-0 flex-1 overflow-hidden">
                <CanvasGitPanel sessionId={chat.sessionId} />
              </TabsContent>
            </Tabs>
          </div>
        </ResizablePanel>
        <ResizableHandle withHandle />
        <ResizablePanel defaultSize={65} minSize={30}>
          <CanvasComposer chat={chat} />
        </ResizablePanel>
      </ResizablePanelGroup>
    </div>
  );
}
