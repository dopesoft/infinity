"use client";

import { useState } from "react";
import { Files, GitBranch } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { CanvasFileTree } from "@/components/canvas/CanvasFileTree";
import { CanvasGitPanel } from "@/components/canvas/CanvasGitPanel";
import { useCanvasStore } from "@/lib/canvas/store";

/**
 * WorkspaceFilesColumn - second column in the unified workspace.
 *
 * Files / Changes (git) toggle. Opening a file dispatches `openFile` on the
 * canvas store, which both registers a new tab on the right column AND
 * (on mobile) is what the parent picks up via the `onFileOpen` callback to
 * auto-switch the mobile mode pill to Canvas.
 */
export function WorkspaceFilesColumn({
  sessionId,
  onFileOpen,
}: {
  sessionId?: string | null;
  onFileOpen?: (path: string) => void;
}) {
  const store = useCanvasStore();
  const [tab, setTab] = useState<"files" | "git">("files");

  // Wrap store.openFile to surface the path up to the parent so mobile can
  // auto-jump to the Canvas mode. Desktop ignores this - both columns are
  // visible already, so the user's eye already lands on the new tab.
  const wrappedOpen = (path: string) => {
    store.openFile(path);
    onFileOpen?.(path);
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Tabs
        value={tab}
        onValueChange={(v) => setTab(v as "files" | "git")}
        className="flex h-full min-h-0 flex-col"
      >
        {/* Tab strip - height-matched (h-10) to the Canvas right pane's tab
            strip so the filter input below this row lines up across columns
            with the preview URL bar. */}
        <div className="flex h-10 shrink-0 items-center border-b bg-muted/20 px-1 dark:bg-zinc-900/40">
          <TabsList className="grid h-8 w-full grid-cols-2 bg-transparent p-0">
            <TabsTrigger
              value="files"
              className="h-8 data-[state=active]:bg-background data-[state=active]:shadow-sm gap-1.5 text-xs"
            >
              <Files className="size-3.5" />
              Files
            </TabsTrigger>
            <TabsTrigger
              value="git"
              className="h-8 data-[state=active]:bg-background data-[state=active]:shadow-sm gap-1.5 text-xs"
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
        <TabsContent value="files" className="mt-0 min-h-0 flex-1 overflow-hidden">
          <CanvasFileTree onFileOpen={wrappedOpen} />
        </TabsContent>
        <TabsContent value="git" className="mt-0 min-h-0 flex-1 overflow-hidden">
          <CanvasGitPanel sessionId={sessionId ?? null} onFileOpen={wrappedOpen} />
        </TabsContent>
      </Tabs>
    </div>
  );
}
