"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Globe, Square, Loader2, ArrowRight, MousePointerClick, Hand } from "lucide-react";
import { Button } from "@/components/ui/button";
import { navigateBrowserSession, sendBrowserInput, setBrowserControl, type BrowserInputEvent } from "@/lib/api";
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
 *   ┌── toolbar (globe · editable URL bar · Go · live · Stop) ──┐
 *   │                                                            │
 *   │   <img> screencast — click / type / scroll forwarded to    │
 *   │   the real browser over /api/browser/session/{id}/input    │
 *   └──────────────────────────────────────────────────────────────┘
 *
 * Interaction model:
 *   - URL bar is a real input. Enter (or Go) navigates the live session.
 *   - Clicking the <img> maps the click back to the source viewport (the CDP
 *     frame is emitted at native size and object-contain letterboxes it, so we
 *     undo the contain transform) and dispatches a real mouse click.
 *   - With the surface focused, keystrokes type into the focused field and
 *     scroll wheel scrolls the page. This is what makes a login the boss can
 *     actually complete, instead of a dead screenshot.
 */
export function CanvasBrowserView({
  frame,
  url,
  running,
  stopping,
  sessionId,
  controller = "agent",
  onStop,
}: {
  frame: string | null;
  url: string;
  running: boolean;
  stopping: boolean;
  sessionId: string;
  /** Who is driving: "agent" (Jarvis) or "human" (takeover in progress). */
  controller?: "agent" | "human";
  onStop: () => void;
}) {
  const imgRef = useRef<HTMLImageElement>(null);
  const surfaceRef = useRef<HTMLDivElement>(null);
  const [urlDraft, setUrlDraft] = useState(url);
  const [editingUrl, setEditingUrl] = useState(false);
  const [focused, setFocused] = useState(false);

  // Keep the URL bar in sync with the live location unless the boss is editing.
  useEffect(() => {
    if (!editingUrl) setUrlDraft(url);
  }, [url, editingUrl]);

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

  const submitUrl = useCallback(() => {
    const u = urlDraft.trim();
    if (!u || !sessionId) return;
    setEditingUrl(false);
    void navigateBrowserSession(sessionId, u);
  }, [urlDraft, sessionId]);

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      {/* Toolbar with an editable URL bar */}
      <div className="flex h-10 shrink-0 items-center gap-2 border-b bg-muted/20 px-2 dark:bg-zinc-900/40">
        <Globe className="size-3.5 shrink-0 text-muted-foreground" />
        <form
          className="flex min-w-0 flex-1 items-center gap-1"
          onSubmit={(e) => {
            e.preventDefault();
            submitUrl();
          }}
        >
          <input
            value={urlDraft}
            onChange={(e) => setUrlDraft(e.target.value)}
            onFocus={() => setEditingUrl(true)}
            onBlur={() => setEditingUrl(false)}
            placeholder={running ? "enter a URL…" : "starting browser…"}
            disabled={!canControl}
            inputMode="url"
            autoCapitalize="off"
            autoCorrect="off"
            spellCheck={false}
            className="min-w-0 flex-1 truncate rounded bg-transparent px-1 py-0.5 font-mono text-xs text-foreground placeholder:text-muted-foreground focus:bg-background focus:outline-none focus:ring-1 focus:ring-ring disabled:opacity-60"
            aria-label="Browser URL"
          />
          {editingUrl ? (
            <Button type="submit" size="sm" variant="ghost" className="h-7 shrink-0 px-2" title="Go">
              <ArrowRight className="size-3.5" />
            </Button>
          ) : null}
        </form>
        {running && controller === "human" ? (
          // Takeover in progress: the boss is driving; Jarvis waits. The
          // hand-back button is the ONLY explicit action — taking over
          // happens implicitly on his first click/keystroke.
          <>
            <span className="inline-flex shrink-0 items-center gap-1 rounded-full bg-warning/15 px-2 py-0.5 text-[10px] font-medium text-warning">
              <Hand className="size-3" />
              you&apos;re driving
            </span>
            <Button
              size="sm"
              variant="ghost"
              className="h-7 shrink-0 gap-1 px-2 text-[11px] text-warning hover:bg-warning/10"
              onClick={() => void setBrowserControl(sessionId, "agent")}
              title="Give control back to Jarvis"
            >
              Hand back
            </Button>
          </>
        ) : running ? (
          <span className="inline-flex shrink-0 items-center gap-1 rounded-full bg-success/10 px-2 py-0.5 text-[10px] font-medium text-success">
            <span className="size-1.5 animate-pulse rounded-full bg-success" />
            live
          </span>
        ) : null}
        <Button
          size="sm"
          variant="ghost"
          className="h-7 shrink-0 gap-1 px-2 text-[11px]"
          onClick={onStop}
          disabled={stopping}
          title="Stop the browser session"
        >
          {stopping ? <Loader2 className="size-3.5 animate-spin" /> : <Square className="size-3.5" />}
          Stop
        </Button>
      </div>

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
                Click to take over — then type, scroll, and click like a normal browser.
              </div>
            ) : null}
          </>
        ) : (
          <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
            <Globe className="size-8 text-muted-foreground/40" />
            <p className="text-sm text-muted-foreground">Waiting for the browser to start…</p>
          </div>
        )}
      </div>
    </div>
  );
}
