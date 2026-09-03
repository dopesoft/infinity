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

/**
 * How much room the workbench takes beside the conversation.
 *
 *   chat   the conversation is the page; nothing else is open
 *   split  42 / 58 — reading a diff while you talk
 *   build  a 286px chat rail and the rest to the workbench — watching a
 *          preview redraw itself while he types. The IDE feeling.
 *
 * One split was wrong because you do two different things here.
 */
export type LayoutMode = "chat" | "split" | "build";

export type CanvasTabKind = "preview" | "terminal" | "media" | "file" | "document";

/**
 * Instrument - WHAT THE WORKBENCH IS SHOWING. One value, owned here.
 *
 * It used to be a `useState` inside WorkbenchPane while the store owned
 * `activeTabId`, so "which document is focused" and "is that document on
 * screen" were two separate decisions kept in step by hand. Every entry point
 * that focused a document without also reaching into the pane's private state
 * - the Made gallery, the Library, the dashboard hand-off - opened a tab
 * nobody could see. Focusing a tab and revealing it are the same act, so they
 * happen at one seam: `setActiveTabId`.
 */
export type Instrument = "files" | "file" | "browser" | "changes" | "terminal" | "made";

/** The instrument that shows a given tab. The whole point of the type above. */
function instrumentForTab(id: string): Instrument {
  switch (id) {
    case "preview":
      return "browser";
    case "terminal":
      return "terminal";
    case "media":
      return "made";
    default:
      return "file"; // file:<path> and document ids both live in the file slot
  }
}

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
  htmlPath?: string; // side-scrollable HTML preview (spreadsheets)
  // The document's VERSION (mem_artifacts.updated_at). A document Jarvis redoes
  // keeps its path, so this is the only thing that tells an already-open tab
  // its bytes moved. Every preview fetch keys off it.
  version?: string;
};

// extensionOf reads the kind of file out of its name. Only document_create
// stamps a format on the row; a file the boss UPLOADED carries none, and an
// empty format is what left the viewer with no preview path at all - it fell
// through to a download card on a PDF it could perfectly well have shown. The
// extension is right there in the name, so every mapper below asks for it.
function extensionOf(name?: string): string {
  const base = (name ?? "").split(/[\\/]/).pop() ?? "";
  const i = base.lastIndexOf(".");
  if (i <= 0 || i === base.length - 1) return "";
  const ext = base.slice(i + 1).toLowerCase();
  return /^[a-z0-9]{1,8}$/.test(ext) ? ext : "";
}

// docMetaFromArtifact maps a server-tracked DocArtifact (mem_artifacts row) to
// the DocMeta a tab renders. Shared by the right-pane (rehydration) and the
// gallery (click-to-open) so the mapping lives in exactly one place.
export function docMetaFromArtifact(a: DocArtifact): DocMeta {
  return {
    id: a.path, // DocMeta.id === path, matching the document_created tab convention
    filename: a.filename,
    format: a.format || extensionOf(a.filename) || extensionOf(a.path),
    path: a.path,
    bytes: a.bytes,
    markdown: a.markdown,
    pdfPath: a.pdf_path,
    htmlPath: a.html_path,
    version: a.updated_at || a.created_at,
  };
}

// docMetaFromLibrary maps a Library row (mem_artifacts, same table as
// DocArtifact) to the DocMeta a document tab renders. It lives beside
// docMetaFromArtifact so both entry points into the document viewer share one
// mapping: the Library and the session gallery can never drift apart.
export function docMetaFromLibrary(e: {
  name: string;
  storage_path?: string;
  format?: string;
  pdf_path?: string;
  html_path?: string;
  markdown?: string;
  updated_at?: string;
  created_at?: string;
}): DocMeta {
  const path = e.storage_path ?? "";
  return {
    id: path,
    filename: e.name,
    // Fall back to the filename extension when the row carries no metadata
    // (older artifacts, and every uploaded file).
    format: e.format || extensionOf(e.name) || extensionOf(path),
    path,
    markdown: e.markdown || undefined,
    pdfPath: e.pdf_path || undefined,
    htmlPath: e.html_path || undefined,
    version: e.updated_at || e.created_at || undefined,
  };
}

// Pending-doc handoff: when a generated document is opened from OUTSIDE the
// canvas (e.g. the dashboard's Saved card), the opener stashes the DocMeta here
// and routes to /live. CanvasRightPane drains it on mount and opens the tab
// focused. sessionStorage (not state) because it must survive the cross-page
// navigation; one-shot because the consumer clears it immediately.
const PENDING_DOC_KEY = "infinity:canvas:pendingDoc";

export function stashPendingDoc(doc: DocMeta) {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(PENDING_DOC_KEY, JSON.stringify(doc));
  } catch {
    /* ignore */
  }
}

export function takePendingDoc(): DocMeta | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(PENDING_DOC_KEY);
    if (!raw) return null;
    window.sessionStorage.removeItem(PENDING_DOC_KEY);
    const d = JSON.parse(raw) as DocMeta;
    return d && d.path ? d : null;
  } catch {
    return null;
  }
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

  /** Current width of the workbench beside the chat. */
  layout: LayoutMode;
  /**
   * Move it by hand. Doing so turns AUTO OFF for the rest of the session:
   * auto that fights you is worse than no auto, so one deliberate move ends
   * the guessing until the next conversation.
   */
  setLayout: (next: LayoutMode) => void;
  /** True while the layout is still choosing for itself. */
  layoutAuto: boolean;
  /**
   * Let the workbench move itself — an edit lands, a preview rebuilds, a
   * render finishes. A no-op once the boss has taken the wheel.
   */
  suggestLayout: (next: LayoutMode) => void;
  previewRefreshKey: number;
  refreshPreview: () => void;

  // Tabs
  tabs: CanvasTab[];
  activeTabId: string;
  /** Focus a tab AND reveal it. The one seam where those two agree. */
  setActiveTabId: (id: string) => void;
  /** What the workbench is showing right now. */
  instrument: Instrument;
  /** Choose an instrument directly - the bar, and the pane's own snaps. */
  setInstrument: (i: Instrument) => void;
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
  // The live session's own address and who is driving it. These live here,
  // NOT in localStorage next to previewUrl, because they belong to one browser
  // session: persisting them is what let a stale address from a previous chat
  // (espn.com, 2026-08-30) show up in the bar of a brand new one. They are
  // transient by design and re-announce themselves on the next frame.
  browserUrl: string;
  setBrowserUrl: (url: string) => void;
  browserController: "agent" | "human";
  setBrowserController: (c: "agent" | "human") => void;

  // Generated documents (each opens as its own closeable tab).
  documents: DocMeta[];
  /** Open a document AND put it on screen. The deliberate "show me this one". */
  openDocument: (doc: DocMeta) => void;
  /** Add a document tab without taking the screen - a document FINISHING is
   *  news, not an interruption. It lands in Made and waits to be opened. */
  registerDocument: (doc: DocMeta) => void;
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
  // Layout mode is session state, NOT persisted: a fresh conversation should
  // open as a conversation, not in whatever width the last coding session
  // left behind. `layoutAuto` flips off the moment the boss moves it himself.
  const [layout, setLayoutInternal] = useState<LayoutMode>("chat");
  const [layoutAuto, setLayoutAuto] = useState(true);
  // Read inside suggestLayout without making it re-create on every toggle:
  // the workbench subscribes to it from a WS handler that must mount once.
  const autoRef = useRef(true);
  autoRef.current = layoutAuto;
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
  // Files, not Browser: you open the workbench to see what is here, and a
  // preview of a project that may not be serving anything is a blank screen
  // with a reload button on it.
  const [instrument, setInstrument] = useState<Instrument>("files");
  const [browserActive, setBrowserActive] = useState(false);
  const [browserSessionId, setBrowserSessionId] = useState("");
  const [browserUrl, setBrowserUrl] = useState("");
  const [browserController, setBrowserController] = useState<"agent" | "human">("agent");
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
        setInstrument("file"); // come back to what he was reading, not the tree
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

  /**
   * The boss moving the layout by hand ends auto for this session. This is
   * the rule that makes the other five safe: an auto that yanks you back to
   * a pane you just closed is worse than no auto at all.
   */
  const setLayout = useCallback((next: LayoutMode) => {
    setLayoutAuto(false);
    setLayoutInternal(next);
  }, []);

  /** The workbench asking to move itself. Silent once the boss has decided. */
  const suggestLayout = useCallback((next: LayoutMode) => {
    setLayoutInternal((cur) => {
      if (!autoRef.current) return cur;
      return next;
    });
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
      // Reveal what you just focused. Every open path in the app funnels
      // through here, so a document opened from the gallery, the Library or
      // the dashboard hand-off can no longer land behind whatever is on top.
      setInstrument(instrumentForTab(id));
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

  // registerDocument is openDocument minus the screen. A document FINISHING is
  // news, not an interruption: it joins the tabs (so the mobile canvas reveal
  // and the layout auto still fire) and waits in Made, which snaps to itself
  // as the count rises. Only a deliberate click gets to take the pane.
  const registerDocument = useCallback((doc: DocMeta) => {
    setDocuments((prev) => [...prev.filter((d) => d.id !== doc.id), doc]);
  }, []);

  const closeDocument = useCallback((id: string) => {
    setDocuments((prev) => prev.filter((d) => d.id !== id));
    // If the closed tab was the active one, retarget — never dump on Preview.
    // Switch to an adjacent open document if any remain, otherwise back to the
    // Media tab (the documents' home, where they were opened from).
    if (activeTabId === id) {
      const idx = documents.findIndex((d) => d.id === id);
      const remaining = documents.filter((d) => d.id !== id);
      const target =
        remaining.length === 0
          ? "media"
          : remaining[Math.min(idx, remaining.length - 1)]?.id ?? "media";
      setActiveTabId(target);
    }
  }, [documents, activeTabId, setActiveTabId]);

  // restoreDocuments makes the open document tabs match a given set, exactly.
  // It is the ONLY authority on which docs are open: it rehydrates after a
  // refresh / device switch (the vanishing-tabs fix) AND clears on a session
  // switch (docs belong to the conversation that made them — an empty list is
  // a legitimate, meaningful argument, not a no-op).
  //
  // Because it can remove the active tab, it retargets activeTabId whenever the
  // tab it points at is a document that no longer exists.
  const restoreDocuments = useCallback(
    (docs: DocMeta[], activeId?: string) => {
      setDocuments(docs);
      const stillOpen = (id: string) => docs.some((d) => d.id === id);

      // A caller-supplied active tab wins, when it survived the restore.
      if (activeId && stillOpen(activeId)) {
        setActiveTabId(activeId);
        return;
      }
      // Otherwise only intervene when the tab in focus was a document that just
      // disappeared — never steal focus from Preview / Terminal / a file tab.
      const activeWasDoc = documents.some((d) => d.id === activeTabId);
      if (!activeWasDoc || stillOpen(activeTabId)) return;
      // Adjacent document if any survive, else Preview — the first tab, and the
      // canvas's resting state. (Distinct from closeDocument, which returns to
      // Media because that's where the boss clicked the document open from.)
      setActiveTabId(docs.length ? docs[docs.length - 1].id : "preview");
    },
    [documents, activeTabId, setActiveTabId],
  );

  const openFile = useCallback(
    (path: string) => {
      setOpenPaths((prev) => (prev.includes(path) ? prev : [...prev, path]));
      setActiveTabId(fileTabId(path));
    },
    [setActiveTabId],
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
          // Nothing left in the file slot means the tree, never a blank layer.
          setInstrument(fallback === "preview" ? "files" : "file");
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
        setInstrument("files");
        return;
      }
      const path = id.slice("file:".length);
      setOpenPaths([path]);
      setActiveTabIdInternal(id);
      setRightModeInternal("file");
      setInstrument("file");
    },
    [],
  );

  const closeAllFiles = useCallback(() => {
    setOpenPaths([]);
    setActiveTabIdInternal("preview");
    setRightModeInternal("preview");
    // This is the project-switch path: show him what is in the new project.
    setInstrument("files");
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
      layout,
      layoutAuto,
      setDevice,
      setLayout,
      suggestLayout,
      previewRefreshKey,
      refreshPreview,
      tabs,
      activeTabId,
      setActiveTabId,
      instrument,
      setInstrument,
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
      browserUrl,
      setBrowserUrl,
      browserController,
      setBrowserController,
      documents,
      openDocument,
      registerDocument,
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
      layout,
      layoutAuto,
      setLayout,
      suggestLayout,
      previewRefreshKey,
      refreshPreview,
      tabs,
      activeTabId,
      setActiveTabId,
      instrument,
      setInstrument,
      openFile,
      closeFile,
      closeOthers,
      closeAllFiles,
      rightMode,
      setRightMode,
      browserActive,
      browserSessionId,
      browserUrl,
      browserController,
      documents,
      openDocument,
      registerDocument,
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
