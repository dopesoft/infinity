"use client";

import * as React from "react";
import { FileText, FolderOpen } from "lucide-react";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useCanvasStore } from "@/lib/canvas/store";
import { fetchCanvasFSList } from "@/lib/canvas/api";

/**
 * FileSwitcher — what the file tree column becomes.
 *
 * A tree is only useful when you do not know a name, which is almost never
 * in a repo you know. So the tree stops being a permanent third column
 * costing 18% of the width, and becomes one keystroke.
 *
 * The files HE just touched are pinned at the top with the amber dirty dot,
 * because after he works those are the only ones you want, and hunting for
 * them in an alphabetised tree was the actual friction.
 *
 * HONEST LIMIT: Core has no recursive file index (`/api/canvas/fs/ls` lists
 * one directory), so this is NOT a whole-repo fuzzy find. It searches what it
 * can actually see — the files he touched, the tabs you have open, and the
 * folder you are in — and the empty state says so rather than implying the
 * file does not exist. Type a folder name to walk into it.
 */
export function FileSwitcher({
  open,
  onOpenChange,
  onPick,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onPick: (path: string) => void;
}) {
  const store = useCanvasStore();
  const [query, setQuery] = React.useState("");
  const [all, setAll] = React.useState<string[]>([]);
  const [dir, setDir] = React.useState("");
  const [loading, setLoading] = React.useState(false);

  // One listing per directory, not one per keystroke.
  React.useEffect(() => {
    if (!open) return;
    const ac = new AbortController();
    setLoading(true);
    void (async () => {
      const res = await fetchCanvasFSList(dir, "", ac.signal);
      const here = (res?.entries ?? []).map((e) => ({
        path: dir ? `${dir}/${e.name}` : e.name,
        isDir: e.type === "dir",
      }));
      setAll(here.filter((e) => !e.isDir).map((e) => e.path));
      setDirs(here.filter((e) => e.isDir).map((e) => e.path));
      setLoading(false);
    })();
    return () => ac.abort();
  }, [open, dir]);

  const [dirs, setDirs] = React.useState<string[]>([]);

  React.useEffect(() => {
    if (!open) {
      setQuery("");
      setDir("");
    }
  }, [open]);

  const dirty = React.useMemo(() => [...store.dirtyPaths], [store.dirtyPaths]);
  const q = query.trim().toLowerCase();
  const match = (p: string) => !q || p.toLowerCase().includes(q);

  const touched = dirty.filter(match);
  const rest = all.filter((p) => match(p) && !store.dirtyPaths.has(p)).slice(0, 40);
  const folders = dirs.filter(match).slice(0, 12);

  const go = (path: string) => {
    onOpenChange(false);
    onPick(path);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="top-[12%] max-w-lg translate-y-0 gap-0 overflow-hidden p-0">
        <DialogTitle className="sr-only">Open a file</DialogTitle>
        <Command shouldFilter={false} className="bg-transparent">
          <CommandInput value={query} onValueChange={setQuery} placeholder="A file he touched, or a folder to open" />
          <CommandList className="max-h-[min(55dvh,380px)]">
            {touched.length === 0 && rest.length === 0 && folders.length === 0 && (
              <CommandEmpty>
                {loading
                  ? "Looking…"
                  : "Nothing here matches. This looks in the files he touched and the folder you are in, not the whole repo."}
              </CommandEmpty>
            )}
            {touched.length > 0 && (
              <CommandGroup heading="He touched these">
                {touched.map((p) => (
                  <CommandItem key={p} value={p} onSelect={() => go(p)} className="gap-2.5">
                    <span className="size-1.5 shrink-0 rounded-full bg-warning" aria-hidden />
                    <span className="min-w-0 flex-1 truncate">{basename(p)}</span>
                    <span className="shrink-0 font-mono text-[10px] text-quiet">{dirname(p)}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
            {folders.length > 0 && (
              <CommandGroup heading="Folders">
                {folders.map((p) => (
                  <CommandItem
                    key={`dir:${p}`}
                    value={`dir:${p}`}
                    onSelect={() => {
                      setDir(p);
                      setQuery("");
                    }}
                    className="gap-2.5"
                  >
                    <FolderOpen className="size-3.5 shrink-0 text-quiet" aria-hidden />
                    <span className="min-w-0 flex-1 truncate">{basename(p)}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
            {rest.length > 0 && (
              <CommandGroup heading={dir || "In this folder"}>
                {rest.map((p) => (
                  <CommandItem key={p} value={p} onSelect={() => go(p)} className="gap-2.5">
                    <FileText className="size-3.5 shrink-0 text-quiet" aria-hidden />
                    <span className="min-w-0 flex-1 truncate">{basename(p)}</span>
                    <span className="shrink-0 font-mono text-[10px] text-quiet">{dirname(p)}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  );
}

function basename(p: string): string {
  return p.replace(/^.*[\\/]/, "");
}
function dirname(p: string): string {
  const parts = p.split("/");
  parts.pop();
  return parts.slice(-1)[0] ?? "";
}
