"use client";

import { Globe, Square, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";

/**
 * CanvasBrowserView - the live cloud-browser screencast surface.
 *
 *   ┌── toolbar (globe · current URL · live badge · Stop) ──┐
 *   │                                                        │
 *   │     <img> rendering the live CDP screencast frame      │
 *   │     (the boss watches Jarvis drive in real time)       │
 *   └────────────────────────────────────────────────────────┘
 *
 * Presentational only - CanvasPreview owns the WS subscription + frame
 * state and renders this in place of the dev-server iframe whenever a
 * browser session is live. We reuse the Preview tab (column 3) rather than
 * spawning a second tab.
 *
 * <img> (not <canvas>): the browser decodes JPEG natively, object-contain
 * scales it, it's accessible, and the future click-to-take-over math stays
 * simple (map click coords against the rendered <img> rect back to source).
 */
export function CanvasBrowserView({
  frame,
  url,
  running,
  stopping,
  onStop,
}: {
  frame: string | null;
  url: string;
  running: boolean;
  stopping: boolean;
  onStop: () => void;
}) {
  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      {/* Toolbar */}
      <div className="flex h-10 shrink-0 items-center gap-2 border-b bg-muted/20 px-2 dark:bg-zinc-900/40">
        <Globe className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground" title={url}>
          {url || "starting browser…"}
        </span>
        {running ? (
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

      {/* Screencast surface */}
      <div className="relative min-h-0 flex-1 bg-black">
        {frame ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={frame} alt="Live browser screencast" className="block size-full object-contain" />
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
