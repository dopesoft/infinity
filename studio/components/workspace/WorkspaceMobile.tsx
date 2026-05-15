"use client";

import { useCallback, useEffect, useState } from "react";
import { motion, AnimatePresence, type PanInfo } from "framer-motion";
import { MessageSquare, Files, LayoutPanelLeft } from "lucide-react";
import { cn } from "@/lib/utils";
import { WorkspaceChatColumn } from "@/components/workspace/WorkspaceChatColumn";
import { WorkspaceFilesColumn } from "@/components/workspace/WorkspaceFilesColumn";
import { CanvasRightPane } from "@/components/canvas/CanvasRightPane";
import type { useChat } from "@/hooks/useChat";
import { useCanvasStore } from "@/lib/canvas/store";

type ChatHook = ReturnType<typeof useChat>;

export type WorkspaceMode = "chat" | "files" | "canvas";

const MODES: WorkspaceMode[] = ["chat", "files", "canvas"];

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
  const idx = MODES.indexOf(mode);

  const handleDragEnd = useCallback(
    (_: unknown, info: PanInfo) => {
      // Threshold tuned for thumb swipes: ~70px or 350px/s.
      const dx = info.offset.x;
      const vx = info.velocity.x;
      const COMMIT = 70;
      const VELOCITY = 350;
      const triggered = Math.abs(dx) > COMMIT || Math.abs(vx) > VELOCITY;
      if (!triggered) return;
      // Right-drag (positive dx) → previous mode; left-drag → next mode.
      const goingPrev = dx > 0;
      const next = goingPrev ? Math.max(0, idx - 1) : Math.min(MODES.length - 1, idx + 1);
      if (next !== idx) onModeChange(MODES[next]);
    },
    [idx, onModeChange],
  );

  const handleFileOpen = useCallback(() => {
    onModeChange("canvas");
  }, [onModeChange]);

  return (
    <div className="flex h-full min-h-0 flex-col">
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

      {/* Swipeable content area. We render one mode at a time and use
          AnimatePresence for the cross-fade. The drag handler is on the
          motion.div itself — small horizontal drag → cycles modes.

          `touch-pan-y` on the motion.div hands vertical pans back to the
          browser, so the chat's scroller actually receives the gesture
          instead of framer-motion eating it. Without this, any touch
          with a slight horizontal component would be claimed by the
          drag listener and the conversation wouldn't scroll. Combined
          with `dragDirectionLock`, framer commits to whichever axis the
          gesture starts on — so a vertical scroll stays a scroll and a
          horizontal swipe still cycles modes. */}
      <div className="relative min-h-0 flex-1 overflow-hidden">
        <AnimatePresence mode="wait" initial={false}>
          <motion.div
            key={mode}
            className="absolute inset-0 flex min-h-0 flex-col touch-pan-y"
            initial={{ opacity: 0, x: 24 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: -24 }}
            transition={{ duration: 0.18, ease: "easeOut" }}
            drag="x"
            dragDirectionLock
            dragConstraints={{ left: 0, right: 0 }}
            dragElastic={0.18}
            onDragEnd={handleDragEnd}
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
      if (ce.detail?.mode && MODES.includes(ce.detail.mode)) setMode(ce.detail.mode);
    }
    window.addEventListener("workspace:set-mode", onJump);
    return () => window.removeEventListener("workspace:set-mode", onJump);
  }, []);
  return { mode, setMode };
}
