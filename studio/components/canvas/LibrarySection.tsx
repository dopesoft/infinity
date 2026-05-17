"use client";

import { useCallback, useEffect, useState } from "react";
import {
  ChevronDown,
  ChevronRight,
  Database,
  FileAudio,
  FileText,
  FileVideo,
  Folder,
  Image as ImageIcon,
  Library,
  RefreshCw,
  Sparkles,
} from "lucide-react";
import {
  fetchLibraryTree,
  setSessionProjectPath,
  type LibraryEntry,
  type LibraryGroup,
} from "@/lib/canvas/api";
import { useCanvasStore } from "@/lib/canvas/store";
import { cn } from "@/lib/utils";

/**
 * LibrarySection - collapsible block at the top of the Files tab that
 * surfaces mem_artifacts grouped by kind. The Files tab is the unified
 * file browser; Library lives inside it, not as its own tab.
 *
 * Click a project entry → swap store.root + persist
 * mem_sessions.project_path so the real-filesystem tree below reloads
 * scoped to that project, and future turns auto-scope.
 *
 * Click a media entry (image/audio/video/document) → opens it in the
 * right pane viewer via the canvas store's openFile helper, which
 * accepts external URLs in addition to filesystem paths.
 */
export function LibrarySection({
  sessionId,
  onProjectSelect,
}: {
  sessionId: string | null;
  /** Optional override for handling project selection (tests / mobile). */
  onProjectSelect?: (path: string) => void;
}) {
  const store = useCanvasStore();
  const [groups, setGroups] = useState<LibraryGroup[]>([]);
  const [loading, setLoading] = useState(false);
  const [expanded, setExpanded] = useState(true);
  const [openKinds, setOpenKinds] = useState<Record<string, boolean>>({
    project: true, // projects expanded by default - most-used
  });

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    try {
      const tree = await fetchLibraryTree(signal);
      setGroups(tree?.groups ?? []);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const toggleKind = (kind: string) =>
    setOpenKinds((prev) => ({ ...prev, [kind]: !prev[kind] }));

  const handleProject = async (entry: LibraryEntry) => {
    const path = entry.storage_path || "";
    if (!path) return;
    if (onProjectSelect) onProjectSelect(path);
    else store.setRoot(path);
    store.closeAllFiles();
    store.clearDirty();
    if (sessionId) {
      void setSessionProjectPath(sessionId, path).catch(() => {});
    }
  };

  const handleMedia = (entry: LibraryEntry) => {
    if (!entry.storage_path) return;
    // Filesystem-backed media → open via the canvas store (uses Monaco
    // or its previewer). Object-store URLs → open in a new tab for v1;
    // a richer in-pane lightbox can come later.
    if (entry.storage_kind === "filesystem") {
      store.openFile(entry.storage_path);
    } else if (typeof window !== "undefined") {
      window.open(entry.storage_path, "_blank", "noopener,noreferrer");
    }
  };

  return (
    <div className="shrink-0 border-b bg-muted/20 dark:bg-zinc-900/30">
      {/* Header row - toggles the whole Library block. Matches the
          visual mass of the deploy/source banners above it (font-mono,
          11px, muted tone) so the Files panel reads as one continuous
          stack rather than competing surfaces. */}
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="flex w-full items-center gap-1.5 px-3 py-1.5 text-left text-[11px] font-semibold uppercase tracking-wide text-muted-foreground transition-colors hover:bg-accent/40"
        aria-label={expanded ? "Collapse Library" : "Expand Library"}
      >
        {expanded ? (
          <ChevronDown className="size-3" aria-hidden />
        ) : (
          <ChevronRight className="size-3" aria-hidden />
        )}
        <Library className="size-3" aria-hidden />
        <span className="flex-1">Library</span>
        {loading && <Sparkles className="size-3 animate-pulse" aria-hidden />}
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            void load();
          }}
          aria-label="Refresh Library"
          title="Refresh"
          className="inline-flex size-5 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-background hover:text-foreground"
        >
          <RefreshCw className={cn("size-3", loading && "animate-spin")} aria-hidden />
        </button>
      </button>

      {expanded && (
        <div className="pb-1">
          {groups.length === 0 && !loading && (
            <p className="px-3 pb-2 text-[11px] text-muted-foreground">
              Empty - projects, images, and other artifacts Jarvis makes will appear here.
            </p>
          )}
          {groups.map((group) => (
            <LibraryGroupRow
              key={group.kind}
              group={group}
              open={!!openKinds[group.kind]}
              onToggle={() => toggleKind(group.kind)}
              onProject={handleProject}
              onMedia={handleMedia}
              currentRoot={store.root}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function LibraryGroupRow({
  group,
  open,
  onToggle,
  onProject,
  onMedia,
  currentRoot,
}: {
  group: LibraryGroup;
  open: boolean;
  onToggle: () => void;
  onProject: (e: LibraryEntry) => void;
  onMedia: (e: LibraryEntry) => void;
  currentRoot: string;
}) {
  const label = KIND_LABEL[group.kind] ?? capitalize(group.kind);
  const Icon = KIND_ICON[group.kind] ?? Folder;
  return (
    <div>
      <button
        type="button"
        onClick={onToggle}
        className="flex w-full items-center gap-1.5 px-3 py-1 text-left text-[12px] text-foreground/85 transition-colors hover:bg-accent/40"
      >
        {open ? (
          <ChevronDown className="size-3 shrink-0" aria-hidden />
        ) : (
          <ChevronRight className="size-3 shrink-0" aria-hidden />
        )}
        <Icon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        <span className="flex-1 truncate">{label}</span>
        <span className="font-mono text-[10px] text-muted-foreground">{group.count}</span>
      </button>
      {open && (
        <ul className="pb-0.5 pl-5">
          {group.entries.map((entry) => (
            <li key={entry.id}>
              <button
                type="button"
                onClick={() =>
                  group.kind === "project" ? onProject(entry) : onMedia(entry)
                }
                className={cn(
                  "flex w-full min-h-7 items-center gap-2 px-2 py-1 text-left text-[12px] transition-colors hover:bg-accent/60",
                  group.kind === "project" &&
                    entry.storage_path === currentRoot &&
                    "bg-accent/50 text-accent-foreground",
                )}
                title={entry.virtual_path}
              >
                <span className="min-w-0 flex-1 truncate">{entry.name}</span>
                {entry.bridge && (
                  <span
                    className={cn(
                      "shrink-0 rounded px-1 font-mono text-[9px] uppercase tracking-wider",
                      entry.bridge === "mac"
                        ? "bg-success/10 text-success"
                        : "bg-info/10 text-info",
                    )}
                  >
                    {entry.bridge}
                  </span>
                )}
                {entry.github_url && group.kind === "project" && (
                  <span
                    className="shrink-0 font-mono text-[9px] text-muted-foreground"
                    title={entry.github_url}
                  >
                    ↗
                  </span>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

const KIND_LABEL: Record<string, string> = {
  project: "Projects",
  image: "Images",
  audio: "Audio",
  video: "Video",
  document: "Documents",
  dataset: "Datasets",
  memory: "Memories",
  other: "Other",
};

const KIND_ICON: Record<string, typeof Folder> = {
  project: Folder,
  image: ImageIcon,
  audio: FileAudio,
  video: FileVideo,
  document: FileText,
  dataset: Database,
  memory: Sparkles,
  other: Folder,
};

function capitalize(s: string): string {
  if (!s) return s;
  return s[0].toUpperCase() + s.slice(1);
}
