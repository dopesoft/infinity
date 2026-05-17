"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { ArrowRight, LayoutPanelLeft, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useWebSocket } from "@/lib/ws/provider";
import { isCodeChangeTool } from "@/lib/canvas/detection";
import { cn } from "@/lib/utils";

/**
 * CodingSessionBanner - first claude_code__edit/write/multiedit in a Live
 * session triggers this banner. It surfaces a one-tap link to Canvas where
 * the boss can review the diff and watch the preview update.
 *
 * Dismissal:
 *   - Click the X → dismissed for this session (sessionStorage).
 *   - Click "Open in Canvas" → routes + dismisses.
 *   - Auto-fades after 45s if neither action.
 *
 * Auto-open: if Settings → Canvas → "Auto-open Canvas" is enabled, we
 * navigate directly the first time without showing the banner.
 */

const DISMISS_KEY_PREFIX = "infinity:canvas:bannerDismissed:";
const AUTO_OPEN_KEY = "infinity:canvas:autoOpen";
const FADE_AFTER_MS = 45_000;

export function CodingSessionBanner({ sessionId }: { sessionId: string }) {
  const ws = useWebSocket();
  const pathname = usePathname();
  const [show, setShow] = useState(false);

  // Reset when the session changes; respect prior dismissal of this session.
  useEffect(() => {
    if (!sessionId) {
      setShow(false);
      return;
    }
    if (typeof window === "undefined") return;
    try {
      const dismissed = window.sessionStorage.getItem(DISMISS_KEY_PREFIX + sessionId);
      if (dismissed === "1") setShow(false);
      else setShow(false);
    } catch {
      /* ignore */
    }
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
        if (autoOpen && pathname !== "/canvas") {
          window.location.assign("/canvas");
          return;
        }
      } catch {
        /* ignore */
      }
      setShow(true);
    });
  }, [ws, sessionId, pathname]);

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

  if (!show || pathname === "/canvas") return null;

  return (
    <div
      className={cn(
        "mx-3 mt-2 flex items-center gap-2 rounded-lg border border-info/30 bg-info/10 px-3 py-2 text-xs shadow-sm",
        "animate-in fade-in slide-in-from-top-1 duration-300",
      )}
      role="status"
    >
      <LayoutPanelLeft className="size-4 shrink-0 text-info" aria-hidden />
      <div className="min-w-0 flex-1">
        <p className="font-medium">Coding session detected</p>
        <p className="text-[11px] text-muted-foreground">
          Watch diffs and live preview in Canvas.
        </p>
      </div>
      <Link href="/canvas" onClick={dismiss}>
        <Button size="sm" variant="default" className="h-8 gap-1 text-xs">
          Open <ArrowRight className="size-3" />
        </Button>
      </Link>
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
