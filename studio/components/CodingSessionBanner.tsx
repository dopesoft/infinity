"use client";

import { useEffect, useState } from "react";
import { ArrowRight, LayoutPanelLeft, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useWebSocket } from "@/lib/ws/provider";
import { isCodeChangeTool } from "@/lib/canvas/detection";
import { cn } from "@/lib/utils";

/**
 * CodingSessionBanner - mobile-only nudge into the Canvas mode.
 *
 * On desktop the unified /live workspace already shows the edited file in
 * column 3: every code-change tool call auto-opens an editor tab there (see
 * Workspace.tsx). No banner is needed, so this surface is hidden at lg+.
 *
 * On mobile, Canvas is a separate swipe-away mode, so the first
 * claude_code__edit/write/multiedit in a session surfaces a one-tap jump to
 * it via the `workspace:set-mode` event (handled by useWorkspaceMode). We no
 * longer navigate to `/canvas` - that route was folded into /live and now
 * just redirects back here, which is why the old "Open" button went nowhere.
 *
 * Dismissal:
 *   - Click the X -> dismissed for this session (sessionStorage).
 *   - Click "View" -> switches to Canvas mode + dismisses.
 *   - Auto-fades after 45s if neither action.
 *
 * Auto-open: if Settings -> Canvas -> "Auto-open Canvas" is enabled, we jump
 * to Canvas mode directly the first time without showing the banner.
 */

const DISMISS_KEY_PREFIX = "infinity:canvas:bannerDismissed:";
const AUTO_OPEN_KEY = "infinity:canvas:autoOpen";
const FADE_AFTER_MS = 45_000;

function jumpToCanvas() {
  if (typeof window === "undefined") return;
  window.dispatchEvent(
    new CustomEvent("workspace:set-mode", { detail: { mode: "canvas" } }),
  );
}

export function CodingSessionBanner({ sessionId }: { sessionId: string }) {
  const ws = useWebSocket();
  const [show, setShow] = useState(false);

  // Reset when the session changes.
  useEffect(() => {
    setShow(false);
  }, [sessionId]);

  // Subscribe for the first relevant tool call.
  useEffect(() => {
    if (!sessionId) return;
    return ws.subscribe((ev) => {
      if (ev.type !== "tool_call") return;
      if ("session_id" in ev && ev.session_id && ev.session_id !== sessionId) return;
      if (!isCodeChangeTool(ev.tool_call.name)) return;
      try {
        const dismissed = window.sessionStorage.getItem(DISMISS_KEY_PREFIX + sessionId);
        if (dismissed === "1") return;
        const autoOpen = window.localStorage.getItem(AUTO_OPEN_KEY) === "1";
        if (autoOpen) {
          jumpToCanvas();
          return;
        }
      } catch {
        /* ignore */
      }
      setShow(true);
    });
  }, [ws, sessionId]);

  // Auto-fade after FADE_AFTER_MS so the banner doesn't camp forever.
  useEffect(() => {
    if (!show) return;
    const id = setTimeout(() => setShow(false), FADE_AFTER_MS);
    return () => clearTimeout(id);
  }, [show]);

  function dismiss() {
    setShow(false);
    if (sessionId && typeof window !== "undefined") {
      try {
        window.sessionStorage.setItem(DISMISS_KEY_PREFIX + sessionId, "1");
      } catch {
        /* ignore */
      }
    }
  }

  if (!show) return null;

  return (
    <div
      className={cn(
        "mx-3 mt-2 flex items-center gap-2 rounded-lg border border-info/30 bg-info/10 px-3 py-2 text-xs shadow-sm lg:hidden",
        "animate-in fade-in slide-in-from-top-1 duration-300",
      )}
      role="status"
    >
      <LayoutPanelLeft className="size-4 shrink-0 text-info" aria-hidden />
      <div className="min-w-0 flex-1">
        <p className="font-medium">Coding session detected</p>
        <p className="text-[11px] text-muted-foreground">
          The file being changed is open in Canvas.
        </p>
      </div>
      <Button
        size="sm"
        variant="default"
        className="h-8 gap-1 text-xs"
        onClick={() => {
          jumpToCanvas();
          dismiss();
        }}
      >
        View <ArrowRight className="size-3" />
      </Button>
      <Button
        type="button"
        size="icon"
        variant="ghost"
        className="size-8 shrink-0"
        onClick={dismiss}
        aria-label="Dismiss"
      >
        <X className="size-3.5" />
      </Button>
    </div>
  );
}
