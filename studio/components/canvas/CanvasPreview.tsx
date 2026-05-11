"use client";

import { useEffect, useMemo, useRef, useState } from "react";
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

  // Measure the available area (the inner pane minus toolbar) so we can
  // scale phone/tablet previews to fit without scrolling. The iframe
  // keeps its NATIVE pixel dimensions (so the embedded app sees a real
  // mobile/tablet viewport and its responsive CSS triggers), but a CSS
  // transform shrinks the rendered output. This is how Lovable / v0 / the
  // Chrome devtools device toolbar all do it.
  const stageRef = useRef<HTMLDivElement>(null);
  const [stageSize, setStageSize] = useState<{ w: number; h: number }>({ w: 0, h: 0 });
  useEffect(() => {
    const el = stageRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver((entries) => {
      const e = entries[0];
      if (!e) return;
      const cr = e.contentRect;
      setStageSize({ w: cr.width, h: cr.height });
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [dims]);

  // Outer pad we want to leave around the device frame. Keeps shadows
  // visible and the device "floating" against the bg gradient.
  const STAGE_PAD = 16;
  const scale = useMemo(() => {
    if (!dims) return 1;
    if (stageSize.w === 0 || stageSize.h === 0) return 1;
    const usableW = Math.max(0, stageSize.w - STAGE_PAD * 2);
    const usableH = Math.max(0, stageSize.h - STAGE_PAD * 2);
    const s = Math.min(usableW / dims.width, usableH / dims.height, 1);
    return Math.max(s, 0.1);
  }, [dims, stageSize]);

  // Desktop preset = the iframe IS the preview pane, edge-to-edge, no
  // chrome. Phone / tablet presets keep the device-card styling (padding,
  // shadow, rounded corners, gradient bg) because that's the entire point
  // of a device preset — you want to see what the app looks like at that
  // size, framed against neutral chrome. Desktop is the default; render
  // it like a real browser window flush against its container.
  return (
    <div className="flex h-full min-h-0 flex-col">
      <CanvasPreviewToolbar effectiveUrl={effectiveUrl} />
      {!effectiveUrl ? (
        <div className="relative min-h-0 flex-1 overflow-auto bg-gradient-to-br from-zinc-200/60 to-zinc-300/40 dark:from-zinc-900/40 dark:to-black">
          <EmptyPreview />
        </div>
      ) : dims ? (
        <div
          ref={stageRef}
          className="relative min-h-0 flex-1 overflow-hidden bg-gradient-to-br from-zinc-200/60 to-zinc-300/40 dark:from-zinc-900/40 dark:to-black"
        >
          {/* The scaled device frame. The iframe is rendered at its NATIVE
              dimensions (so the embedded app's responsive CSS sees a real
              mobile/tablet viewport) and shrunk visually via CSS transform.
              The wrapper takes up the scaled-down footprint so flex centring
              works against the post-scale size. */}
          <div className="absolute inset-0 flex items-center justify-center">
            <div
              style={{
                width: `${dims.width * scale}px`,
                height: `${dims.height * scale}px`,
              }}
            >
              <div
                className="overflow-hidden rounded-xl border bg-background shadow-2xl ring-1 ring-black/5 dark:ring-white/5"
                style={{
                  width: `${dims.width}px`,
                  height: `${dims.height}px`,
                  transform: `scale(${scale})`,
                  transformOrigin: "top left",
                }}
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
          </div>
        </div>
      ) : (
        <iframe
          key={`preview-${store.previewRefreshKey}`}
          src={effectiveUrl}
          title="Preview"
          className="block min-h-0 flex-1 border-0 bg-background"
          sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-modals allow-pointer-lock allow-downloads"
          allow="clipboard-write; clipboard-read"
        />
      )}
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
