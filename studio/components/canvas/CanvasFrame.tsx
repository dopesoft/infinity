"use client";

import { useEffect, useState } from "react";
import { Files, GitBranch, MonitorPlay } from "lucide-react";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import { CanvasLeftPane } from "@/components/canvas/CanvasLeftPane";
import { CanvasRightPane } from "@/components/canvas/CanvasRightPane";
import { CanvasMobileShell } from "@/components/canvas/CanvasMobileShell";
import { CanvasComposer } from "@/components/canvas/CanvasComposer";
import { useCanvasStore } from "@/lib/canvas/store";
import { isCodeChangeTool, extractToolFilePath } from "@/lib/canvas/detection";
import { useWebSocket } from "@/lib/ws/provider";
import type { useChat } from "@/hooks/useChat";

type ChatHook = ReturnType<typeof useChat>;

/**
 * CanvasFrame — the responsive shell that hosts the IDE layout.
 *
 *   Desktop (lg+):   [ left pane (resizable) | divider | right pane ]
 *                    Left pane is itself vertically split:
 *                      [ Files / Git tabs (resizable) | divider | Composer ]
 *
 *   Mobile (<lg):    Full-screen with a 3-tab strip: Files / Git / Editor.
 *                    Editor = the mobile incarnation of the right pane.
 *                    Composer is sticky-bottom regardless of which tab.
 *
 * The WS subscription here is one of two consumers of tool_call events:
 *   1. useChat (in the composer) — to display messages in conversation form.
 *   2. CanvasFrame — to update the dirty-file set so the file tree and
 *      Monaco tabs reflect agent activity in real time.
 *
 * Both consumers ride the same multi-subscriber WebSocketProvider, so no
 * extra socket is opened.
 */
export function CanvasFrame({ chat }: { chat: ChatHook }) {
  const store = useCanvasStore();
  const ws = useWebSocket();
  const [mobileTab, setMobileTab] = useState<"files" | "git" | "editor">("files");

  // Subscribe to WS tool_call events to mark files dirty as the agent works.
  // Filtered by sessionId so a stale tab from a previous session doesn't
  // light up files this session never touched.
  useEffect(() => {
    return ws.subscribe((ev) => {
      if ("session_id" in ev && ev.session_id && chat.sessionId && ev.session_id !== chat.sessionId) {
        return;
      }
      if (ev.type !== "tool_call") return;
      const name = ev.tool_call.name;
      if (!isCodeChangeTool(name)) return;
      const path = extractToolFilePath(ev.tool_call.input);
      if (path) store.markDirty(path);
    });
  }, [ws, chat.sessionId, store]);

  return (
    <>
      {/* Desktop layout */}
      <div className="hidden min-h-0 flex-1 lg:flex">
        <ResizablePanelGroup direction="horizontal" autoSaveId="canvas:h">
          <ResizablePanel defaultSize={32} minSize={20} maxSize={50}>
            <CanvasLeftPane chat={chat} />
          </ResizablePanel>
          <ResizableHandle />
          <ResizablePanel defaultSize={68} minSize={40}>
            <CanvasRightPane chat={chat} />
          </ResizablePanel>
        </ResizablePanelGroup>
      </div>

      {/* Mobile layout — vertical 50/50 split. Top half hosts a sub-tab
          switcher (Files / Git / Editor), each scrollable inside. Bottom
          half is always the chat (ConversationStream + Composer) so the
          boss can keep prompting without losing context. The divider is
          draggable so either half can grow. */}
      <div className="flex min-h-0 flex-1 flex-col lg:hidden">
        <ResizablePanelGroup direction="vertical" autoSaveId="canvas:mobile:v1">
          <ResizablePanel defaultSize={50} minSize={20}>
            <div className="flex h-full min-h-0 flex-col">
              <div className="sticky top-0 z-10 shrink-0 border-b bg-background/95 px-1.5 pt-1 backdrop-blur supports-[backdrop-filter]:bg-background/80">
                <div className="grid grid-cols-3 gap-1 pb-1">
                  <MobileTabButton
                    active={mobileTab === "files"}
                    onClick={() => setMobileTab("files")}
                    icon={<Files className="size-4" />}
                    label="Files"
                  />
                  <MobileTabButton
                    active={mobileTab === "git"}
                    onClick={() => setMobileTab("git")}
                    icon={<GitBranch className="size-4" />}
                    label="Git"
                    badge={store.dirtyPaths.size > 0 ? store.dirtyPaths.size : undefined}
                  />
                  <MobileTabButton
                    active={mobileTab === "editor"}
                    onClick={() => setMobileTab("editor")}
                    icon={<MonitorPlay className="size-4" />}
                    label="Editor"
                  />
                </div>
              </div>
              <div className="min-h-0 flex-1">
                <CanvasMobileShell chat={chat} mobileTab={mobileTab} />
              </div>
            </div>
          </ResizablePanel>
          <ResizableHandle withHandle />
          <ResizablePanel defaultSize={50} minSize={30}>
            <CanvasComposer chat={chat} />
          </ResizablePanel>
        </ResizablePanelGroup>
      </div>
    </>
  );
}

function MobileTabButton({
  active,
  onClick,
  icon,
  label,
  badge,
}: {
  active: boolean;
  onClick: () => void;
  icon: React.ReactNode;
  label: string;
  badge?: number;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={
        "relative inline-flex h-11 w-full items-center justify-center gap-1.5 rounded-md px-3 text-sm font-medium transition-colors " +
        (active
          ? "bg-accent text-accent-foreground"
          : "text-muted-foreground hover:bg-accent/60 hover:text-foreground")
      }
      aria-pressed={active}
    >
      {icon}
      <span>{label}</span>
      {typeof badge === "number" && badge > 0 && (
        <span className="ml-1 inline-flex h-4 min-w-[16px] items-center justify-center rounded-full bg-warning/20 px-1 font-mono text-[10px] font-semibold leading-none text-warning">
          {badge > 99 ? "99+" : badge}
        </span>
      )}
    </button>
  );
}
