"use client";

import { useEffect, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { MessageSquare, Files, LayoutPanelLeft } from "lucide-react";
import { cn } from "@/lib/utils";
import { WorkspaceChatColumn } from "@/components/workspace/WorkspaceChatColumn";
import { WorkspaceFilesColumn } from "@/components/workspace/WorkspaceFilesColumn";
import { CanvasRightPane } from "@/components/canvas/CanvasRightPane";
import type { useChat } from "@/hooks/useChat";
import { useCanvasStore } from "@/lib/canvas/store";

type ChatHook = ReturnType<typeof useChat>;

export type WorkspaceMode = "chat" | "files" | "canvas";

/**
 * WorkspaceMobile — the phone-first manifestation of the unified workspace.
 *
 * Three full-bleed modes (Chat / Files / Canvas) selected via sticky-top
 * pills below the header. Horizontal swipe gestures (framer-motion drag)
 * cycle between adjacent modes for one-handed thumb operation. Tapping a
 * file in the Files mode auto-jumps to Canvas — that's the rule the boss
 * locked in: "I expect when I click a file then we automatically go into
 * canvas to view it."
 *
 * Badges on the pills surface activity in modes the user isn't currently
 * looking at: a dot on Files when files have been modified this turn, a
 * dot on Canvas when there's an open file tab and the preview is rebuilding
 * (handled inside CanvasRightPane via the store).
 */
export function WorkspaceMobile({
  chat,
  mode,
  onModeChange,
}: {
  chat: ChatHook;
  mode: WorkspaceMode;
  onModeChange: (m: WorkspaceMode) => void;
}) {
  const store = useCanvasStore();

  function handleFileOpen() {
    onModeChange("canvas");
  }

  return (
    <div className="flex h-full min-h-0 min-w-0 flex-col overflow-x-hidden">

      {/* Sticky pills below header */}
      <div className="sticky top-0 z-20 flex shrink-0 items-center justify-center gap-1 border-b bg-background/95 px-3 py-1.5 backdrop-blur supports-[backdrop-filter]:bg-background/80">
        <ModePill
          active={mode === "chat"}
          icon={<MessageSquare className="size-4" />}
          label="Chat"
          onClick={() => onModeChange("chat")}
        />
        <ModePill
          active={mode === "files"}
          icon={<Files className="size-4" />}
          label="Files"
          onClick={() => onModeChange("files")}
          badge={store.dirtyPaths.size}
        />
        <ModePill
          active={mode === "canvas"}
          icon={<LayoutPanelLeft className="size-4" />}
          label="Canvas"
          onClick={() => onModeChange("canvas")}
        />
      </div>

      {/* Content area. One mode at a time with an opacity-only cross-fade.
          The swipe-to-switch gesture was REMOVED — framer-motion's drag
          consumed pointer events that should have been scrolls (the
          conversation wouldn't scroll under it) AND its dragElastic
          translated the panel horizontally on incidental movement,
          which is what caused the "page shifted right when the
          thinking indicator appeared" symptom. The pills at the top
          are the sole way to switch modes; they're already
          one-tap-from-anywhere and don't fight the touch surface
          underneath. `overflow-x-hidden` + `min-w-0` on the wrapper
          is a belt-and-suspenders against any descendant trying to
          extend past the viewport again. */}
      <div className="relative min-h-0 min-w-0 flex-1 overflow-hidden">
        <AnimatePresence mode="wait" initial={false}>
          <motion.div
            key={mode}
            className="absolute inset-0 flex min-h-0 min-w-0 flex-col overflow-x-hidden"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.15, ease: "easeOut" }}
          >
            {mode === "chat" && <WorkspaceChatColumn chat={chat} />}
            {mode === "files" && (
              <WorkspaceFilesColumn sessionId={chat.sessionId} onFileOpen={handleFileOpen} />
            )}
            {mode === "canvas" && (
              <div className="flex h-full min-h-0 flex-col">
                <CanvasRightPane chat={chat} />
              </div>
            )}
          </motion.div>
        </AnimatePresence>
      </div>
    </div>
  );
}

function ModePill({
  active,
  icon,
  label,
  onClick,
  badge,
}: {
  active: boolean;
  icon: React.ReactNode;
  label: string;
  onClick: () => void;
  badge?: number;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "relative inline-flex h-9 items-center gap-1.5 rounded-full px-3.5 text-sm font-medium transition-colors",
        active
          ? "bg-foreground text-background"
          : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
      )}
    >
      {icon}
      <span>{label}</span>
      {typeof badge === "number" && badge > 0 && (
        <span
          aria-label={`${badge} change${badge === 1 ? "" : "s"}`}
          className={cn(
            "ml-0.5 inline-flex h-4 min-w-[16px] items-center justify-center rounded-full px-1 font-mono text-[10px] font-semibold leading-none",
            active ? "bg-background/20 text-background" : "bg-warning/20 text-warning",
          )}
        >
          {badge > 99 ? "99+" : badge}
        </span>
      )}
    </button>
  );
}

/** Hook helper so the parent can drive the mode externally (keyboard, info modal). */
export function useWorkspaceMode(initial: WorkspaceMode = "chat") {
  const [mode, setMode] = useState<WorkspaceMode>(initial);
  // Listen for app-wide "open file" events so non-mobile-tree opens also
  // jump to canvas (e.g. clicked from chat tool-card preview, etc.).
  useEffect(() => {
    if (typeof window === "undefined") return;
    function onJump(e: Event) {
      const ce = e as CustomEvent<{ mode?: WorkspaceMode }>;
      const m = ce.detail?.mode;
      if (m === "chat" || m === "files" || m === "canvas") setMode(m);
    }
    window.addEventListener("workspace:set-mode", onJump);
    return () => window.removeEventListener("workspace:set-mode", onJump);
  }, []);
  return { mode, setMode };
}
