"use client";

import { useEffect, useMemo, useRef } from "react";
import { MonitorPlay, X, Lock, FileText, SquareTerminal, Sparkles } from "lucide-react";
import { CanvasPreview } from "@/components/canvas/CanvasPreview";
import { CanvasTerminal } from "@/components/canvas/CanvasTerminal";
import { CanvasMediaGallery } from "@/components/canvas/CanvasMediaGallery";
import { CanvasFileTab } from "@/components/canvas/CanvasFileTab";
import { DocumentTab } from "@/components/canvas/DocumentTab";
import { useCanvasStore, docMetaFromArtifact, takePendingDoc } from "@/lib/canvas/store";
import { useSessionArtifacts } from "@/lib/canvas/useSessionArtifacts";
import { useRuns } from "@/lib/runs/useRuns";
import { useWebSocket } from "@/lib/ws/provider";
import { cn } from "@/lib/utils";
import type { DocArtifact } from "@/lib/api";
import type { useChat } from "@/hooks/useChat";

type ChatHook = ReturnType<typeof useChat>;

// Open document tabs persist per session so they survive refresh / device
// switch — the boss's "my tabs shouldn't vanish unless I click X" rule. The
// files themselves are server-tracked (mem_artifacts); this localStorage set
// only remembers WHICH were open (view state), keyed by session.
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
    /* ignore */
  }
}

/**
 * CanvasRightPane - VS Code-style tabbed editor group.
 *
 * Tab 1 is always Preview (pinned, non-closable). Subsequent tabs are open
 * files, ordered by open time. Active-tab content renders below the tab
 * strip. We keep all tabs mounted-but-hidden so switching between Preview
 * and a Monaco file doesn't unmount Monaco (preserves cursor position,
 * scroll, and avoids a re-init flicker on every tab click).
 */
export function CanvasRightPane({ chat }: { chat: ChatHook }) {
  const store = useCanvasStore();
  const ws = useWebSocket();
  const stripRef = useRef<HTMLDivElement>(null);

  // Session repository: every generated document (server-tracked) + every
  // generated media asset. CanvasRightPane is the single data owner — it
  // rehydrates the open doc tabs, drives the Media tab count badge, and feeds
  // the gallery (which is a pure renderer).
  const { artifacts: docArtifacts, loading: docsLoading, refresh: refreshDocs } = useSessionArtifacts(chat.sessionId);
  const { runs: mediaRuns } = useRuns({
    kind: "media.generate",
    targetId: chat.sessionId || undefined,
    limit: 50,
  });

  const mediaCount = useMemo(
    () =>
      mediaRuns.reduce(
        (n, r) => n + (r.status !== "running" && r.status !== "error" ? r.meta?.media?.length ?? 0 : 0),
        0,
      ),
    [mediaRuns],
  );
  const galleryCount = docArtifacts.length + mediaCount;

  // A generated document opens in a NEW tab (rendered report / download).
  // Cloud-first: markdown rides the event; binaries fetch via the
  // cloud-direct proxy. Filter to this chat session.
  useEffect(() => {
    return ws.subscribe((ev) => {
      if (ev.type !== "document_created") return;
      if (chat.sessionId && ev.session_id && ev.session_id !== chat.sessionId) return;
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
    });
  }, [ws, store, chat.sessionId]);

  // Pending-doc handoff: a document opened from outside the canvas (the
  // dashboard's Saved card) stashes its DocMeta and routes here. Drain it once
  // on mount and open the tab focused, so the boss lands ON the document rather
  // than at the Library root. Runs independent of session — the DocMeta carries
  // everything DocumentTab needs (path + preview fields).
  const pendingDrainedRef = useRef(false);
  useEffect(() => {
    if (pendingDrainedRef.current) return;
    pendingDrainedRef.current = true;
    const doc = takePendingDoc();
    if (doc) store.openDocument(doc);
  }, [store]);

  // ── Open-tab persistence (the vanishing-tabs fix) ─────────────────────────
  // Rehydrate ONCE per session, after the artifact list is available: reopen
  // exactly the tabs that were open (closed-by-X tabs aren't in the set).
  const rehydratedRef = useRef<string>("");
  useEffect(() => {
    const sid = chat.sessionId;
    if (!sid || docsLoading) return;
    if (rehydratedRef.current === sid) return;
    rehydratedRef.current = sid; // mark rehydrated even if nothing to restore
    const { openIds, activeId } = readOpenDocSet(sid);
    if (openIds.length === 0) return;
    const byPath = new Map(docArtifacts.map((a) => [a.path, a] as const));
    const docs = openIds
      .map((p) => byPath.get(p))
      .filter((a): a is DocArtifact => !!a)
      .map(docMetaFromArtifact);
    if (docs.length) store.restoreDocuments(docs, activeId);
  }, [chat.sessionId, docsLoading, docArtifacts, store]);

  // Persist the open set whenever tabs change — but only AFTER rehydration, so
  // the empty initial state doesn't clobber the saved set on mount.
  useEffect(() => {
    const sid = chat.sessionId;
    if (!sid || rehydratedRef.current !== sid) return;
    const openIds = store.documents.map((d) => d.id);
    const activeId = store.documents.some((d) => d.id === store.activeTabId)
      ? store.activeTabId
      : undefined;
    writeOpenDocSet(sid, openIds, activeId);
  }, [chat.sessionId, store.documents, store.activeTabId]);

  // When the active tab changes, scroll it into view in the strip - mobile
  // and small desktop widths can clip the strip horizontally.
  useEffect(() => {
    const strip = stripRef.current;
    if (!strip) return;
    const el = strip.querySelector<HTMLElement>(`[data-tab-id="${cssAttr(store.activeTabId)}"]`);
    if (el && typeof el.scrollIntoView === "function") {
      el.scrollIntoView({ block: "nearest", inline: "nearest", behavior: "smooth" });
    }
  }, [store.activeTabId]);

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      {/* Tab strip */}
      <div
        ref={stripRef}
        className="flex h-10 items-center gap-0.5 overflow-x-auto scroll-touch border-b bg-muted/20 px-1 dark:bg-zinc-900/40"
        role="tablist"
        aria-label="Canvas tabs"
      >
        {store.tabs.map((tab) => {
          const isActive = tab.id === store.activeTabId;
          if (tab.kind === "preview") {
            return (
              <button
                key={tab.id}
                data-tab-id={tab.id}
                type="button"
                role="tab"
                aria-selected={isActive}
                onClick={() => store.setActiveTabId(tab.id)}
                className={cn(
                  "group inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium transition-colors",
                  isActive
                    ? "bg-background text-foreground shadow-sm"
                    : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                )}
                title="Live preview (pinned)"
              >
                <MonitorPlay className="size-3.5" />
                <span>Preview</span>
                <Lock className="size-2.5 text-muted-foreground/60" aria-hidden />
              </button>
            );
          }
          if (tab.kind === "terminal") {
            return (
              <button
                key={tab.id}
                data-tab-id={tab.id}
                type="button"
                role="tab"
                aria-selected={isActive}
                onClick={() => store.setActiveTabId(tab.id)}
                className={cn(
                  "group inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium transition-colors",
                  isActive
                    ? "bg-background text-foreground shadow-sm"
                    : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                )}
                title="Terminal - your shell + Jarvis's commands (pinned)"
              >
                <SquareTerminal className="size-3.5" />
                <span>Terminal</span>
                <Lock className="size-2.5 text-muted-foreground/60" aria-hidden />
              </button>
            );
          }
          if (tab.kind === "media") {
            return (
              <button
                key={tab.id}
                data-tab-id={tab.id}
                type="button"
                role="tab"
                aria-selected={isActive}
                onClick={() => store.setActiveTabId(tab.id)}
                className={cn(
                  "group inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium transition-colors",
                  isActive
                    ? "bg-background text-foreground shadow-sm"
                    : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                )}
                title="Media - everything Jarvis made this session: images, video, documents (pinned)"
              >
                <Sparkles className="size-3.5" />
                <span>Media</span>
                {galleryCount > 0 && (
                  <span className="ml-0.5 rounded-full bg-muted px-1.5 text-[10px] font-semibold tabular-nums text-muted-foreground/90">
                    {galleryCount}
                  </span>
                )}
                <Lock className="size-2.5 text-muted-foreground/60" aria-hidden />
              </button>
            );
          }
          if (tab.kind === "document") {
            return (
              <div
                key={tab.id}
                data-tab-id={tab.id}
                role="tab"
                aria-selected={isActive}
                className={cn(
                  "group inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md text-xs font-medium transition-colors",
                  isActive
                    ? "bg-background text-foreground shadow-sm"
                    : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                )}
              >
                <button
                  type="button"
                  onClick={() => store.setActiveTabId(tab.id)}
                  className="inline-flex h-8 items-center gap-1.5 pl-2.5 pr-1"
                  title={tab.filename}
                >
                  <FileText className="size-3.5" />
                  <span className="max-w-[160px] truncate">{tab.filename}</span>
                </button>
                <button
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation();
                    store.closeDocument(tab.id);
                  }}
                  className="mr-1 inline-flex size-5 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
                  aria-label={`Close ${tab.filename}`}
                  title="Close"
                >
                  <X className="size-3" />
                </button>
              </div>
            );
          }
          const basename = tab.path.replace(/^.*[\\/]/, "");
          const isDirty = store.dirtyPaths.has(tab.path);
          return (
            <div
              key={tab.id}
              data-tab-id={tab.id}
              role="tab"
              aria-selected={isActive}
              className={cn(
                "group inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md text-xs font-medium transition-colors",
                isActive
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
              )}
            >
              <button
                type="button"
                onClick={() => store.setActiveTabId(tab.id)}
                className="inline-flex h-8 items-center gap-1.5 pl-2.5 pr-1"
                title={tab.path}
              >
                {isDirty && (
                  <span
                    className="size-1.5 shrink-0 rounded-full bg-warning"
                    aria-label="modified this session"
                  />
                )}
                <span className="max-w-[160px] truncate">{basename}</span>
              </button>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  store.closeFile(tab.id);
                }}
                className="mr-1 inline-flex size-5 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
                aria-label={`Close ${basename}`}
                title="Close"
              >
                <X className="size-3" />
              </button>
            </div>
          );
        })}
      </div>

      {/* Content area - every tab is kept mounted to avoid Monaco re-init. */}
      <div className="relative min-h-0 flex-1">
        <div
          className={cn(
            "absolute inset-0 transition-opacity",
            store.activeTabId === "preview" ? "opacity-100" : "pointer-events-none opacity-0",
          )}
          aria-hidden={store.activeTabId !== "preview"}
        >
          <CanvasPreview sessionId={chat.sessionId} />
        </div>
        {/* Terminal kept mounted so scrollback + Jarvis's command feed survive
            tab switches (same reason Preview/Monaco stay mounted). */}
        <div
          className={cn(
            "absolute inset-0 transition-opacity",
            store.activeTabId === "terminal" ? "opacity-100" : "pointer-events-none opacity-0",
          )}
          aria-hidden={store.activeTabId !== "terminal"}
        >
          <CanvasTerminal sessionId={chat.sessionId} />
        </div>
        {/* Media gallery kept mounted so the useRuns subscription + in-flight
            spinners persist across tab switches (same reason as Preview). */}
        <div
          className={cn(
            "absolute inset-0 transition-opacity",
            store.activeTabId === "media" ? "opacity-100" : "pointer-events-none opacity-0",
          )}
          aria-hidden={store.activeTabId !== "media"}
        >
          <CanvasMediaGallery documents={docArtifacts} mediaRuns={mediaRuns} loading={docsLoading} onRefresh={refreshDocs} />
        </div>
        {store.tabs.map((tab) => {
          if (tab.kind !== "file") return null;
          const isActive = store.activeTabId === tab.id;
          return (
            <div
              key={tab.id}
              className={cn(
                "absolute inset-0 transition-opacity",
                isActive ? "opacity-100" : "pointer-events-none opacity-0",
              )}
              aria-hidden={!isActive}
            >
              <CanvasFileTab
                path={tab.path}
                isActive={isActive}
                sessionId={chat.sessionId}
              />
            </div>
          );
        })}
        {store.documents.map((doc) => {
          const isActive = store.activeTabId === doc.id;
          return (
            <div
              key={doc.id}
              className={cn(
                "absolute inset-0 transition-opacity",
                isActive ? "opacity-100" : "pointer-events-none opacity-0",
              )}
              aria-hidden={!isActive}
            >
              <DocumentTab doc={doc} />
            </div>
          );
        })}
      </div>
    </div>
  );
}

function cssAttr(s: string) {
  // CSS attribute selectors don't tolerate ":" without escaping. file tab
  // IDs are "file:/abs/path" - escape the colon so querySelector finds them.
  return s.replace(/:/g, "\\:");
}
