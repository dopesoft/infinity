"use client";

import { useEffect, useMemo, useState } from "react";
import { AppShell } from "@/components/AppShell";
import { SessionHeader } from "@/components/SessionHeader";
import { InfoModal } from "@/components/workspace/InfoModal";
import { Workspace } from "@/components/workspace/Workspace";
import { BridgePill } from "@/components/canvas/BridgePill";
import { ProjectBreadcrumb } from "@/components/canvas/ProjectBreadcrumb";
import { LayoutModeSwitch } from "@/components/canvas/LayoutModeSwitch";
import { WorkbenchControl } from "@/components/canvas/WorkbenchControl";
import { CanvasStoreProvider, useCanvasStore } from "@/lib/canvas/store";
import { fetchCanvasConfig } from "@/lib/canvas/api";
import { useGitPendingCount } from "@/lib/canvas/useGitPending";
import { fetchSessions } from "@/lib/api";
import { useChat } from "@/hooks/useChat";
import { type AgentState } from "@/components/StatusPill";
import { useRealtime } from "@/lib/realtime/provider";

/**
 * /live - the unified workspace.
 *
 *   <AppShell>
 *     <SessionHeader>    chat-app name + chevron switcher + new + info
 *     <Workspace>        desktop = 3 cols, mobile = 3 modes (Chat / Files / Canvas)
 *
 * The old standalone /canvas route now redirects here. Meta surfaces that
 * used to be primary tabs (Skills / Heartbeat / Trust / Cron / Audit) are
 * still deep-linkable but live in the desktop overflow kebab and the
 * mobile drawer's "More" section. The Info button (next to the session
 * name) opens a read-only modal with Brain + Activity tabs that summarize
 * those surfaces inline.
 */
export default function LivePage() {
  // CanvasStoreProvider hosts the workspace's per-session canvas state
  // (file tabs, dirty paths, preview URL). It used to live only inside
  // /canvas; now it wraps the unified /live workspace.
  const envPreviewUrl = process.env.NEXT_PUBLIC_PREVIEW_URL ?? "";
  return (
    <CanvasStoreProvider envPreviewUrl={envPreviewUrl}>
      <LivePageInner />
    </CanvasStoreProvider>
  );
}

function LivePageInner() {
  const chat = useChat();
  const store = useCanvasStore();
  const [sessionName, setSessionName] = useState<string>("");

  // Hydrate workspace defaults (workspace root, preview URL fallback,
  // bridge reachability) from Core on mount. Once. The per-session
  // project_path effect in <Workspace> takes over from there.
  useEffect(() => {
    const ac = new AbortController();
    // No session yet at first paint; pass "". Workspace's per-session effect
    // re-fetches with the session id and re-asserts the bridge-aware root.
    fetchCanvasConfig("", ac.signal).then((cfg) => {
      if (!cfg) return;
      // Prefer the server-configured default project path (typically the
      // Jarvis repo) over the broader canvas root. This is the first-paint
      // value; <Workspace>'s per-session effect re-asserts it once the
      // session's project_path has been resolved.
      if (!store.root) {
        const initial = cfg.default_project_path || cfg.root;
        if (initial) store.setRoot(initial);
      }
      if (!store.previewUrl && cfg.preview_url) store.setPreviewUrl(cfg.preview_url);
      store.setBridgeOk(cfg.mac_bridge_ok);
    });
    return () => ac.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Pull the current session's name and keep it fresh via realtime.
  useEffect(() => {
    if (!chat.sessionId) {
      setSessionName("");
      return;
    }
    const ac = new AbortController();
    fetchSessions(ac.signal).then((rows) => {
      if (!rows) return;
      const me = rows.find((r) => r.id === chat.sessionId);
      setSessionName(me?.name ?? "");
    });
    return () => ac.abort();
  }, [chat.sessionId]);

  useRealtime("mem_sessions", async () => {
    if (!chat.sessionId) return;
    const rows = await fetchSessions();
    if (!rows) return;
    const me = rows.find((r) => r.id === chat.sessionId);
    setSessionName(me?.name ?? "");
  });

  const startedAt = useMemo(() => {
    const first = chat.messages[0];
    return first ? first.createdAt : null;
  }, [chat.messages]);

  const usedTokens = chat.usage.input + chat.usage.output;

  // One poll for the whole page. This count drives three surfaces - the
  // workbench door's badge, the Changes instrument's badge, and the commit
  // bar - and each of them used to run its own 4s `git status`. Hoisting it
  // here makes them agree with each other and costs a third of the requests.
  const changeCount = useGitPendingCount(store.root, chat.sessionId);

  const agentState: AgentState =
    chat.status === "disconnected"
      ? "offline"
      : chat.isStreaming
        ? "thinking"
        : chat.status === "connected"
          ? "listening"
          : "idle";

  return (
    <AppShell>
      <div className="flex min-h-0 flex-1 flex-col">
        <SessionHeader
          sessionId={chat.sessionId}
          sessionName={sessionName}
          startedAt={startedAt}
          onNew={chat.newSession}
          onClear={chat.clear}
          onSwitch={chat.switchSession}
          onRewind={undefined}
          agentState={agentState}
          extraActions={
            <>
              <ProjectBreadcrumb sessionId={chat.sessionId || null} />
              <BridgePill
                sessionId={chat.sessionId || null}
                onPreferenceChange={() => store.bumpBridgeEpoch()}
              />
              <LayoutModeSwitch />
            </>
          }
          pinnedActions={<WorkbenchControl changes={changeCount} />}
          actionChips={
            <InfoModal
              messages={chat.messages}
              usedTokens={usedTokens}
              wsConnected={chat.status === "connected"}
            />
          }
        />
        <Workspace chat={chat} changeCount={changeCount} />
      </div>
    </AppShell>
  );
}
