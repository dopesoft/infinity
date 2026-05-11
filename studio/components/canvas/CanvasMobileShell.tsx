"use client";

import { CanvasFileTree } from "@/components/canvas/CanvasFileTree";
import { CanvasGitPanel } from "@/components/canvas/CanvasGitPanel";
import { CanvasRightPane } from "@/components/canvas/CanvasRightPane";
import { CanvasComposer } from "@/components/canvas/CanvasComposer";
import type { useChat } from "@/hooks/useChat";

type ChatHook = ReturnType<typeof useChat>;

/**
 * CanvasMobileShell — Files / Git / Editor swap with a sticky composer
 * at the bottom. Each tab is mounted-but-hidden so switching is instant
 * and Monaco doesn't re-init.
 */
export function CanvasMobileShell({
  chat,
  mobileTab,
}: {
  chat: ChatHook;
  mobileTab: "files" | "git" | "editor";
  onMobileTabChange?: (next: "files" | "git" | "editor") => void;
}) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="relative min-h-0 flex-1">
        <div
          className={`absolute inset-0 ${mobileTab === "files" ? "" : "pointer-events-none opacity-0"}`}
          aria-hidden={mobileTab !== "files"}
        >
          <CanvasFileTree />
        </div>
        <div
          className={`absolute inset-0 ${mobileTab === "git" ? "" : "pointer-events-none opacity-0"}`}
          aria-hidden={mobileTab !== "git"}
        >
          <CanvasGitPanel sessionId={chat.sessionId} />
        </div>
        <div
          className={`absolute inset-0 ${mobileTab === "editor" ? "" : "pointer-events-none opacity-0"}`}
          aria-hidden={mobileTab !== "editor"}
        >
          <CanvasRightPane chat={chat} />
        </div>
      </div>
      <div className="shrink-0 border-t">
        <CanvasComposer chat={chat} />
      </div>
    </div>
  );
}
