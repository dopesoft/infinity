"use client";

/**
 * CanvasStore - shared client state for the Canvas surface.
 *
 * Why a context and not URL params: the Canvas page is one app shell with
 * many internal sub-views (file tree, git, preview, editor tabs, composer).
 * Routing every selection through the URL would make tab-switching feel
 * sluggish, break the back button (every file open would push a history
 * entry), and complicate sharing state with the persistent composer.
 *
 * What's persisted (localStorage):
 *   - infinity:canvas:root         workspace root path
 *   - infinity:canvas:previewUrl   override for the preview iframe src
 *   - infinity:canvas:device       mobile | tablet | desktop
 *   - infinity:canvas:tabs         {openPaths, activeIndex} restore on refresh
 *
 * What's transient (in-memory only):
 *   - dirtyPaths    files modified in this session (from WS tool calls)
 *   - bridgeOk      whether the Mac MCP bridge is currently reachable
 *   - status banner shown when first tool call lands
 */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { extractStreamingContent, extractStreamingPath } from "./streaming";
import type { DocArtifact } from "@/lib/api";

export type DevicePreset = "mobile" | "tablet" | "desktop";

export type CanvasTabKind = "preview" | "terminal" | "media" | "file" | "document";

export type CanvasTab =
  | { kind: "preview"; id: "preview" }
  | { kind: "terminal"; id: "terminal" }
  | { kind: "media"; id: "media" }
  | { kind: "file"; id: string; path: string }
  | { kind: "document"; id: string; filename: string; format: string; path: string };

// DocMeta is a generated document opened in a Studio tab. markdown rides the
// ws event for reports (rendered inline); binaries (xlsx/docx/pptx) download
// via the cloud-direct proxy keyed on `path`.
export type DocMeta = {
  id: string; // = path
  filename: string;
  format: string;
  path: string;
  bytes?: number;
  markdown?: string;
  pdfPath?: string;
};

// docMetaFromArtifact maps a server-tracked DocArtifact (mem_artifacts row) to
// the DocMeta a tab renders. Shared by the right-pane (rehydration) and the
// gallery (click-to-open) so the mapping lives in exactly one place.
export function docMetaFromArtifact(a: DocArtifact): DocMeta {
  return {
    id: a.path, // DocMeta.id === path, matching the document_created tab convention
    filename: a.filename,
    format: a.format,
    path: a.path,
    bytes: a.bytes,
    markdown: a.markdown,
    pdfPath: a.pdf_path,
  };
}

type Persisted = {
  root: string;
  previewUrl: string;
  device: DevicePreset;
  openPaths: string[];
  activeIndex: number;
};

const ROOT_KEY = "infinity:canvas:root";
const PREVIEW_KEY = "infinity:canvas:previewUrl";
const DEVICE_KEY = "infinity:canvas:device";
const TABS_KEY = "infinity:canvas:tabs";
const RIGHT_MODE_KEY = "infinity:canvas:rightMode";

function readPersisted(): Persisted {
  if (typeof window === "undefined") {
    return { root: "", previewUrl: "", device: "desktop", openPaths: [], activeIndex: 0 };
  }
  const root = window.localStorage.getItem(ROOT_KEY) ?? "";
  const previewUrl = window.localStorage.getItem(PREVIEW_KEY) ?? "";
  const deviceRaw = (window.localStorage.getItem(DEVICE_KEY) ?? "desktop").toLowerCase();
  const device: DevicePreset =
    deviceRaw === "mobile" || deviceRaw === "tablet" || deviceRaw === "desktop"
      ? (deviceRaw as DevicePreset)
      : "desktop";
  let openPaths: string[] = [];
  let activeIndex = 0;
  try {
    const raw = window.localStorage.getItem(TABS_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as { openPaths?: string[]; activeIndex?: number };
      if (Array.isArray(parsed.openPaths)) openPaths = parsed.openPaths.filter((p) => typeof p === "string");
      if (typeof parsed.activeIndex === "number") activeIndex = parsed.activeIndex;
    }
  } catch {
    /* ignore */
  }
  return { root, previewUrl, device, openPaths, activeIndex };
}

type CanvasStoreValue = {
  // Workspace
  root: string;
  setRoot: (next: string) => void;
  bridgeOk: boolean;
  setBridgeOk: (ok: boolean) => void;

  // Preview
  previewUrl: string;
  setPreviewUrl: (next: string) => void;
  envPreviewUrl: string;
  device: DevicePreset;
  setDevice: (next: DevicePreset) => void;
  previewRefreshKey: number;
  refreshPreview: () => void;

  // Tabs
  tabs: CanvasTab[];
  activeTabId: string;
  setActiveTabId: (id: string) => void;
  openFile: (path: string) => void;
  closeFile: (id: string) => void;
  closeOthers: (id: string) => void;
  closeAllFiles: () => void;
  rightMode: "preview" | "file"; // current focus
  setRightMode: (m: "preview" | "file") => void;

  // Live cloud browser. When a screencast frame arrives, browserActive
  // flips on and the PREVIEW tab takes over to show the live stream (we
  // reuse Preview rather than spawning a second tab). It flips off when the
  // session closes / the boss stops it. browserSessionId is the live
  // session id (for the Stop button + RunIndicator). Transient — a live
  // session re-announces itself via incoming frames after a refresh.
  browserActive: boolean;
  setBrowserActive: (on: boolean) => void;
  browserSessionId: string;
  setBrowserSessionId: (id: string) => void;

  // Generated documents (each opens as its own closeable tab).
  documents: DocMeta[];
  openDocument: (doc: DocMeta) => void;
  closeDocument: (id: string) => void;
  // Rehydrate open doc tabs from server state (survives refresh / device).
  restoreDocuments: (docs: DocMeta[], activeId?: string) => void;

  // Dirty tracking
  dirtyPaths: Set<string>;
  markDirty: (path: string) => void;
  clearDirty: () => void;

  // Bumped whenever the session's bridge preference changes (Mac ↔ Cloud ↔
  // auto). Consumers that derive the workspace root or list files key off this
  // so switching the bridge re-roots the tree to that bridge's filesystem
  // (/workspace for Cloud, the Mac repo for Mac) and refreshes immediately.
  bridgeEpoch: number;
  bumpBridgeEpoch: () => void;

  // Live file content — what the canvas file tab renders instead of the disk
  // read while present, so the boss watches a file fill in AS Jarvis writes it.
  // Populated two ways: pushToolInputDelta (token-by-token, while the model
  // streams the tool args) and setPendingFile (the full content from a
  // completed tool_call — the model-agnostic floor for providers that don't
  // stream tool args). liveStreaming marks paths still actively receiving
  // deltas, so the editor stays read-only and auto-scrolls. endLiveFile drops
  // the buffer on tool_result so the tab reloads the authoritative disk file.
  liveContent: Map<string, string>;
  liveStreaming: Set<string>;
  lastLive: Map<string, string>;
  editFocus: Map<string, number>;
  markEditFocus: (path: string, line: number) => void;
  pushToolInputDelta: (toolId: string, name: string, delta: string) => void;
  setPendingFile: (path: string, content: string) => void;
  endLiveFile: (path: string) => void;
};

const CanvasStoreContext = createContext<CanvasStoreValue | null>(null);

function fileTabId(path: string) {
  return `file:${path}`;
}

export function CanvasStoreProvider({
  children,
  envPreviewUrl = "",
  initialRoot = "",
}: {
  children: React.ReactNode;
  envPreviewUrl?: string;
  initialRoot?: string;
}) {
  // SSR-safe: start with neutral defaults, hydrate from localStorage on mount.
  const [root, setRootInternal] = useState<string>(initialRoot);
  const [previewUrl, setPreviewUrlInternal] = useState<string>("");
  const [device, setDeviceInternal] = useState<DevicePreset>("desktop");
  const [previewRefreshKey, setRefreshKey] = useState(0);
  const [bridgeOk, setBridgeOk] = useState(false);
  const [dirtyPaths, setDirtyPaths] = useState<Set<string>>(() => new Set());
  const [bridgeEpoch, setBridgeEpoch] = useState(0);
  const [liveContent, setLiveContent] = useState<Map<string, string>>(() => new Map());
  const [liveStreaming, setLiveStreaming] = useState<Set<string>>(() => new Set());
  // lastLive remembers the most recent thing Jarvis WROTE into each file this
  // session (a full-write's content, or an edit's new text). Unlike
  // liveContent it survives endLiveFile, so after the file flips to its disk
  // diff the editor can jump to the section that was just edited - even on the
  // 2nd/3rd edit to the same file.
  const [lastLive, setLastLive] = useState<Map<string, string>>(() => new Map());
  // editFocus is the AUTHORITATIVE 1-based line where the backend applied the
  // most recent edit to each file (start_line from fs_edit). The diff reveals
  // exactly this line - no client-side text matching. Set from the tool result.
  const [editFocus, setEditFocus] = useState<Map<string, number>>(() => new Map());
  const markEditFocus = useCallback((path: string, line: number) => {
    if (!path || !(line > 0)) return;
    setEditFocus((prev) => {
      const next = new Map(prev);
      next.set(path, line);
      return next;
    });
  }, []);
  // Per-tool-id raw partial-JSON accumulation. A ref (not state) because deltas
  // are high-frequency and we only re-render when extracted path/content change.
  const toolRawRef = useRef<Map<string, { raw: string; path: string | null; name: string }>>(new Map());
  const [rightMode, setRightModeInternal] = useState<"preview" | "file">("preview");
  const [browserActive, setBrowserActive] = useState(false);
  const [browserSessionId, setBrowserSessionId] = useState("");
  const [documents, setDocuments] = useState<DocMeta[]>([]);

  // openPaths is the source of truth for non-preview tabs.
  const [openPaths, setOpenPaths] = useState<string[]>([]);
  const [activeTabId, setActiveTabIdInternal] = useState<string>("preview");
  const hydratedRef = useRef(false);

  // Hydrate from localStorage once, client-side.
  useEffect(() => {
    if (hydratedRef.current) return;
    hydratedRef.current = true;
    const persisted = readPersisted();
    if (persisted.root && !root) setRootInternal(persisted.root);
    if (persisted.previewUrl) setPreviewUrlInternal(persisted.previewUrl);
    setDeviceInternal(persisted.device);
    setOpenPaths(persisted.openPaths);
    // Map activeIndex to a tab id; index 0 = preview by convention, so
    // anything in openPaths gets indices 1..N. Be permissive: if the
    // saved active doesn't exist, fall back to preview.
    if (persisted.activeIndex === 0 || persisted.openPaths.length === 0) {
      setActiveTabIdInternal("preview");
      setRightModeInternal("preview");
    } else {
      const i = Math.min(persisted.activeIndex - 1, persisted.openPaths.length - 1);
      const targetPath = persisted.openPaths[i];
      if (targetPath) {
        setActiveTabIdInternal(fileTabId(targetPath));
        setRightModeInternal("file");
      } else {
        setActiveTabIdInternal("preview");
        setRightModeInternal("preview");
      }
    }
    if (typeof window !== "undefined") {
      const rm = window.localStorage.getItem(RIGHT_MODE_KEY);
      if (rm === "preview" || rm === "file") setRightModeInternal(rm);
    }
  }, [root]);

  // Mirror openPaths + activeTabId to localStorage. Compute activeIndex
  // relative to the [preview, ...files] ordering used in the renderer.
  useEffect(() => {
    if (!hydratedRef.current) return;
    if (typeof window === "undefined") return;
    const activeIndex =
      activeTabId === "preview"
        ? 0
        : Math.max(1, openPaths.findIndex((p) => fileTabId(p) === activeTabId) + 1);
    try {
      window.localStorage.setItem(
        TABS_KEY,
        JSON.stringify({ openPaths, activeIndex }),
      );
    } catch {
      /* ignore */
    }
  }, [openPaths, activeTabId]);

  const setRoot = useCallback((next: string) => {
    setRootInternal(next);
    if (typeof window !== "undefined") {
      try {
        if (next) window.localStorage.setItem(ROOT_KEY, next);
        else window.localStorage.removeItem(ROOT_KEY);
      } catch {
        /* ignore */
      }
    }
  }, []);

  const setPreviewUrl = useCallback((next: string) => {
    const trimmed = next.trim();
    setPreviewUrlInternal(trimmed);
    if (typeof window !== "undefined") {
      try {
        if (trimmed) window.localStorage.setItem(PREVIEW_KEY, trimmed);
        else window.localStorage.removeItem(PREVIEW_KEY);
      } catch {
        /* ignore */
      }
    }
  }, []);

  const setDevice = useCallback((next: DevicePreset) => {
    setDeviceInternal(next);
    if (typeof window !== "undefined") {
      try {
        window.localStorage.setItem(DEVICE_KEY, next);
      } catch {
        /* ignore */
      }
    }
  }, []);

  const setRightMode = useCallback((m: "preview" | "file") => {
    setRightModeInternal(m);
    if (typeof window !== "undefined") {
      try {
        window.localStorage.setItem(RIGHT_MODE_KEY, m);
      } catch {
        /* ignore */
      }
    }
  }, []);

  const refreshPreview = useCallback(() => setRefreshKey((k) => k + 1), []);

  const setActiveTabId = useCallback(
    (id: string) => {
      setActiveTabIdInternal(id);
      setRightModeInternal(id === "preview" ? "preview" : "file");
      if (typeof window !== "undefined") {
        try {
          window.localStorage.setItem(RIGHT_MODE_KEY, id === "preview" ? "preview" : "file");
        } catch {
          /* ignore */
        }
      }
    },
    [],
  );

  const openDocument = useCallback((doc: DocMeta) => {
    setDocuments((prev) => [...prev.filter((d) => d.id !== doc.id), doc]);
    setActiveTabId(doc.id);
  }, [setActiveTabId]);

  const closeDocument = useCallback((id: string) => {
    setDocuments((prev) => prev.filter((d) => d.id !== id));
    setActiveTabIdInternal((cur) => (cur === id ? "preview" : cur));
  }, []);

  // restoreDocuments rehydrates the set of open document tabs after a refresh /
  // device switch from server-tracked artifacts (the vanishing-tabs fix). It
  // replaces the in-memory documents wholesale (closed-by-X tabs are simply not
  // in the list) and optionally restores which one was active, without
  // stealing focus when nothing was active.
  const restoreDocuments = useCallback((docs: DocMeta[], activeId?: string) => {
    setDocuments(docs);
    if (activeId && docs.some((d) => d.id === activeId)) {
      setActiveTabIdInternal(activeId);
      setRightModeInternal("file");
    }
  }, []);

  const openFile = useCallback(
    (path: string) => {
      setOpenPaths((prev) => (prev.includes(path) ? prev : [...prev, path]));
      setActiveTabIdInternal(fileTabId(path));
      setRightModeInternal("file");
      if (typeof window !== "undefined") {
        try {
          window.localStorage.setItem(RIGHT_MODE_KEY, "file");
        } catch {
          /* ignore */
        }
      }
    },
    [],
  );

  const closeFile = useCallback(
    (id: string) => {
      if (!id.startsWith("file:")) return;
      const closingPath = id.slice("file:".length);
      setOpenPaths((prev) => {
        const next = prev.filter((p) => p !== closingPath);
        // If we closed the active tab, fall back to the previous tab,
        // or Preview if no files remain.
        if (activeTabId === id) {
          const idx = prev.findIndex((p) => p === closingPath);
          const fallback =
            idx > 0
              ? fileTabId(prev[idx - 1])
              : next.length > 0
                ? fileTabId(next[0])
                : "preview";
          setActiveTabIdInternal(fallback);
          setRightModeInternal(fallback === "preview" ? "preview" : "file");
        }
        return next;
      });
    },
    [activeTabId],
  );

  const closeOthers = useCallback(
    (id: string) => {
      if (id === "preview") {
        setOpenPaths([]);
        setActiveTabIdInternal("preview");
        setRightModeInternal("preview");
        return;
      }
      const path = id.slice("file:".length);
      setOpenPaths([path]);
      setActiveTabIdInternal(id);
      setRightModeInternal("file");
    },
    [],
  );

  const closeAllFiles = useCallback(() => {
    setOpenPaths([]);
    setActiveTabIdInternal("preview");
    setRightModeInternal("preview");
  }, []);

  const markDirty = useCallback((path: string) => {
    setDirtyPaths((prev) => {
      if (prev.has(path)) return prev;
      const next = new Set(prev);
      next.add(path);
      return next;
    });
  }, []);

  const clearDirty = useCallback(() => setDirtyPaths(new Set()), []);
  const bumpBridgeEpoch = useCallback(() => setBridgeEpoch((n) => n + 1), []);

  // pushToolInputDelta accumulates one streamed tool-argument chunk, extracts
  // the file path (opens the tab the instant it's known) and the content so
  // far, and pushes it into the live buffer. Gate to code-change tools at the
  // call site (Workspace) — the store stays generic.
  const pushToolInputDelta = useCallback(
    (toolId: string, name: string, delta: string) => {
      const entry = toolRawRef.current.get(toolId) ?? { raw: "", path: null as string | null, name };
      entry.raw += delta;
      if (name) entry.name = name;
      if (!entry.path) {
        const p = extractStreamingPath(entry.raw);
        if (p) {
          entry.path = p;
          openFile(p);
          markDirty(p);
          setLiveStreaming((prev) => {
            const next = new Set(prev);
            next.add(p);
            return next;
          });
        }
      }
      toolRawRef.current.set(toolId, entry);
      if (entry.path) {
        const content = extractStreamingContent(entry.raw);
        if (content != null) {
          const path = entry.path;
          setLiveContent((prev) => {
            const next = new Map(prev);
            next.set(path, content);
            return next;
          });
          setLastLive((prev) => {
            const next = new Map(prev);
            next.set(path, content);
            return next;
          });
        }
      }
    },
    [openFile, markDirty],
  );

  // setPendingFile is the model-agnostic floor: the complete content from a
  // finished tool_call. Opens the file and shows the full content immediately,
  // even when the provider couldn't stream per-token deltas. Reconciles
  // whatever streamed (the complete input is authoritative until disk write).
  const setPendingFile = useCallback(
    (path: string, content: string) => {
      openFile(path);
      markDirty(path);
      setLiveContent((prev) => {
        const next = new Map(prev);
        next.set(path, content);
        return next;
      });
      setLastLive((prev) => {
        const next = new Map(prev);
        next.set(path, content);
        return next;
      });
      setLiveStreaming((prev) => {
        if (!prev.has(path)) return prev;
        const next = new Set(prev);
        next.delete(path); // tool_call done — no longer actively streaming
        return next;
      });
    },
    [openFile, markDirty],
  );

  // endLiveFile drops the live buffer (on tool_result) so the file tab reloads
  // the authoritative on-disk content (enabling real diff + edit).
  const endLiveFile = useCallback((path: string) => {
    setLiveStreaming((prev) => {
      if (!prev.has(path)) return prev;
      const next = new Set(prev);
      next.delete(path);
      return next;
    });
    setLiveContent((prev) => {
      if (!prev.has(path)) return prev;
      const next = new Map(prev);
      next.delete(path);
      return next;
    });
    for (const [id, entry] of toolRawRef.current) {
      if (entry.path === path) toolRawRef.current.delete(id);
    }
  }, []);

  const tabs = useMemo<CanvasTab[]>(() => {
    return [
      { kind: "preview", id: "preview" } as const,
      { kind: "terminal", id: "terminal" } as const,
      { kind: "media", id: "media" } as const,
      ...openPaths.map((p) => ({ kind: "file", id: fileTabId(p), path: p }) as const),
      ...documents.map((d) =>
        ({ kind: "document", id: d.id, filename: d.filename, format: d.format, path: d.path }) as const),
    ];
  }, [openPaths, documents]);

  const value = useMemo<CanvasStoreValue>(
    () => ({
      root,
      setRoot,
      bridgeOk,
      setBridgeOk,
      previewUrl,
      setPreviewUrl,
      envPreviewUrl,
      device,
      setDevice,
      previewRefreshKey,
      refreshPreview,
      tabs,
      activeTabId,
      setActiveTabId,
      openFile,
      closeFile,
      closeOthers,
      closeAllFiles,
      rightMode,
      setRightMode,
      browserActive,
      setBrowserActive,
      browserSessionId,
      setBrowserSessionId,
      documents,
      openDocument,
      closeDocument,
      restoreDocuments,
      dirtyPaths,
      markDirty,
      clearDirty,
      bridgeEpoch,
      bumpBridgeEpoch,
      liveContent,
      liveStreaming,
      lastLive,
      editFocus,
      markEditFocus,
      pushToolInputDelta,
      setPendingFile,
      endLiveFile,
    }),
    [
      root,
      setRoot,
      bridgeOk,
      previewUrl,
      setPreviewUrl,
      envPreviewUrl,
      device,
      setDevice,
      previewRefreshKey,
      refreshPreview,
      tabs,
      activeTabId,
      setActiveTabId,
      openFile,
      closeFile,
      closeOthers,
      closeAllFiles,
      rightMode,
      setRightMode,
      browserActive,
      browserSessionId,
      documents,
      openDocument,
      closeDocument,
      restoreDocuments,
      dirtyPaths,
      markDirty,
      clearDirty,
      bridgeEpoch,
      bumpBridgeEpoch,
      liveContent,
      liveStreaming,
      lastLive,
      editFocus,
      markEditFocus,
      pushToolInputDelta,
      setPendingFile,
      endLiveFile,
    ],
  );

  return <CanvasStoreContext.Provider value={value}>{children}</CanvasStoreContext.Provider>;
}

export function useCanvasStore() {
  const ctx = useContext(CanvasStoreContext);
  if (!ctx) throw new Error("useCanvasStore must be used within CanvasStoreProvider");
  return ctx;
}

export function devicePresetDimensions(p: DevicePreset): { width: number; height: number } | null {
  switch (p) {
    case "mobile":
      return { width: 390, height: 844 }; // iPhone 14 Pro
    case "tablet":
      return { width: 820, height: 1180 }; // iPad Air
    case "desktop":
      return null;
  }
}
