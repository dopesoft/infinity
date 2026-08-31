"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { MonitorPlay, MonitorX, Sparkles, Loader2, AlertTriangle } from "lucide-react";
import { CanvasBrowserView } from "@/components/canvas/CanvasBrowser";
import { CanvasAuthCard, usePendingAuthExtension } from "@/components/canvas/CanvasAuthCard";
import { useCanvasStore, devicePresetDimensions } from "@/lib/canvas/store";
import { useWebSocket } from "@/lib/ws/provider";
import { useRuns } from "@/lib/runs/useRuns";
import { isCodeChangeTool } from "@/lib/canvas/detection";
import { useProjectContext } from "@/lib/canvas/useCurrentProject";
import { coreBaseURL } from "@/lib/api";

/**
 * CanvasPreview - body of the Preview tab.
 *
 *   ┌── toolbar (URL / device toggle / refresh / open-in-new) ──┐
 *   │                                                            │
 *   │          iframe wrapped in a centered device frame         │
 *   │                                                            │
 *   └────────────────────────────────────────────────────────────┘
 *
 * The iframe lives inside a sandboxed container with explicit width/height
 * for mobile/tablet presets, and 100%×100% for desktop. We remount the
 * iframe via its `key` on every refresh - a key change is the only way
 * to force a same-URL reload in iframes without messing with `src=""`
 * tricks that some browsers cache through.
 *
 * Auto-refresh on agent edits: we subscribe to tool_result events for
 * write/edit tools and bump the refresh key with a debounced timer. This
 * makes the preview feel alive - every time the agent ships a change,
 * the iframe reloads ~500ms later.
 */
const AUTO_REFRESH_DEBOUNCE_MS = 600;

export function CanvasPreview({ sessionId = "" }: { sessionId?: string }) {
  const store = useCanvasStore();
  const ws = useWebSocket();
  const projectCtx = useProjectContext();
  const autoTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // A cli tool waiting on device-login surfaces here as a sign-in card.
  // A pending sign-in is scoped to the session that started it (auth_session_id),
  // so the card only appears in the conversation that triggered the sign-in —
  // never in an unrelated one. The dismiss below is a secondary control within
  // that originating session.
  const { pending: pendingAuth, refresh: refreshAuth } = usePendingAuthExtension(sessionId);

  // Remember a dismissal for this browser session (sessionStorage) so a fresh
  // page load can re-offer it but navigating around doesn't keep shoving it back.
  const [dismissedAuth, setDismissedAuth] = useState<Set<string>>(() => {
    if (typeof window === "undefined") return new Set();
    try {
      return new Set(JSON.parse(sessionStorage.getItem("infinity:auth-dismissed") || "[]"));
    } catch {
      return new Set();
    }
  });
  const dismissAuth = useCallback((name: string) => {
    setDismissedAuth((prev) => {
      const next = new Set(prev);
      next.add(name);
      try {
        sessionStorage.setItem("infinity:auth-dismissed", JSON.stringify([...next]));
      } catch {}
      return next;
    });
  }, []);

  // Live cloud-browser screencast. We reuse THIS tab (Preview) to show the
  // stream rather than spawning a second tab — when a browser session is
  // live, the screencast takes over the Preview surface (see early return
  // below). Frames ride the per-session WS broadcaster, so they keep
  // flowing across every observe/act/extract and survive refresh.
  const [browserFrame, setBrowserFrame] = useState<string | null>(null);
  // The live address and who is driving live in the canvas store, not here,
  // because the bar that renders them is BrowserFrame one level up. Keeping a
  // second copy down here is what produced two address bars showing two
  // different URLs.
  const { latest: browserRun } = useRuns({ kind: "browser.session", limit: 5 });
  const browserRunning = browserRun?.status === "running";

  useEffect(() => {
    return ws.subscribe((ev) => {
      if (sessionId && ev.session_id && ev.session_id !== sessionId) return;
      if (ev.type === "browser_control") {
        store.setBrowserController(ev.browser_control.controller);
        // A takeover request must be seen: pull focus to Preview.
        if (ev.browser_control.controller === "human" && store.activeTabId !== "preview") {
          store.setActiveTabId("preview");
        }
        return;
      }
      if (ev.type !== "browser_frame") return;
      const f = ev.browser_frame;
      setBrowserFrame(f.frame);
      if (f.url) store.setBrowserUrl(f.url);
      if (f.browser_session_id) store.setBrowserSessionId(f.browser_session_id);
      if (!store.browserActive) {
        store.setBrowserActive(true);
        // Pull focus to Preview so the boss sees the browser drive.
        if (store.activeTabId !== "preview") store.setActiveTabId("preview");
      }
    });
  }, [ws, store, sessionId]);

  // Stopping is driven from the bar in BrowserFrame now. Drop the last frame
  // when the session ends so a later session never opens on a stale image of
  // the previous one.
  useEffect(() => {
    if (!store.browserActive) setBrowserFrame(null);
  }, [store.browserActive]);

  // The session decides the surface. Sessions WITHOUT a project_path are
  // chat-only - show the "no app yet" empty state and skip the iframe.
  // Sessions WITH a project_path always point the iframe at the bridge
  // tunnel (preview.dopesoft.io); the bridge handles booting / crashed /
  // ready transitions in the response body itself.
  const hasProject = !!projectCtx?.session?.project_path?.trim();
  const projectStatus = projectCtx?.project?.status;

  // Cloud projects are served by the cloud workspace bridge through Core's
  // /api/canvas/preview proxy (the project DTO is tagged bridge="cloud").
  // Mac projects keep using the dev-server tunnel the boss configured. This
  // is what makes the Preview tab render a cloud app (the pulse-board) with
  // no manual URL - and a manual URL bar override still wins for both.
  const isCloudPreview = projectCtx?.project?.bridge === "cloud";

  // Base URL the iframe points at. Priority: explicit URL-bar override →
  // cloud proxy (when the project is cloud) → env/Mac tunnel. The iframe src
  // appends a cache-busting query param keyed to previewRefreshKey so every
  // click of ↻ (and every agent edit) forces a true reload.
  const baseUrl = useMemo(() => {
    // A cloud project is served by the cloud bridge through Core's proxy -
    // that's where the app physically lives, so it ALWAYS wins. This must come
    // BEFORE previewUrl/env: a leftover Mac-tunnel URL (preview.dopesoft.io)
    // persisted in localStorage was hijacking cloud previews ("refused to
    // connect"). A manual URL-bar entry still applies to non-cloud previews.
    if (isCloudPreview) return `${coreBaseURL()}/api/canvas/preview/`;
    const explicit = store.previewUrl?.trim();
    if (explicit) return explicit;
    // Only fall back to the env / Mac-tunnel dev-server URL when this session
    // actually HAS a project to preview. A chat-only session has nothing to
    // show, so embedding the tunnel just hammers preview.dopesoft.io - which
    // 502s and X-Frame-Options-blocks whenever the Mac dev server is asleep,
    // spamming the console on every chat refresh. No project => no iframe.
    if (hasProject && store.envPreviewUrl?.trim()) return store.envPreviewUrl.trim();
    return "";
  }, [store.previewUrl, store.envPreviewUrl, isCloudPreview, hasProject]);

  // First-mount cache key - a single timestamp captured once per page load.
  // Together with previewRefreshKey, this guarantees the iframe URL is
  // unique on every paint, defeating the browser's HTTP cache for
  // Next.js dev's versioned chunks (which 404 after a dev-server
  // restart if you reload the cached HTML).
  const mountKeyRef = useRef<number>(Date.now());
  const effectiveUrl = useMemo(() => {
    if (!baseUrl) return "";
    const sep = baseUrl.includes("?") ? "&" : "?";
    return `${baseUrl}${sep}_cv=${mountKeyRef.current}-${store.previewRefreshKey}`;
  }, [baseUrl, store.previewRefreshKey]);

  // Only auto-reload when there's actually a live app in the frame: a non-cloud
  // (Mac) dev server, or a cloud project that's actually running. Reloading an
  // empty/cloud-splash preview just re-flashes it for no reason.
  const previewIsLive = !!baseUrl && (!isCloudPreview || projectStatus === "running");
  // A cloud preview only has something to show while its dev server is running
  // or booting. Pointing the iframe at the proxy otherwise just renders the
  // bridge's raw "404 page not found" (no dev server behind it) - which reads
  // as "broken" when nothing's actually wrong. Show a calm idle state instead.
  const cloudAppLive = projectStatus === "running" || projectStatus === "booting";

  // Auto-refresh on code-change tool_result events — but ONLY for THIS session's
  // edits to a live app. Without the session filter, every background fs/github
  // write from any other session (crons, heartbeat, self-improve, autonomous
  // turns) remounted the iframe and flashed the preview even when the boss was
  // doing nothing in it. (The browser_frame subscription above already scopes by
  // session; this one was missing the same guard.)
  useEffect(() => {
    return ws.subscribe((ev) => {
      if (ev.type !== "tool_result") return;
      if (sessionId && ev.session_id && ev.session_id !== sessionId) return;
      if (!previewIsLive) return;
      if (!isCodeChangeTool(ev.tool_result.name)) return;
      if (autoTimer.current) clearTimeout(autoTimer.current);
      autoTimer.current = setTimeout(() => {
        store.refreshPreview();
        autoTimer.current = null;
      }, AUTO_REFRESH_DEBOUNCE_MS);
    });
  }, [ws, store, sessionId, previewIsLive]);

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
  //
  // We use a callback ref + ResizeObserver + window resize. The callback
  // ref lets us measure synchronously the very first time the element
  // mounts, so the first paint already has the correct scale and the
  // iframe never briefly renders at native size with scrollbars.
  const [stageEl, setStageEl] = useState<HTMLDivElement | null>(null);
  const [stageSize, setStageSize] = useState<{ w: number; h: number }>(() => ({ w: 0, h: 0 }));

  // Stable measurer. State setters from useState are guaranteed stable
  // by React, so we can use empty deps and still call setStageSize.
  // setStageSize uses a functional update so we never re-render when
  // the size hasn't actually changed (defends against ResizeObserver
  // firing on subpixel jitter, which would otherwise loop with the
  // CSS transform also rounding).
  const remeasure = useCallback((el: HTMLDivElement | null) => {
    if (!el) return;
    const w = el.clientWidth;
    const h = el.clientHeight;
    setStageSize((prev) => (prev.w === w && prev.h === h ? prev : { w, h }));
  }, []);

  // Stable ref callback. If we let this be a new function each render,
  // React detaches/reattaches the ref every cycle → setStageEl fires →
  // re-render → new function reference → infinite loop (React #185).
  const stageRefCb = useCallback(
    (el: HTMLDivElement | null) => {
      setStageEl(el);
      remeasure(el);
    },
    [remeasure],
  );

  useEffect(() => {
    if (!stageEl) return;
    const onResize = () => remeasure(stageEl);
    if (typeof ResizeObserver === "undefined") {
      window.addEventListener("resize", onResize);
      remeasure(stageEl);
      return () => window.removeEventListener("resize", onResize);
    }
    const ro = new ResizeObserver(() => remeasure(stageEl));
    ro.observe(stageEl);
    // Belt-and-braces: also listen to window resize, since some
    // browsers (Safari) don't fire RO on every layout shift.
    window.addEventListener("resize", onResize);
    return () => {
      ro.disconnect();
      window.removeEventListener("resize", onResize);
    };
  }, [stageEl, remeasure]);

  // Outer pad we want to leave around the device frame. Keeps shadows
  // visible and the device "floating" against the bg gradient.
  const STAGE_PAD = 12;
  const scale = useMemo(() => {
    if (!dims) return 1;
    // Until we've measured at least once, hide the iframe entirely (see
    // render below) rather than risk rendering at native size and
    // briefly overflowing.
    if (stageSize.w === 0 || stageSize.h === 0) return 0;
    const usableW = Math.max(0, stageSize.w - STAGE_PAD * 2);
    const usableH = Math.max(0, stageSize.h - STAGE_PAD * 2);
    const s = Math.min(usableW / dims.width, usableH / dims.height, 1);
    return Math.max(s, 0.05);
  }, [dims, stageSize]);

  // Desktop preset = the iframe IS the preview pane, edge-to-edge, no
  // chrome. Phone / tablet presets keep the device-card styling (padding,
  // shadow, rounded corners, gradient bg) because that's the entire point
  // of a device preset - you want to see what the app looks like at that
  // size, framed against neutral chrome. Desktop is the default; render
  // it like a real browser window flush against its container.
  // Live browser session takes over the Preview surface — the boss watches
  // Jarvis drive here rather than in a separate tab. Stop returns to the
  // app preview. (All hooks above have already run, so this early return
  // is safe.)
  if (store.browserActive) {
    return (
      <CanvasBrowserView
        frame={browserFrame}
        running={browserRunning}
        sessionId={store.browserSessionId ?? ""}
      />
    );
  }

  // A tool waiting on device-login takes over the pane with a sign-in card -
  // self-contained, no trip to Settings, and it replaces the old raw-404 dead
  // end. Authenticating is the one thing only the boss can do, so it outranks
  // the app / empty states below.
  if (pendingAuth && !dismissedAuth.has(pendingAuth.name)) {
    return (
      <CanvasAuthCard
        ext={pendingAuth}
        sessionId={sessionId}
        onResolved={refreshAuth}
        onDismiss={() => dismissAuth(pendingAuth.name)}
      />
    );
  }

  // No project on this session - Canvas is a passive surface. Show a
  // "tell the agent what to build" empty state instead of trying to
  // proxy through the bridge. This is the v1 path for new sessions.
  if (projectCtx && !hasProject && !projectCtx.loading) {
    return (
      <div className="flex h-full min-h-0 flex-col">
        <div className="relative min-h-0 flex-1 overflow-auto bg-gradient-to-br from-zinc-200/60 to-zinc-300/40 dark:from-zinc-900/40 dark:to-black">
          <NoProjectPreview />
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      {projectStatus && projectStatus !== "running" ? (
        <ProjectStatusBanner status={projectStatus} error={projectCtx?.project?.last_error} />
      ) : null}
      {!effectiveUrl ? (
        <div className="relative min-h-0 flex-1 overflow-auto bg-gradient-to-br from-zinc-200/60 to-zinc-300/40 dark:from-zinc-900/40 dark:to-black">
          <EmptyPreview />
        </div>
      ) : isCloudPreview && !cloudAppLive ? (
        <div className="relative min-h-0 flex-1 overflow-auto bg-gradient-to-br from-zinc-200/60 to-zinc-300/40 dark:from-zinc-900/40 dark:to-black">
          <CloudIdlePreview />
        </div>
      ) : dims ? (
        <div
          ref={stageRefCb}
          className="relative min-h-0 flex-1 overflow-hidden bg-gradient-to-br from-zinc-200/60 to-zinc-300/40 dark:from-zinc-900/40 dark:to-black"
        >
          {/* The scaled device frame. The iframe is rendered at its NATIVE
              dimensions (so the embedded app's responsive CSS sees a real
              mobile/tablet viewport) and shrunk visually via CSS transform.
              The wrapper takes up the scaled-down footprint so flex centring
              works against the post-scale size. Hidden until we've measured
              the stage at least once so we never briefly overflow. */}
          <div
            className="absolute inset-0 flex items-center justify-center transition-opacity"
            style={{ opacity: scale > 0 ? 1 : 0 }}
          >
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
                  transform: `scale(${scale || 1})`,
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

function NoProjectPreview() {
  return (
    // Pinned absolute to the Preview column root (marked `relative`)
    // so this icon centers against the FULL column rect - same
    // physical Y as Chat + Files empty states, which do the same.
    // pointer-events-none keeps the URL bar above clickable.
    <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
      <span className="inline-flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
        <Sparkles className="size-5" aria-hidden />
      </span>
      <div className="max-w-md space-y-1">
        <h3 className="text-sm font-semibold">Live preview</h3>
        <p className="text-xs leading-relaxed text-muted-foreground">
          A preview shows up here once something is live.
        </p>
      </div>
    </div>
  );
}

function ProjectStatusBanner({ status, error }: { status: string; error?: string }) {
  const tone =
    status === "booting"
      ? "bg-info/10 text-info border-info/30"
      : status === "crashed"
        ? "bg-danger/10 text-danger border-danger/30"
        : "bg-muted text-muted-foreground border";
  const icon =
    status === "booting" ? (
      <Loader2 className="size-3.5 animate-spin" aria-hidden />
    ) : status === "crashed" ? (
      <AlertTriangle className="size-3.5" aria-hidden />
    ) : null;
  const label =
    status === "booting"
      ? "Warming up dev server…"
      : status === "crashed"
        ? `Dev server crashed${error ? ` - ${error}` : ""}`
        : status === "idle"
          ? "Dev server idle - switch sessions to wake it"
          : status;
  return (
    <div className={"flex items-center gap-2 border-b px-3 py-1 text-[11px] " + tone}>
      {icon}
      <span className="truncate">{label}</span>
    </div>
  );
}

// CloudIdlePreview - shown when a cloud session has no running dev server, so
// the proxy would otherwise serve a raw 404. Calm "nothing running yet" state
// instead of a fake error.
function CloudIdlePreview() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
      <span className="inline-flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
        <MonitorPlay className="size-5" aria-hidden />
      </span>
      <div className="max-w-md space-y-1">
        <h3 className="text-sm font-semibold">Nothing running here yet</h3>
        <p className="text-xs leading-relaxed text-muted-foreground">
          This is a cloud session with no live app. Ask Jarvis to build or start one and
          it&apos;ll appear here automatically.
        </p>
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
