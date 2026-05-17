"use client";

import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import { WorkspaceChatColumn } from "@/components/workspace/WorkspaceChatColumn";
import { WorkspaceFilesColumn } from "@/components/workspace/WorkspaceFilesColumn";
import { CanvasRightPane } from "@/components/canvas/CanvasRightPane";
import { WorkspaceMobile, useWorkspaceMode } from "@/components/workspace/WorkspaceMobile";
import { useCanvasStore } from "@/lib/canvas/store";
import {
  useCurrentProject,
  CurrentProjectProvider,
} from "@/lib/canvas/useCurrentProject";
import { useWebSocket } from "@/lib/ws/provider";
import { isCodeChangeTool, extractToolFilePaths } from "@/lib/canvas/detection";
import { fetchCanvasConfig, fetchBridgeStatus } from "@/lib/canvas/api";
import type { useChat } from "@/hooks/useChat";

type ChatHook = ReturnType<typeof useChat>;

/**
 * Workspace — the unified /live surface that merges the old Live + Canvas
 * tabs into one work environment.
 *
 *   Desktop (lg+)   three horizontally-resizable columns:
 *                     [ Chat | Files / Git | Canvas (preview + editor tabs) ]
 *
 *   Mobile (<lg)    three full-bleed modes selected by sticky-top pills:
 *                     [ Chat | Files | Canvas ]
 *                   Swipe horizontally to cycle. Tapping a file in Files
 *                   auto-jumps to Canvas.
 *
 * Project = session: the file tree + preview re-scope to the current
 * session's `project_path`. Sessions without a project show empty Canvas
 * surfaces (handled inside CanvasFileTree / CanvasPreview empty states),
 * so chat-only sessions don't leak the workspace folder.
 */
export function Workspace({ chat }: { chat: ChatHook }) {
  const store = useCanvasStore();
  const ws = useWebSocket();
  const current = useCurrentProject();
  const { mode, setMode } = useWorkspaceMode("chat");

  // Server-configured fallback path (INFINITY_DEFAULT_PROJECT_PATH on
  // core). Sessions without their own project_path land here — typically
  // the Jarvis repo — so chat-only sessions don't sit in an empty "set
  // workspace root first" state. Also pre-warms the cloud bridge from
  // Railway App Sleeping (fire-and-forget).
  const [defaultProjectPath, setDefaultProjectPath] = useState<string>("");
  useEffect(() => {
    const ac = new AbortController();
    void (async () => {
      const cfg = await fetchCanvasConfig(ac.signal);
      if (cfg?.default_project_path) setDefaultProjectPath(cfg.default_project_path);
    })();
    void fetchBridgeStatus(ac.signal).catch(() => {});
    return () => ac.abort();
  }, []);

  // Project = session lifecycle. When the active session changes its
  // project_path, re-scope the canvas store. When the session has no
  // project AND no configured default, blank the root so the file tree
  // shows the empty state. Wait for the initial fetch to complete so we
  // don't blow away a hydrating root on first render.
  const projectPath = current.session?.project_path?.trim() ?? "";
  useEffect(() => {
    if (current.loading) return;
    const next = projectPath || defaultProjectPath;
    if (next) {
      if (next !== store.root) {
        store.setRoot(next);
        store.closeAllFiles();
        store.clearDirty();
      }
    } else if (store.root) {
      // No session project AND no configured default — wipe.
      store.setRoot("");
      store.closeAllFiles();
      store.clearDirty();
    }
  }, [projectPath, defaultProjectPath, current.loading, store]);

  // Mark files dirty as the agent edits them, filtered by sessionId so a
  // stale tab from another session doesn't paint phantom changes.
  useEffect(() => {
    return ws.subscribe((ev) => {
      if (
        "session_id" in ev &&
        ev.session_id &&
        chat.sessionId &&
        ev.session_id !== chat.sessionId
      ) {
        return;
      }
      if (ev.type !== "tool_call") return;
      const name = ev.tool_call.name;
      if (!isCodeChangeTool(name)) return;
      // github__push_files carries multiple paths in one call — mark
      // every one so the Changes badge reflects the real fan-out.
      for (const path of extractToolFilePaths(ev.tool_call.input)) {
        store.markDirty(path);
      }
    });
  }, [ws, chat.sessionId, store]);

  // Mount gate — react-resizable-panels reads localStorage on first paint
  // so SSR vs CSR diverge; render a stable skeleton until the client takes
  // over.
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  if (!mounted) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center" suppressHydrationWarning>
        <Loader2 className="size-5 animate-spin text-muted-foreground" aria-hidden />
      </div>
    );
  }

  return (
    <CurrentProjectProvider value={current}>
      {/* Desktop — three horizontally-resizable columns */}
      <div className="hidden min-h-0 flex-1 lg:flex">
        {/* No autoSaveId — column widths reset to defaults on every refresh.
            Dragging the dividers still works in-session, but a reload always
            returns to the 25 / 18 / 57 layout. The boss explicitly wants the
            workspace to feel "fresh" on each visit instead of accumulating
            stuck layouts from random drag sessions. */}
        <ResizablePanelGroup direction="horizontal">
          <ResizablePanel defaultSize={25} minSize={15} maxSize={50}>
            <div className="flex h-full min-h-0 flex-col border-r bg-muted/30 dark:bg-zinc-900/40">
              <WorkspaceChatColumn chat={chat} />
            </div>
          </ResizablePanel>
          <ResizableHandle />
          <ResizablePanel defaultSize={18} minSize={12} maxSize={40}>
            <div className="flex h-full min-h-0 flex-col border-r bg-muted/20 dark:bg-zinc-900/30">
              <WorkspaceFilesColumn sessionId={chat.sessionId} />
            </div>
          </ResizablePanel>
          <ResizableHandle />
          <ResizablePanel defaultSize={57} minSize={30}>
            <div className="flex h-full min-h-0 flex-col">
              <CanvasRightPane chat={chat} />
            </div>
          </ResizablePanel>
        </ResizablePanelGroup>
      </div>

      {/* Mobile — single-column with mode pills */}
      <div className="flex min-h-0 flex-1 flex-col lg:hidden">
        <WorkspaceMobile chat={chat} mode={mode} onModeChange={setMode} />
      </div>
    </CurrentProjectProvider>
  );
}
