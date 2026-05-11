"use client";

import { useEffect, useMemo, useRef } from "react";
import { MonitorPlay, MonitorX } from "lucide-react";
import { CanvasPreviewToolbar } from "@/components/canvas/CanvasPreviewToolbar";
import { useCanvasStore, devicePresetDimensions } from "@/lib/canvas/store";
import { useWebSocket } from "@/lib/ws/provider";
import { isCodeChangeTool } from "@/lib/canvas/detection";

/**
 * CanvasPreview — body of the Preview tab.
 *
 *   ┌── toolbar (URL / device toggle / refresh / open-in-new) ──┐
 *   │                                                            │
 *   │          iframe wrapped in a centered device frame         │
 *   │                                                            │
 *   └────────────────────────────────────────────────────────────┘
 *
 * The iframe lives inside a sandboxed container with explicit width/height
 * for mobile/tablet presets, and 100%×100% for desktop. We remount the
 * iframe via its `key` on every refresh — a key change is the only way
 * to force a same-URL reload in iframes without messing with `src=""`
 * tricks that some browsers cache through.
 *
 * Auto-refresh on agent edits: we subscribe to tool_result events for
 * write/edit tools and bump the refresh key with a debounced timer. This
 * makes the preview feel alive — every time the agent ships a change,
 * the iframe reloads ~500ms later.
 */
const AUTO_REFRESH_DEBOUNCE_MS = 600;

export function CanvasPreview() {
  const store = useCanvasStore();
  const ws = useWebSocket();
  const autoTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const effectiveUrl = useMemo(() => {
    const explicit = store.previewUrl?.trim();
    if (explicit) return explicit;
    if (store.envPreviewUrl?.trim()) return store.envPreviewUrl.trim();
    return "";
  }, [store.previewUrl, store.envPreviewUrl]);

  // Auto-refresh on code-change tool_result events.
  useEffect(() => {
    return ws.subscribe((ev) => {
      if (ev.type !== "tool_result") return;
      if (!isCodeChangeTool(ev.tool_result.name)) return;
      if (autoTimer.current) clearTimeout(autoTimer.current);
      autoTimer.current = setTimeout(() => {
        store.refreshPreview();
        autoTimer.current = null;
      }, AUTO_REFRESH_DEBOUNCE_MS);
    });
  }, [ws, store]);

  useEffect(() => () => {
    if (autoTimer.current) clearTimeout(autoTimer.current);
  }, []);

  const dims = devicePresetDimensions(store.device);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <CanvasPreviewToolbar effectiveUrl={effectiveUrl} />
      <div className="relative min-h-0 flex-1 overflow-auto bg-gradient-to-br from-zinc-200/60 to-zinc-300/40 dark:from-zinc-900/40 dark:to-black">
        {effectiveUrl ? (
          <div className="flex min-h-full items-center justify-center p-2 sm:p-4">
            <div
              className="overflow-hidden rounded-xl border bg-background shadow-2xl ring-1 ring-black/5 dark:ring-white/5"
              style={
                dims
                  ? { width: `${dims.width}px`, height: `${dims.height}px`, maxWidth: "100%", maxHeight: "100%" }
                  : { width: "100%", height: "100%" }
              }
            >
              <iframe
                key={`preview-${store.previewRefreshKey}`}
                src={effectiveUrl}
                title="Preview"
                className="block size-full border-0"
                sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-modals allow-pointer-lock allow-downloads"
                allow="clipboard-write; clipboard-read"
              />
            </div>
          </div>
        ) : (
          <EmptyPreview />
        )}
      </div>
    </div>
  );
}

function EmptyPreview() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
      <div className="relative">
        <MonitorPlay className="size-12 text-muted-foreground/50" />
        <MonitorX className="absolute -right-2 -bottom-2 size-5 text-warning" />
      </div>
      <div className="max-w-md space-y-1">
        <h3 className="text-sm font-semibold">No preview URL configured</h3>
        <p className="text-xs leading-relaxed text-muted-foreground">
          Set <code className="rounded bg-muted px-1 font-mono text-[10px]">NEXT_PUBLIC_PREVIEW_URL</code>{" "}
          or use the URL bar above to point Canvas at your Mac dev server
          (e.g. <code className="rounded bg-muted px-1 font-mono text-[10px]">http://localhost:3000</code>).
          See{" "}
          <a
            href="/settings"
            className="text-foreground underline decoration-dotted underline-offset-2 hover:decoration-solid"
          >
            Settings → Canvas
          </a>{" "}
          for the tunnel setup.
        </p>
      </div>
    </div>
  );
}
