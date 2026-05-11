"use client";

import { CanvasFileTree } from "@/components/canvas/CanvasFileTree";
import { CanvasGitPanel } from "@/components/canvas/CanvasGitPanel";
import { CanvasRightPane } from "@/components/canvas/CanvasRightPane";
import type { useChat } from "@/hooks/useChat";

type ChatHook = ReturnType<typeof useChat>;

/**
 * CanvasMobileShell — the *top half* of the mobile Canvas layout.
 *
 * Renders the active mobile tab (Files / Git / Editor). The tab strip
 * itself lives in CanvasFrame; this component just swaps content. Chat
 * (ConversationStream + Composer) is the bottom half of CanvasFrame's
 * vertical split — it's not rendered here so a deep scroll in the tree
 * doesn't push the input below the keyboard.
 *
 * Every pane is mounted-but-hidden so switching tabs is instant: Monaco
 * inside the Editor tab doesn't re-init, the file tree keeps its
 * expansion state, the git poll keeps running.
 */
export function CanvasMobileShell({
  chat,
  mobileTab,
}: {
  chat: ChatHook;
  mobileTab: "files" | "git" | "editor";
}) {
  return (
    <div className="relative h-full min-h-0">
      <div
        className={`absolute inset-0 overflow-hidden ${mobileTab === "files" ? "" : "pointer-events-none opacity-0"}`}
        aria-hidden={mobileTab !== "files"}
      >
        <CanvasFileTree />
      </div>
      <div
        className={`absolute inset-0 overflow-hidden ${mobileTab === "git" ? "" : "pointer-events-none opacity-0"}`}
        aria-hidden={mobileTab !== "git"}
      >
        <CanvasGitPanel sessionId={chat.sessionId} />
      </div>
      <div
        className={`absolute inset-0 overflow-hidden ${mobileTab === "editor" ? "" : "pointer-events-none opacity-0"}`}
        aria-hidden={mobileTab !== "editor"}
      >
        <CanvasRightPane chat={chat} />
      </div>
    </div>
  );
}
