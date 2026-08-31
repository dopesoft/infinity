"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Globe, MousePointerClick } from "lucide-react";
import { sendBrowserInput, type BrowserInputEvent } from "@/lib/api";
import { cn } from "@/lib/utils";

// Named keys forwarded as key events; everything else printable goes as text.
const NAMED_KEYS = new Set([
  "Enter", "Tab", "Backspace", "Delete", "Escape",
  "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Home", "End", "PageUp", "PageDown",
]);

/**
 * CanvasBrowserView - the live cloud-browser surface. The boss both WATCHES
 * Jarvis drive AND can take over by hand:
 *
 *   ┌── (the address bar, who is driving, Hand back and Stop all live one ──┐
 *   │    level up, in BrowserFrame — see below)                             │
 *   │   <img> screencast — click / type / scroll forwarded to               │
 *   │   the real browser over /api/browser/session/{id}/input               │
 *   └──────────────────────────────────────────────────────────────────────┘
 *
 * THIS COMPONENT IS THE SURFACE ONLY. It used to carry its own toolbar with a
 * second address bar, which rendered INSIDE BrowserFrame's chrome: two URL
 * bars stacked, the outer one showing the project preview's address, which is
 * a completely different thing and which persisted across chats. The toolbar
 * moved up into BrowserFrame so there is one bar that means one thing. Nothing
 * was dropped in the move: address, driving state, Hand back and Stop are all
 * still there, in the bar above this surface.
 *
 * Interaction model:
 *   - Clicking the <img> maps the click back to the source viewport (the CDP
 *     frame is emitted at native size and object-contain letterboxes it, so we
 *     undo the contain transform) and dispatches a real mouse click.
 *   - With the surface focused, keystrokes type into the focused field and
 *     scroll wheel scrolls the page. This is what makes a login the boss can
 *     actually complete, instead of a dead screenshot.
 *   - Clicking or typing takes control; scrolling does NOT. Watching the agent
 *     work must never take the browser off it (see Registry.claimsControl).
 */
export function CanvasBrowserView({
  frame,
  running,
  sessionId,
}: {
  frame: string | null;
  running: boolean;
  sessionId: string;
}) {
  const imgRef = useRef<HTMLImageElement>(null);
  const surfaceRef = useRef<HTMLDivElement>(null);
  const [focused, setFocused] = useState(false);

  const canControl = !!sessionId;

  const post = useCallback(
    (ev: BrowserInputEvent) => {
      if (!sessionId) return;
      void sendBrowserInput(sessionId, ev);
    },
    [sessionId],
  );

  // Map a pointer event on the letterboxed <img> back to source-viewport px.
  const toViewport = useCallback((clientX: number, clientY: number): { x: number; y: number } | null => {
    const img = imgRef.current;
    if (!img || !img.naturalWidth || !img.naturalHeight) return null;
    const rect = img.getBoundingClientRect();
    const scale = Math.min(rect.width / img.naturalWidth, rect.height / img.naturalHeight);
    if (scale <= 0) return null;
    const dispW = img.naturalWidth * scale;
    const dispH = img.naturalHeight * scale;
    const offX = (rect.width - dispW) / 2;
    const offY = (rect.height - dispH) / 2;
    const x = (clientX - rect.left - offX) / scale;
    const y = (clientY - rect.top - offY) / scale;
    if (x < 0 || y < 0 || x > img.naturalWidth || y > img.naturalHeight) return null;
    return { x: Math.round(x), y: Math.round(y) };
  }, []);

  const handleClick = useCallback(
    (e: React.MouseEvent) => {
      surfaceRef.current?.focus(); // capture keystrokes after a click
      const p = toViewport(e.clientX, e.clientY);
      if (p) post({ type: "click", x: p.x, y: p.y, button: "left" });
    },
    [toViewport, post],
  );

  const handleWheel = useCallback(
    (e: React.WheelEvent) => {
      const p = toViewport(e.clientX, e.clientY);
      if (p) post({ type: "scroll", x: p.x, y: p.y, delta_x: e.deltaX, delta_y: e.deltaY });
    },
    [toViewport, post],
  );

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (!canControl) return;
      // Let the browser handle copy/paste/select-all combos natively (paste is
      // forwarded via onPaste below); don't swallow them as text.
      if (e.metaKey || e.ctrlKey) return;
      if (NAMED_KEYS.has(e.key)) {
        e.preventDefault();
        post({ type: "key", key: e.key });
      } else if (e.key.length === 1) {
        e.preventDefault();
        post({ type: "text", text: e.key });
      }
    },
    [canControl, post],
  );

  const handlePaste = useCallback(
    (e: React.ClipboardEvent) => {
      const text = e.clipboardData.getData("text");
      if (text) {
        e.preventDefault();
        post({ type: "text", text });
      }
    },
    [post],
  );

  // Resize the remote viewport to match the screencast surface so the page
  // reflows to the boss's Jarvis window instead of letterboxing a fixed frame.
  // Debounced so a drag-resize doesn't spam Chromium with metric overrides.
  useEffect(() => {
    const el = surfaceRef.current;
    if (!el || !canControl || typeof ResizeObserver === "undefined") return;
    let timer: ReturnType<typeof setTimeout> | null = null;
    let last = { w: 0, h: 0 };
    const push = () => {
      const w = Math.round(el.clientWidth);
      const h = Math.round(el.clientHeight);
      if (w < 80 || h < 80) return; // tab hidden / not laid out yet
      if (Math.abs(w - last.w) < 8 && Math.abs(h - last.h) < 8) return;
      last = { w, h };
      post({ type: "resize", width: w, height: h });
    };
    const ro = new ResizeObserver(() => {
      if (timer) clearTimeout(timer);
      timer = setTimeout(push, 250);
    });
    ro.observe(el);
    push(); // sync the viewport once on mount / session change
    return () => {
      if (timer) clearTimeout(timer);
      ro.disconnect();
    };
  }, [canControl, post]);

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      {/* Interactive screencast surface. tabIndex makes it focusable so it can
          capture keystrokes; the ring shows the boss it has keyboard focus. */}
      <div
        ref={surfaceRef}
        tabIndex={0}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        onKeyDown={handleKeyDown}
        onPaste={handlePaste}
        className={cn(
          "relative min-h-0 flex-1 bg-black outline-none",
          focused && "ring-2 ring-inset ring-brand/60",
        )}
      >
        {frame ? (
          <>
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              ref={imgRef}
              src={frame}
              alt="Live browser"
              draggable={false}
              onClick={handleClick}
              onWheel={handleWheel}
              className={cn("block size-full object-contain", canControl ? "cursor-pointer" : "cursor-default")}
            />
            {canControl && !focused ? (
              <div className="pointer-events-none absolute inset-x-0 bottom-0 flex items-center justify-center gap-1.5 bg-gradient-to-t from-black/70 to-transparent px-3 py-2 text-[11px] text-zinc-300">
                <MousePointerClick className="size-3.5" />
                Scroll to watch. Click to take over, then type and click like a normal browser.
              </div>
            ) : null}
          </>
        ) : (
          <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
            <Globe className="size-8 text-muted-foreground/40" />
            <p className="text-sm text-muted-foreground">
              {running
                ? "Waiting for the browser to start…"
                : "The browser session has ended."}
            </p>
          </div>
        )}
      </div>
    </div>
  );
}
