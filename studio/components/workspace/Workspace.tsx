"use client";

import { useEffect, useRef, useState } from "react";
import { Loader2 } from "lucide-react";
import { WorkspaceChatColumn } from "@/components/workspace/WorkspaceChatColumn";
import { WorkbenchPane } from "@/components/canvas/WorkbenchPane";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { useIsDesktop } from "@/lib/use-media-query";
import { useSessionArtifacts } from "@/lib/canvas/useSessionArtifacts";
import { useRuns } from "@/lib/runs/useRuns";
import {
  docMetaFromArtifact,
  takePendingDoc,
  useCanvasStore,
  type LayoutMode,
} from "@/lib/canvas/store";
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
import { cn } from "@/lib/utils";
import type { DocArtifact } from "@/lib/api";
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
export function Workspace({
  chat,
  changeCount,
}: {
  chat: ChatHook;
  /** Files differing from HEAD. Polled once by the page and handed down, so
   *  the door badge, the Changes instrument and the commit bar agree. */
  changeCount: number;
}) {
  const store = useCanvasStore();
  const ws = useWebSocket();
  const current = useCurrentProject();

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

  // Documents = session lifecycle. A generated document belongs to the
  // conversation that produced it, but the canvas store outlives any one
  // session (CanvasStoreProvider wraps /live, above useChat), so a new chat
  // inherits the previous chat's open document tabs unless something evicts
  // them. Nothing did.
  //
  // This lives here rather than in <CanvasRightPane> because THIS component is
  // always mounted — the right pane is unmounted whenever the boss is on the
  // mobile Chat pill, which is exactly when he starts a new conversation. The
  // pane's rehydration effect then refills the set from the new session's
  // artifacts (empty for a fresh chat), so both halves agree.
  //
  // Skip the ""→sid transition: that is first-mount hydration, not a switch.
  const prevSessionRef = useRef<string>("");
  useEffect(() => {
    const sid = chat.sessionId;
    if (prevSessionRef.current === sid) return;
    const switched = prevSessionRef.current !== "";
    prevSessionRef.current = sid;
    if (switched) store.restoreDocuments([]);
  }, [chat.sessionId, store]);

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

      // A document_create finished: open it as its OWN document tab (with the
      // sibling PDF for download) here at the ALWAYS-MOUNTED seam. This used to
      // live only in CanvasRightPane, which is UNMOUNTED on mobile whenever the
      // boss is on the Chat pill — so on the phone the report never popped a tab
      // and its PDF download went missing (he found it only via the Media
      // count). openDocument dedupes by id, so this + rehydration never double-
      // add. The mobile auto-reveal below (widened to `document`) then surfaces
      // the Canvas once per burst, exactly like a code edit does.
      if (ev.type === "document_created") {
        const d = ev.document_created;
        store.openDocument({
          id: d.path,
          filename: d.filename,
          format: d.format,
          path: d.path,
          bytes: d.bytes,
          markdown: d.markdown,
          pdfPath: d.pdf_path,
          htmlPath: d.html_path,
        });
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
    // Files AND documents both auto-reveal the Canvas — a generated report/doc
    // should pop the pane on the phone exactly like a code edit does.
    const hasFileTab = store.tabs.some(
      (t) => t.kind === "file" || t.kind === "document",
    );
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

  // ── Doc + media ownership ────────────────────────────────────────────────
  // These used to live inside the right pane, which is unmounted whenever the
  // boss is on a phone looking at the conversation — which is exactly when a
  // document finishes. Owning them HERE, at the always-mounted seam, is what
  // stops a generated report losing its tab and its PDF on the phone.
  const {
    artifacts: docArtifacts,
    loading: docsLoading,
    forSession: docsForSession,
    refresh: refreshDocs,
  } = useSessionArtifacts(chat.sessionId);
  const { runs: mediaRuns } = useRuns({
    kind: "media.generate",
    targetId: chat.sessionId,
    limit: 50,
    enabled: !!chat.sessionId,
  });
  const isDesktop = useIsDesktop();

  // Rehydrate the open document tabs once per session from that session's own
  // artifacts. `restoreDocuments` is called unconditionally: an empty set is
  // the correct answer for a fresh conversation, and skipping the call is what
  // used to strand the previous session's tabs on screen.
  const rehydratedRef = useRef<string>("");
  useEffect(() => {
    const sid = chat.sessionId;
    if (!sid || docsForSession !== sid) return;
    if (rehydratedRef.current === sid) return;
    rehydratedRef.current = sid;
    const { openIds, activeId } = readOpenDocSet(sid);
    const byPath = new Map(docArtifacts.map((a) => [a.path, a] as const));
    const docs = openIds
      .map((p) => byPath.get(p))
      .filter((a): a is DocArtifact => !!a)
      .map(docMetaFromArtifact);
    store.restoreDocuments(docs, activeId);
    const pending = takePendingDoc();
    if (pending) store.openDocument(pending);
  }, [chat.sessionId, docsForSession, docArtifacts, store]);

  // Persist which tabs are open, AFTER rehydration, so neither the empty
  // initial state nor a mid-switch state clobbers the saved set.
  useEffect(() => {
    const sid = chat.sessionId;
    if (!sid || rehydratedRef.current !== sid) return;
    writeOpenDocSet(sid, store.documents.map((d) => d.id), store.activeTabId);
  }, [chat.sessionId, store.documents, store.activeTabId]);

  // ── The layout moves itself ──────────────────────────────────────────────
  // Five rules, and a sixth that makes them safe: one deliberate move by the
  // boss turns all of this off for the session (enforced inside the store's
  // `suggestLayout`, which is a no-op once `layoutAuto` is false).
  const hadWorkRef = useRef(false);
  useEffect(() => {
    const hasWork = store.tabs.some((t) => t.kind === "file" || t.kind === "document");
    if (hasWork && !hadWorkRef.current) {
      // He started writing: open beside the conversation.
      hadWorkRef.current = true;
      store.suggestLayout("split");
    } else if (!hasWork && hadWorkRef.current) {
      // Nothing open any more: the conversation is the page again.
      hadWorkRef.current = false;
      store.suggestLayout("chat");
    }
  }, [store]);

  useEffect(() => {
    // The preview rebuilt while he is still working: put the browser in front.
    if (store.previewRefreshKey > 0) store.suggestLayout("build");
  }, [store.previewRefreshKey, store]);

  const mediaCountRef = useRef(0);
  useEffect(() => {
    const n = mediaRuns.reduce(
      (acc, r) => acc + (r.status === "ok" ? (r.meta?.media?.length ?? 0) : 0),
      0,
    );
    // Something finished rendering: you should never have to go looking for a
    // thing he just made.
    if (n > mediaCountRef.current && mediaCountRef.current > 0) store.suggestLayout("build");
    mediaCountRef.current = n;
  }, [mediaRuns, store]);

  // Mount gate: the layout reads client-only state, so render a stable
  // skeleton until the client takes over rather than mismatching on hydrate.
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  if (!mounted) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center" suppressHydrationWarning>
        <Loader2 className="size-5 animate-spin text-muted-foreground" aria-hidden />
      </div>
    );
  }

  const open = store.layout !== "chat";
  const pane = (
    <WorkbenchPane
      sessionId={chat.sessionId}
      documents={docArtifacts}
      mediaRuns={mediaRuns}
      docsLoading={docsLoading}
      onRefreshDocs={refreshDocs}
      onDiscussDoc={(doc) =>
        void chat.send(
          `I want to discuss the document **${doc.filename}**. Open and read it from \`${doc.path}\` for context, then ask me what I would like to do with it.`,
        )
      }
      changeCount={changeCount}
      onClose={() => store.setLayout("chat")}
    />
  );

  const chatColumn = (
    <WorkspaceChatColumn
      chat={chat}
      changeCount={changeCount}
      minimalComposer={store.layout === "build"}
    />
  );

  return (
    <CurrentProjectProvider value={current}>
      {/* Desktop: one grid whose template IS the layout mode. 180ms so the
          change reads as a movement rather than a jump cut. */}
      <div
        className={cn(
          "hidden min-h-0 min-w-0 flex-1 lg:grid",
          "transition-[grid-template-columns] duration-layout ease-out motion-reduce:transition-none",
        )}
        style={{ gridTemplateColumns: templateFor(store.layout) }}
      >
        <div className="flex h-full min-h-0 min-w-0 flex-col">{chatColumn}</div>
        {open ? (
          <div className="flex h-full min-h-0 min-w-0 flex-col border-l border-hairline">
            {pane}
          </div>
        ) : (
          <div className="min-w-0" />
        )}
      </div>

      {/* Phone: the conversation is the page and the workbench is a sheet you
          pull up. No mode pills taxing every screen — it still opens itself
          the first time he writes a file, which is the behaviour that
          mattered. */}
      <div className="flex min-h-0 min-w-0 flex-1 flex-col lg:hidden">
        {chatColumn}
        {!isDesktop ? (
          <ResponsiveModal
            open={open}
            onOpenChange={(o) => store.setLayout(o ? "split" : "chat")}
            title="Workbench"
            size="lg"
            contentClassName="p-0"
          >
            <div className="flex h-[70dvh] min-h-0 flex-col">{pane}</div>
          </ResponsiveModal>
        ) : null}
      </div>
    </CurrentProjectProvider>
  );
}

/**
 * The three widths, as grid templates.
 *
 *   chat   the conversation is the page
 *   split  42 / 58 — reading a diff while you talk
 *   build  a 286px chat rail and the rest to the workbench: the conversation
 *          becomes a running commentary and the browser gets real room
 */
function templateFor(mode: LayoutMode): string {
  if (mode === "build") return "286px minmax(0,1fr)";
  if (mode === "split") return "42% minmax(0,1fr)";
  return "minmax(0,1fr) 0px";
}

// Which document tabs were open, per session. The documents themselves are
// server-tracked (mem_artifacts); this is view state only.
const OPEN_DOCS_PREFIX = "infinity:canvas:opendocs:";

function readOpenDocSet(sid: string): { openIds: string[]; activeId?: string } {
  if (typeof window === "undefined") return { openIds: [] };
  try {
    const raw = window.localStorage.getItem(OPEN_DOCS_PREFIX + sid);
    if (!raw) return { openIds: [] };
    const p = JSON.parse(raw) as { openIds?: string[]; activeId?: string };
    return {
      openIds: Array.isArray(p.openIds) ? p.openIds.filter((x) => typeof x === "string") : [],
      activeId: typeof p.activeId === "string" ? p.activeId : undefined,
    };
  } catch {
    return { openIds: [] };
  }
}

function writeOpenDocSet(sid: string, openIds: string[], activeId?: string) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(OPEN_DOCS_PREFIX + sid, JSON.stringify({ openIds, activeId }));
  } catch {
    /* private mode / quota — view state only, safe to lose */
  }
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
