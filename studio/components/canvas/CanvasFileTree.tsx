"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  AlertCircle,
  ChevronDown,
  ChevronRight,
  File as FileIcon,
  Folder,
  FolderOpen,
  Loader2,
  RefreshCw,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useCanvasStore } from "@/lib/canvas/store";
import { fetchCanvasFSList, fetchCanvasDebug, type FSEntry } from "@/lib/canvas/api";
import { cn } from "@/lib/utils";

/**
 * CanvasFileTree — lazy directory browser scoped to INFINITY_CANVAS_ROOT.
 *
 * Internally each directory caches its children once loaded. Expanding a
 * collapsed folder kicks a fetch; collapsing does NOT discard children
 * (cheap re-expand), but the user can hit "refresh" on any node to
 * re-fetch.
 *
 * Selecting a file opens it in the right pane (canvas store handles tab
 * lifecycle). Files modified in this session show a yellow dot — that
 * signal comes from the WS hook in CanvasFrame.
 */
type Node = {
  path: string;
  name: string;
  type: FSEntry["type"];
  expanded: boolean;
  loading: boolean;
  error?: string;
  children?: Node[];
};

function makeRootNode(rootPath: string): Node {
  return {
    path: rootPath,
    name: pathBasename(rootPath) || rootPath,
    type: "dir",
    expanded: true,
    loading: false,
  };
}

function pathBasename(p: string): string {
  if (!p) return "";
  const trimmed = p.replace(/\/+$/, "");
  const idx = trimmed.lastIndexOf("/");
  return idx >= 0 ? trimmed.slice(idx + 1) : trimmed;
}

function pathJoin(parent: string, child: string): string {
  if (!parent || parent === "/") return "/" + child.replace(/^\/+/, "");
  return parent.replace(/\/+$/, "") + "/" + child.replace(/^\/+/, "");
}

export function CanvasFileTree() {
  const store = useCanvasStore();
  const [root, setRoot] = useState<Node | null>(null);
  const [filter, setFilter] = useState("");

  // (Re)build the root whenever the workspace root changes.
  useEffect(() => {
    if (!store.root) {
      setRoot(null);
      return;
    }
    const next = makeRootNode(store.root);
    setRoot(next);
    void loadChildren(next.path).then((entries) => {
      setRoot((prev) => {
        if (!prev || prev.path !== next.path) return prev;
        return {
          ...prev,
          loading: false,
          children: entries.map(entryToNode(prev.path)),
        };
      });
    });
  }, [store.root]);

  const onToggle = useCallback((targetPath: string) => {
    setRoot((prev) => {
      if (!prev) return prev;
      return updateNode(prev, targetPath, (n) => {
        const nextExpanded = !n.expanded;
        if (nextExpanded && !n.children && n.type === "dir") {
          // Kick a load.
          void loadChildren(n.path).then((entries) => {
            setRoot((p) =>
              p
                ? updateNode(p, n.path, (m) => ({
                    ...m,
                    loading: false,
                    children: entries.map(entryToNode(n.path)),
                  }))
                : p,
            );
          });
          return { ...n, expanded: nextExpanded, loading: true };
        }
        return { ...n, expanded: nextExpanded };
      });
    });
  }, []);

  const onRefresh = useCallback((targetPath: string) => {
    setRoot((prev) => {
      if (!prev) return prev;
      void loadChildren(targetPath).then((entries) => {
        setRoot((p) =>
          p
            ? updateNode(p, targetPath, (m) => ({
                ...m,
                loading: false,
                children: entries.map(entryToNode(targetPath)),
              }))
            : p,
        );
      });
      return updateNode(prev, targetPath, (n) => ({ ...n, loading: true }));
    });
  }, []);

  const filtered = useMemo(() => {
    if (!root) return null;
    if (!filter.trim()) return root;
    return pruneByFilter(root, filter.trim().toLowerCase());
  }, [root, filter]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-1 border-b px-2 py-1.5">
        <Input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter..."
          inputMode="search"
          aria-label="Filter files"
          className="h-8 flex-1 text-sm"
        />
        {root && (
          <>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              className="h-8 w-8 shrink-0"
              onClick={() => onRefresh(root.path)}
              aria-label="Refresh tree"
              title="Refresh"
            >
              <RefreshCw className="size-3.5" />
            </Button>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              className="h-8 w-8 shrink-0"
              onClick={async () => {
                const out = await fetchCanvasDebug(root.path);
                console.log("[canvas debug]", out);
                // Also dump to clipboard so the boss can paste it.
                try {
                  await navigator.clipboard.writeText(JSON.stringify(out, null, 2));
                } catch {
                  /* clipboard may be unavailable in some sandboxes */
                }
              }}
              aria-label="Diagnose file tree"
              title="Run file-tree diagnostic (output in console + clipboard)"
            >
              <AlertCircle className="size-3.5" />
            </Button>
          </>
        )}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto scroll-touch py-1 text-sm">
        {!store.root && <EmptyRoot />}
        {store.root && !filtered && (
          <div className="flex items-center justify-center py-8 text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
          </div>
        )}
        {filtered && (
          <NodeRow
            node={filtered}
            depth={0}
            onToggle={onToggle}
            onRefresh={onRefresh}
            onSelect={(p) => store.openFile(p)}
            activeFilePath={
              store.activeTabId.startsWith("file:") ? store.activeTabId.slice("file:".length) : null
            }
            dirtyPaths={store.dirtyPaths}
          />
        )}
      </div>
    </div>
  );
}

function NodeRow({
  node,
  depth,
  onToggle,
  onRefresh,
  onSelect,
  activeFilePath,
  dirtyPaths,
}: {
  node: Node;
  depth: number;
  onToggle: (p: string) => void;
  onRefresh: (p: string) => void;
  onSelect: (p: string) => void;
  activeFilePath: string | null;
  dirtyPaths: Set<string>;
}) {
  const isDir = node.type === "dir";
  const isActive = !isDir && activeFilePath === node.path;
  const isDirty = dirtyPaths.has(node.path);
  const Icon = isDir ? (node.expanded ? FolderOpen : Folder) : FileIcon;
  return (
    <div>
      <button
        type="button"
        onClick={() => (isDir ? onToggle(node.path) : onSelect(node.path))}
        className={cn(
          "group flex w-full min-h-7 items-center gap-1 rounded-sm px-1.5 py-1 text-left text-[13px] transition-colors",
          isActive ? "bg-accent text-accent-foreground" : "hover:bg-accent/60",
        )}
        style={{ paddingLeft: `${depth * 12 + 6}px` }}
        title={node.path}
      >
        <span className="inline-flex size-3.5 shrink-0 items-center justify-center text-muted-foreground">
          {isDir ? (
            node.expanded ? (
              <ChevronDown className="size-3" />
            ) : (
              <ChevronRight className="size-3" />
            )
          ) : null}
        </span>
        <Icon
          className={cn(
            "size-3.5 shrink-0",
            isDir ? "text-info" : isDirty ? "text-warning" : "text-muted-foreground",
          )}
          aria-hidden
        />
        <span className="min-w-0 flex-1 truncate">{node.name}</span>
        {isDirty && !isDir && (
          <span
            className="size-1.5 shrink-0 rounded-full bg-warning"
            aria-label="modified this session"
          />
        )}
        {node.loading && <Loader2 className="size-3 shrink-0 animate-spin text-muted-foreground" />}
        {node.error && (
          <span title={node.error}>
            <AlertCircle className="size-3 shrink-0 text-danger" />
          </span>
        )}
      </button>
      {isDir && node.expanded && node.children && (
        <div>
          {node.children.length === 0 ? (
            <div
              className="px-1.5 py-1 text-[11px] italic text-muted-foreground"
              style={{ paddingLeft: `${(depth + 1) * 12 + 18}px` }}
            >
              empty
            </div>
          ) : (
            node.children.map((child) => (
              <NodeRow
                key={child.path}
                node={child}
                depth={depth + 1}
                onToggle={onToggle}
                onRefresh={onRefresh}
                onSelect={onSelect}
                activeFilePath={activeFilePath}
                dirtyPaths={dirtyPaths}
              />
            ))
          )}
        </div>
      )}
    </div>
  );
}

function EmptyRoot() {
  return (
    <div className="px-3 py-6 text-xs leading-relaxed text-muted-foreground">
      <p className="mb-2 font-medium">No app in this session.</p>
      <p>
        Head over to <span className="font-medium">Live</span> and tell the agent
        what to build. Once the scaffold lands, this tree will fill in with the
        project files — and only those files.
      </p>
    </div>
  );
}

// ---- helpers ---------------------------------------------------------------

async function loadChildren(path: string): Promise<FSEntry[]> {
  const res = await fetchCanvasFSList(path);
  return res?.entries ?? [];
}

function entryToNode(parent: string) {
  return (e: FSEntry): Node => ({
    path: pathJoin(parent, e.name),
    name: e.name,
    type: e.type,
    expanded: false,
    loading: false,
  });
}

function updateNode(node: Node, targetPath: string, fn: (n: Node) => Node): Node {
  if (node.path === targetPath) return fn(node);
  if (!node.children) return node;
  return { ...node, children: node.children.map((c) => updateNode(c, targetPath, fn)) };
}

function pruneByFilter(node: Node, q: string): Node | null {
  if (node.type === "file") {
    return node.name.toLowerCase().includes(q) ? node : null;
  }
  const children = (node.children ?? [])
    .map((c) => pruneByFilter(c, q))
    .filter(Boolean) as Node[];
  if (node.name.toLowerCase().includes(q) || children.length > 0) {
    return { ...node, expanded: true, children };
  }
  return null;
}
