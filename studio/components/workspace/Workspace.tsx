"use client";

import { useEffect, useRef, useState } from "react";
import { Loader2 } from "lucide-react";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import { WorkspaceChatColumn } from "@/components/workspace/WorkspaceChatColumn";
import { WorkspaceFilesColumn } from "@/components/workspace/WorkspaceFilesColumn";
import { CanvasRightPane } from "@/components/canvas/CanvasRightPane";
import { WorkspaceMobile, useWorkspaceMode } from "@/components/workspace/WorkspaceMobile";
import { useCanvasStore } from "@/lib/canvas/store";
import {
  useCurrentProject,
  CurrentProjectProvider,
} from "@/lib/canvas/useCurrentProject";
import { useWebSocket } from "@/lib/ws/provider";
import {
  isCodeChangeTool,
  extractToolFilePaths,
  extractToolFilePath,
  extractToolPreview,
} from "@/lib/canvas/detection";
import { fetchCanvasConfig, fetchBridgeStatus } from "@/lib/canvas/api";
import type { useChat } from "@/hooks/useChat";

type ChatHook = ReturnType<typeof useChat>;

// Ceiling on auto-opened editor tabs per session. Each open file mounts a
// Monaco instance; past this the agent's edits still mark files dirty but
// don't auto-open, so a large refactor can't melt the browser.
const MAX_AUTO_OPEN_TABS = 8;

/**
 * Workspace - the unified /live surface that merges the old Live + Canvas
 * tabs into one work environment.
 *
 *   Desktop (lg+)   three horizontally-resizable columns:
 *                     [ Chat | Files / Git | Canvas (preview + editor tabs) ]
 *
 *   Mobile (<lg)    three full-bleed modes selected by sticky-top pills:
 *                     [ Chat | Files | Canvas ]
 *                   Swipe horizontally to cycle. Tapping a file in Files
 *                   auto-jumps to Canvas.
 *
 * Project = session: the file tree + preview re-scope to the current
 * session's `project_path`. Sessions without a project show empty Canvas
 * surfaces (handled inside CanvasFileTree / CanvasPreview empty states),
 * so chat-only sessions don't leak the workspace folder.
 */
export function Workspace({ chat }: { chat: ChatHook }) {
  const store = useCanvasStore();
  const ws = useWebSocket();
  const current = useCurrentProject();
  const { mode, setMode } = useWorkspaceMode("chat");

  // Server-configured fallback path (INFINITY_DEFAULT_PROJECT_PATH on
  // core). Sessions without their own project_path land here - typically
  // the Jarvis repo - so chat-only sessions don't sit in an empty "set
  // workspace root first" state. Also pre-warms the cloud bridge from
  // Railway App Sleeping (fire-and-forget).
  const [defaultProjectPath, setDefaultProjectPath] = useState<string>("");
  useEffect(() => {
    const ac = new AbortController();
    void (async () => {
      // Pass the session so Core returns a bridge-aware default: Cloud sessions
      // default to the /workspace volume (where fs_save lands), not the Mac repo.
      const cfg = await fetchCanvasConfig(chat.sessionId ?? "", ac.signal);
      if (cfg?.default_project_path) setDefaultProjectPath(cfg.default_project_path);
    })();
    void fetchBridgeStatus(ac.signal).catch(() => {});
    return () => ac.abort();
    // bridgeEpoch: re-fetch when the boss switches Mac ↔ Cloud so the default
    // root flips to that bridge's filesystem.
  }, [chat.sessionId, store.bridgeEpoch]);

  // Project = session lifecycle. When the active session changes its
  // project_path, re-scope the canvas store. When the session has no
  // project AND no configured default, blank the root so the file tree
  // shows the empty state. Wait for the initial fetch to complete so we
  // don't blow away a hydrating root on first render.
  const projectPath = current.session?.project_path?.trim() ?? "";
  useEffect(() => {
    if (current.loading) return;
    const next = projectPath || defaultProjectPath;
    if (next) {
      if (next !== store.root) {
        store.setRoot(next);
        store.closeAllFiles();
        store.clearDirty();
      }
    } else if (store.root) {
      // No session project AND no configured default - wipe.
      store.setRoot("");
      store.closeAllFiles();
      store.clearDirty();
    }
  }, [projectPath, defaultProjectPath, current.loading, store]);

  // As the agent edits files, mark them dirty AND auto-open each as an
  // editor tab in column 3, filtered by sessionId so a stale tab from
  // another session doesn't paint phantom changes or steal focus. This
  // mirrors the document_created -> openDocument path in CanvasRightPane:
  // the file being changed surfaces itself instead of waiting for a click.
  const toolPathsRef = useRef<Map<string, string[]>>(new Map());
  // Latest store / sessionId via refs so the WS subscription mounts ONCE and
  // never re-subscribes. Critical: the store identity changes on every
  // streamed token (liveContent updates), so depending on it here would tear
  // down and re-subscribe per token — dropping deltas and making the live
  // stream choppy. Refs keep the handler stable while always reading current
  // state.
  const storeRef = useRef(store);
  storeRef.current = store;
  const sessionIdRef = useRef(chat.sessionId);
  sessionIdRef.current = chat.sessionId;
  useEffect(() => {
    return ws.subscribe((ev) => {
      const store = storeRef.current;
      const sessionId = sessionIdRef.current;
      if (
        "session_id" in ev &&
        ev.session_id &&
        sessionId &&
        ev.session_id !== sessionId
      ) {
        return;
      }

      // Active project switched (agent ran project_open/create/clone) — re-scope
      // the canvas to the new project instantly instead of waiting on the 1.5s
      // session poll. Empty path = back to Jarvis's own code (the disk/self).
      if (ev.type === "project_changed") {
        const next = ev.project_changed.project_path?.trim() ?? "";
        if (next && next !== store.root) {
          store.setRoot(next);
          store.closeAllFiles();
          store.clearDirty();
        }
        return;
      }

      // Live token stream: the model is WRITING the tool args. Open the file
      // the instant its path is known and type the content in as it arrives,
      // so the boss watches Jarvis code in real time instead of staring at a
      // stale tool call. Gate to code-change tools when the name is known;
      // path extraction guards the rest (non-file tools yield no path).
      if (ev.type === "tool_input_delta") {
        const d = ev.tool_input_delta;
        if (d.name && !isCodeChangeTool(d.name)) return;
        store.pushToolInputDelta(d.id, d.name, d.delta);
        return;
      }

      // Tool finished: drop the live buffer so the tab reloads the
      // authoritative on-disk file (real diff + editable).
      if (ev.type === "tool_result") {
        const paths = toolPathsRef.current.get(ev.tool_result.id);
        if (paths) {
          // Authoritative edit location: fs_edit returns the exact start_line
          // it changed. Carry it so the diff reveals THAT line - no client-side
          // text guessing. Single-path edits only (multi-file pushes have none).
          if (paths.length === 1) {
            const line = parseStartLine(ev.tool_result.output);
            if (line > 0) store.markEditFocus(paths[0], line);
          }
          for (const p of paths) store.endLiveFile(p);
          toolPathsRef.current.delete(ev.tool_result.id);
        }
        return;
      }

      if (ev.type !== "tool_call") return;
      const name = ev.tool_call.name;
      if (!isCodeChangeTool(name)) return;
      // github__push_files carries multiple paths in one call - mark every
      // one so the Changes badge reflects the real fan-out. Auto-open each
      // as a tab so column 3 surfaces the file being changed; openFile
      // re-activates an already-open path, so the editor follows the most
      // recently touched file.
      //
      // Cap how many tabs we auto-open: every open file mounts a Monaco
      // instance (CanvasRightPane keeps all tabs mounted), so a 20+ file
      // refactor would spawn 20+ editors and choke the browser - badly on
      // mobile. Beyond the cap we still markDirty (the Files column shows
      // the dot), the boss just opens those by hand. We never evict an
      // existing tab, so an unsaved editor buffer can't be destroyed.
      const input = ev.tool_call.input;
      const paths = extractToolFilePaths(input);
      toolPathsRef.current.set(ev.tool_call.id, paths);
      const primaryPath = extractToolFilePath(input);
      const primaryContent = extractToolPreview(input);
      let openFileTabs = store.tabs.reduce(
        (n, t) => (t.kind === "file" ? n + 1 : n),
        0,
      );
      for (const path of paths) {
        store.markDirty(path);
        // Floor (model-agnostic): show the COMPLETE content the model wrote,
        // immediately. Covers providers that couldn't stream per-token deltas
        // and reconciles whatever did stream — the full input is authoritative
        // until tool_result reloads from disk. Only the primary single-file
        // path carries inline content; multi-file pushes open normally.
        if (path === primaryPath && primaryContent) {
          store.setPendingFile(path, primaryContent);
          continue;
        }
        const alreadyOpen = store.tabs.some(
          (t) => t.kind === "file" && t.path === path,
        );
        if (alreadyOpen) {
          store.openFile(path); // re-focus the file being changed
        } else if (openFileTabs < MAX_AUTO_OPEN_TABS) {
          store.openFile(path);
          openFileTabs += 1;
        }
      }
    });
  }, [ws]);

  // Auto-reveal the Canvas on MOBILE the instant the agent starts writing.
  // On desktop the canvas is always the 3rd column, but on the phone the boss
  // could be on the Chat pill when Jarvis begins coding and never see column 3
  // "pop up". The store opens the file tab regardless; here we fire the same
  // `workspace:set-mode` event the Files tap uses, but driven by the agent's
  // write - so the canvas surfaces itself. We trigger ONCE per coding burst
  // (first file tab appearing) and reset when all files close, so we never
  // yank the boss back to canvas if he deliberately switches to Chat mid-burst.
  // Desktop ignores `mode`, so this is a harmless no-op there.
  const hadFileTabRef = useRef(false);
  useEffect(() => {
    const hasFileTab = store.tabs.some((t) => t.kind === "file");
    if (hasFileTab && !hadFileTabRef.current) {
      hadFileTabRef.current = true;
      if (typeof window !== "undefined") {
        window.dispatchEvent(
          new CustomEvent("workspace:set-mode", { detail: { mode: "canvas" } }),
        );
      }
    } else if (!hasFileTab) {
      hadFileTabRef.current = false;
    }
  }, [store.tabs]);

  // Mount gate - react-resizable-panels reads localStorage on first paint
  // so SSR vs CSR diverge; render a stable skeleton until the client takes
  // over.
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  if (!mounted) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center" suppressHydrationWarning>
        <Loader2 className="size-5 animate-spin text-muted-foreground" aria-hidden />
      </div>
    );
  }

  return (
    <CurrentProjectProvider value={current}>
      {/* Desktop - three horizontally-resizable columns */}
      <div className="hidden min-h-0 flex-1 lg:flex">
        {/* No autoSaveId - column widths reset to defaults on every refresh.
            Dragging the dividers still works in-session, but a reload always
            returns to the 25 / 18 / 57 layout. The boss explicitly wants the
            workspace to feel "fresh" on each visit instead of accumulating
            stuck layouts from random drag sessions. */}
        <ResizablePanelGroup direction="horizontal">
          <ResizablePanel defaultSize={25} minSize={15} maxSize={50}>
            <div className="flex h-full min-h-0 flex-col border-r bg-muted/30 dark:bg-zinc-900/40">
              <WorkspaceChatColumn chat={chat} />
            </div>
          </ResizablePanel>
          <ResizableHandle />
          <ResizablePanel defaultSize={18} minSize={12} maxSize={40}>
            <div className="flex h-full min-h-0 flex-col border-r bg-muted/20 dark:bg-zinc-900/30">
              <WorkspaceFilesColumn sessionId={chat.sessionId} />
            </div>
          </ResizablePanel>
          <ResizableHandle />
          <ResizablePanel defaultSize={57} minSize={30}>
            <div className="flex h-full min-h-0 flex-col">
              <CanvasRightPane chat={chat} />
            </div>
          </ResizablePanel>
        </ResizablePanelGroup>
      </div>

      {/* Mobile - single-column with mode pills */}
      <div className="flex min-h-0 flex-1 flex-col lg:hidden">
        <WorkspaceMobile chat={chat} mode={mode} onModeChange={setMode} />
      </div>
    </CurrentProjectProvider>
  );
}

// parseStartLine pulls the authoritative `start_line` the backend's fs_edit
// reports out of the tool-result output (which is the bridge's JSON, possibly
// prefixed with "[bridge=cloud] "). Regex so it's robust to the prefix/envelope.
function parseStartLine(output: string | undefined): number {
  if (!output) return 0;
  const m = output.match(/"start_line"\s*:\s*(\d+)/);
  if (!m) return 0;
  const n = parseInt(m[1], 10);
  return Number.isFinite(n) && n > 0 ? n : 0;
}
