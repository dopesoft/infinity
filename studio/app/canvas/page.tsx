"use client";

import { useEffect } from "react";
import { TabFrame } from "@/components/TabFrame";
import { CanvasFrame } from "@/components/canvas/CanvasFrame";
import { CanvasStoreProvider, useCanvasStore } from "@/lib/canvas/store";
import { fetchCanvasConfig } from "@/lib/canvas/api";
import { useChat } from "@/hooks/useChat";
import type { AgentState } from "@/components/StatusPill";

/**
 * /canvas — Lovable-style IDE surface. The TabFrame shell stays consistent
 * with every other tab so navigation feels native; everything below the
 * header is the Canvas layout (file tree + git + preview + Monaco tabs +
 * persistent composer).
 *
 * Session continuity comes from useChat() reading localStorage[infinity:sessionId],
 * which is the same key Live writes. Open Canvas while a Live conversation
 * is in flight → the composer here picks up exactly where you left off,
 * messages stream into both views in parallel.
 */
export default function CanvasPage() {
  const envPreviewUrl = process.env.NEXT_PUBLIC_PREVIEW_URL ?? "";
  return (
    <CanvasStoreProvider envPreviewUrl={envPreviewUrl}>
      <CanvasPageInner />
    </CanvasStoreProvider>
  );
}

function CanvasPageInner() {
  const chat = useChat();
  const store = useCanvasStore();

  // Pull the workspace root + bridge status from Core on mount. The root
  // defaults to $HOME on the Mac; the boss can override it in Settings →
  // Canvas. envPreviewUrl from NEXT_PUBLIC_PREVIEW_URL is honored unless
  // the user has saved a localStorage override.
  useEffect(() => {
    const ac = new AbortController();
    fetchCanvasConfig(ac.signal).then((cfg) => {
      if (!cfg) return;
      if (!store.root && cfg.root) store.setRoot(cfg.root);
      if (!store.previewUrl && cfg.preview_url) store.setPreviewUrl(cfg.preview_url);
      store.setBridgeOk(cfg.mac_bridge_ok);
    });
    return () => ac.abort();
    // Intentional: we only fetch once on mount; subsequent changes are
    // local until the user reloads. The bridge status is best-effort UI
    // hinting, not a security boundary.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const agentState: AgentState =
    chat.status === "disconnected"
      ? "offline"
      : chat.isStreaming
        ? "thinking"
        : chat.status === "connected"
          ? "listening"
          : "idle";

  return (
    <TabFrame agentState={agentState}>
      <CanvasFrame chat={chat} />
    </TabFrame>
  );
}
