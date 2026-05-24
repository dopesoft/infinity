"use client";

/**
 * ProjectBreadcrumb - the "where am I" chip in the /live header.
 *
 * Always tells the boss whether the canvas is scoped to Jarvis's own code
 * ("Jarvis") or a specific app ("Project: my-app"), and lets them switch.
 * Together with <BridgePill> (which machine) it answers "where am I" fully:
 * machine + project.
 *
 * Switching reuses the proven path: setSessionProjectPath + store.setRoot +
 * closeAllFiles/clearDirty → the file tree + git panel auto-rescope (they key
 * off store.root). Projects come from the artifact registry (kind='project')
 * via fetchLibraryTree, so anything Jarvis created or cloned shows here.
 *
 * Uses store (CanvasStoreProvider wraps the page) — NOT useProjectContext,
 * whose provider lives deeper inside <Workspace>. sessionId comes in as a prop.
 */

import { useCallback, useEffect, useState } from "react";
import { ChevronDown, Check, FolderGit2, Sparkles } from "lucide-react";
import { useCanvasStore } from "@/lib/canvas/store";
import {
  fetchLibraryTree,
  setSessionProjectPath,
  type LibraryEntry,
} from "@/lib/canvas/api";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { cn } from "@/lib/utils";

export function ProjectBreadcrumb({ sessionId }: { sessionId: string | null }) {
  const store = useCanvasStore();
  const [open, setOpen] = useState(false);
  const [projects, setProjects] = useState<LibraryEntry[]>([]);

  const load = useCallback(async () => {
    const tree = await fetchLibraryTree();
    const group = tree?.groups.find((g) => g.kind === "project");
    setProjects((group?.entries ?? []).filter((e) => !!e.storage_path));
  }, []);

  // Load on mount, when the bridge changes (different machine → different
  // projects), and when the active root changes (a switch / new project) so the
  // label + list stay current. Also reloaded when the picker opens.
  useEffect(() => {
    void load();
  }, [load, store.bridgeEpoch, store.root]);

  const root = store.root;
  const current = projects.find(
    (p) =>
      p.storage_path &&
      root &&
      (root === p.storage_path || root.startsWith(p.storage_path + "/")),
  );
  const label = current ? current.name : "Jarvis";

  const selectProject = (entry: LibraryEntry) => {
    const path = entry.storage_path || "";
    if (!path) return;
    store.setRoot(path);
    store.closeAllFiles();
    store.clearDirty();
    if (sessionId) void setSessionProjectPath(sessionId, path).catch(() => {});
    setOpen(false);
  };

  // "Jarvis (self)" = clear the project binding. The root falls back to the
  // bridge-aware default (Jarvis's own code / the disk); bumpBridgeEpoch
  // re-fetches that default so the tree re-scopes.
  const selectSelf = () => {
    if (sessionId) void setSessionProjectPath(sessionId, "").catch(() => {});
    store.closeAllFiles();
    store.clearDirty();
    store.bumpBridgeEpoch();
    setOpen(false);
  };

  return (
    <>
      <button
        type="button"
        onClick={() => {
          setOpen(true);
          void load();
        }}
        title={current ? current.storage_path : "Jarvis's own code"}
        className="inline-flex h-7 max-w-[180px] items-center gap-1.5 rounded-full border border-border bg-background px-2.5 text-[11px] font-medium text-muted-foreground transition-colors hover:text-foreground"
      >
        {current ? (
          <FolderGit2 className="size-3 shrink-0 text-info" />
        ) : (
          <Sparkles className="size-3 shrink-0 text-info" />
        )}
        <span className="min-w-0 truncate">{label}</span>
        <ChevronDown className="size-3 shrink-0 opacity-60" />
      </button>

      <ResponsiveModal
        open={open}
        onOpenChange={setOpen}
        title="Switch project"
        description="Jarvis's own code, or one of your projects."
        size="sm"
      >
        <div className="flex flex-col gap-1">
          <SwitchRow
            icon={<Sparkles className="size-4 text-info" />}
            name="Jarvis (self)"
            sub="Jarvis's own code"
            active={!current}
            onClick={selectSelf}
          />
          {projects.map((p) => (
            <SwitchRow
              key={p.id}
              icon={<FolderGit2 className="size-4 text-info" />}
              name={p.name}
              sub={p.storage_path ?? ""}
              active={current?.id === p.id}
              onClick={() => selectProject(p)}
            />
          ))}
          {projects.length === 0 && (
            <p className="px-2 py-3 text-xs text-muted-foreground">
              No projects yet. Ask Jarvis to build a new app or clone a repo.
            </p>
          )}
        </div>
      </ResponsiveModal>
    </>
  );
}

function SwitchRow({
  icon,
  name,
  sub,
  active,
  onClick,
}: {
  icon: React.ReactNode;
  name: string;
  sub: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex min-h-12 w-full items-center gap-3 rounded-lg px-3 py-2 text-left transition-colors",
        active ? "bg-accent text-accent-foreground" : "hover:bg-accent/60",
      )}
    >
      <span className="shrink-0">{icon}</span>
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-sm font-medium">{name}</span>
        <span className="truncate font-mono text-[11px] text-muted-foreground">{sub}</span>
      </span>
      {active && <Check className="size-4 shrink-0 text-success" />}
    </button>
  );
}
