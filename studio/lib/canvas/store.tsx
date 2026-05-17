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

export type DevicePreset = "mobile" | "tablet" | "desktop";

export type CanvasTabKind = "preview" | "file";

export type CanvasTab =
  | { kind: "preview"; id: "preview" }
  | { kind: "file"; id: string; path: string };

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

  // Dirty tracking
  dirtyPaths: Set<string>;
  markDirty: (path: string) => void;
  clearDirty: () => void;
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
  const [rightMode, setRightModeInternal] = useState<"preview" | "file">("preview");

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

  const tabs = useMemo<CanvasTab[]>(() => {
    return [
      { kind: "preview", id: "preview" } as const,
      ...openPaths.map((p) => ({ kind: "file", id: fileTabId(p), path: p }) as const),
    ];
  }, [openPaths]);

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
      dirtyPaths,
      markDirty,
      clearDirty,
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
      dirtyPaths,
      markDirty,
      clearDirty,
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
