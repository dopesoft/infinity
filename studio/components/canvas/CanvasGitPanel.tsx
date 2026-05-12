"use client";

import { useCallback, useEffect, useState } from "react";
import {
  ArrowDownToLine,
  ArrowUpFromLine,
  GitBranch,
  GitCommit,
  Loader2,
  Plus,
  RefreshCw,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Drawer,
  DrawerContent,
  DrawerHeader,
  DrawerTitle,
  DrawerDescription,
  DrawerFooter,
  DrawerClose,
} from "@/components/ui/drawer";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogClose,
} from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import { useIsDesktop } from "@/lib/use-media-query";
import { useCanvasStore } from "@/lib/canvas/store";
import {
  fetchCanvasGitStatus,
  canvasGitStage,
  canvasGitCommit,
  canvasGitPush,
  canvasGitPull,
  type GitStatusResponse,
  type GitStatusEntry,
} from "@/lib/canvas/api";
import { cn } from "@/lib/utils";

/**
 * CanvasGitPanel — VS Code Source Control parity, minus the heavy stuff.
 *
 * What it does:
 *   - Polls git status (read-only, no Trust prompt) every 4s while focused.
 *   - Click a changed file → opens Monaco in diff mode in the right pane.
 *   - Stage all / Commit / Push / Pull — each composes a single bash command
 *     and queues a Trust contract. The boss approves in Trust tab (or inline
 *     from the contract toast). After approval, the API endpoint runs the
 *     command and echoes output back here as a toast/banner.
 *
 * What it deliberately doesn't do (yet):
 *   - Branch switching, merge conflict resolution, rebase, cherry-pick.
 *     These need richer UI affordances (branch picker, conflict view) that
 *     don't ship in v1 of Canvas. Drop to the terminal on the Mac.
 */

const STATUS_POLL_MS = 4_000;

export function CanvasGitPanel({
  sessionId,
  onFileOpen,
}: {
  sessionId: string | null;
  onFileOpen?: (path: string) => void;
}) {
  const store = useCanvasStore();
  const openFile = (p: string) => (onFileOpen ? onFileOpen(p) : store.openFile(p));
  const [status, setStatus] = useState<GitStatusResponse | null>(null);
  const [busy, setBusy] = useState<string | null>(null); // "stage" | "commit" | "push" | "pull"
  const [toast, setToast] = useState<{ kind: "ok" | "err"; text: string } | null>(null);
  const [commitOpen, setCommitOpen] = useState(false);
  const [commitMessage, setCommitMessage] = useState("");
  const isDesktop = useIsDesktop();

  const refresh = useCallback(async () => {
    if (!store.root) return;
    const next = await fetchCanvasGitStatus(store.root);
    if (next) setStatus(next);
  }, [store.root]);

  useEffect(() => {
    void refresh();
    if (!store.root) return;
    const id = setInterval(() => void refresh(), STATUS_POLL_MS);
    return () => clearInterval(id);
  }, [refresh, store.root]);

  const onStageAll = async () => {
    setBusy("stage");
    setToast(null);
    const res = await canvasGitStage({ repo: store.root, session_id: sessionId ?? undefined });
    setBusy(null);
    if (res?.status === "executed") {
      setToast({ kind: "ok", text: "Staged." });
      void refresh();
    } else if (res?.contract_id) {
      setToast({
        kind: "ok",
        text: "Approve in Trust to stage.",
      });
    } else {
      setToast({ kind: "err", text: res?.reason ?? "Stage failed." });
    }
  };

  const onCommit = async () => {
    const msg = commitMessage.trim();
    if (!msg) return;
    setBusy("commit");
    setToast(null);
    setCommitOpen(false);
    const res = await canvasGitCommit({
      repo: store.root,
      message: msg,
      session_id: sessionId ?? undefined,
    });
    setBusy(null);
    if (res?.status === "executed") {
      setToast({ kind: "ok", text: "Committed." });
      setCommitMessage("");
      void refresh();
    } else if (res?.contract_id) {
      setToast({ kind: "ok", text: "Approve in Trust to commit." });
    } else {
      setToast({ kind: "err", text: res?.reason ?? "Commit failed." });
    }
  };

  const onPush = async () => {
    setBusy("push");
    setToast(null);
    const res = await canvasGitPush({
      repo: store.root,
      remote: "origin",
      branch: status?.branch,
      session_id: sessionId ?? undefined,
    });
    setBusy(null);
    if (res?.status === "executed") {
      setToast({ kind: "ok", text: "Pushed." });
      void refresh();
    } else if (res?.contract_id) {
      setToast({ kind: "ok", text: "Approve in Trust to push." });
    } else {
      setToast({ kind: "err", text: res?.reason ?? "Push failed." });
    }
  };

  const onPull = async () => {
    setBusy("pull");
    setToast(null);
    const res = await canvasGitPull({
      repo: store.root,
      remote: "origin",
      branch: status?.branch,
      session_id: sessionId ?? undefined,
    });
    setBusy(null);
    if (res?.status === "executed") {
      setToast({ kind: "ok", text: "Pulled." });
      void refresh();
    } else if (res?.contract_id) {
      setToast({ kind: "ok", text: "Approve in Trust to pull." });
    } else {
      setToast({ kind: "err", text: res?.reason ?? "Pull failed." });
    }
  };

  const staged = (status?.entries ?? []).filter((e) => e.staged);
  const unstaged = (status?.entries ?? []).filter((e) => !e.staged && e.status !== "U");
  const untracked = (status?.entries ?? []).filter((e) => e.status === "U");

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* Header */}
      <div className="flex items-center gap-1 border-b px-2 py-1.5">
        <div className="flex min-w-0 flex-1 items-center gap-1.5 text-xs text-muted-foreground">
          <GitBranch className="size-3.5 shrink-0" />
          <span className="truncate font-mono" title={status?.branch}>
            {status?.branch ?? "—"}
          </span>
          {status && (status.ahead > 0 || status.behind > 0) && (
            <span className="ml-1 font-mono text-[10px]">
              {status.ahead > 0 && <span title="commits ahead">↑{status.ahead}</span>}
              {status.behind > 0 && <span className="ml-0.5" title="commits behind">↓{status.behind}</span>}
            </span>
          )}
        </div>
        <Button
          type="button"
          size="icon"
          variant="ghost"
          className="h-8 w-8 shrink-0"
          onClick={() => void refresh()}
          aria-label="Refresh git status"
          title="Refresh"
        >
          <RefreshCw className="size-3.5" />
        </Button>
      </div>

      {/* Actions */}
      <div className="grid grid-cols-2 gap-1 border-b px-2 py-1.5 text-xs">
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="h-8 justify-start gap-1.5"
          disabled={!!busy || (status?.entries ?? []).length === 0}
          onClick={() => void onStageAll()}
        >
          {busy === "stage" ? <Loader2 className="size-3.5 animate-spin" /> : <Plus className="size-3.5" />}
          Stage all
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="h-8 justify-start gap-1.5"
          disabled={!!busy || staged.length === 0}
          onClick={() => setCommitOpen(true)}
        >
          <GitCommit className="size-3.5" />
          Commit
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="h-8 justify-start gap-1.5"
          disabled={!!busy}
          onClick={() => void onPush()}
        >
          {busy === "push" ? <Loader2 className="size-3.5 animate-spin" /> : <ArrowUpFromLine className="size-3.5" />}
          Push
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="h-8 justify-start gap-1.5"
          disabled={!!busy}
          onClick={() => void onPull()}
        >
          {busy === "pull" ? <Loader2 className="size-3.5 animate-spin" /> : <ArrowDownToLine className="size-3.5" />}
          Pull
        </Button>
      </div>

      {/* Toast */}
      {toast && (
        <div
          className={cn(
            "border-b px-2 py-1.5 text-xs",
            toast.kind === "ok" ? "bg-success/10 text-success" : "bg-danger/10 text-danger",
          )}
        >
          {toast.text}
        </div>
      )}

      {/* Status list */}
      <div className="min-h-0 flex-1 overflow-y-auto scroll-touch py-1 text-sm">
        {!store.root && (
          <div className="px-3 py-6 text-xs text-muted-foreground">
            Set a workspace root first.
          </div>
        )}
        {store.root && status === null && (
          <div className="flex items-center justify-center py-8 text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
          </div>
        )}
        {store.root && status && status.entries.length === 0 && (
          <div className="px-3 py-6 text-xs text-muted-foreground">
            Working tree clean.
          </div>
        )}
        {staged.length > 0 && <GitGroup title="Staged" entries={staged} repo={store.root} onOpen={openFile} />}
        {unstaged.length > 0 && <GitGroup title="Changes" entries={unstaged} repo={store.root} onOpen={openFile} />}
        {untracked.length > 0 && <GitGroup title="Untracked" entries={untracked} repo={store.root} onOpen={openFile} />}
      </div>

      {/* Commit modal — Dialog on desktop, Drawer on mobile per project convention. */}
      {isDesktop ? (
        <Dialog open={commitOpen} onOpenChange={setCommitOpen}>
          <DialogContent className="max-w-lg gap-0 p-0">
            <DialogHeader>
              <DialogTitle>Commit changes</DialogTitle>
              <DialogDescription>
                {staged.length} file{staged.length === 1 ? "" : "s"} staged.
                This will queue a Trust contract on your Mac.
              </DialogDescription>
            </DialogHeader>
            <div className="px-4">
              <Textarea
                value={commitMessage}
                onChange={(e) => setCommitMessage(e.target.value)}
                placeholder="Commit message…"
                rows={4}
                className="min-h-24 font-mono text-sm"
                autoFocus
              />
            </div>
            <div className="flex flex-row justify-end gap-2 p-4">
              <DialogClose asChild>
                <Button variant="ghost">Cancel</Button>
              </DialogClose>
              <Button onClick={() => void onCommit()} disabled={!commitMessage.trim() || busy === "commit"}>
                {busy === "commit" ? <Loader2 className="size-4 animate-spin" /> : <GitCommit className="size-4" />}
                Commit
              </Button>
            </div>
          </DialogContent>
        </Dialog>
      ) : (
        <Drawer open={commitOpen} onOpenChange={setCommitOpen}>
          <DrawerContent>
            <DrawerHeader className="text-left">
              <DrawerTitle>Commit changes</DrawerTitle>
              <DrawerDescription>
                {staged.length} file{staged.length === 1 ? "" : "s"} staged.
                This will queue a Trust contract on your Mac.
              </DrawerDescription>
            </DrawerHeader>
            <div className="px-4">
              <Textarea
                value={commitMessage}
                onChange={(e) => setCommitMessage(e.target.value)}
                placeholder="Commit message…"
                rows={4}
                className="min-h-24 font-mono text-sm"
                autoFocus
              />
            </div>
            <DrawerFooter className="flex flex-row justify-end gap-2">
              <DrawerClose asChild>
                <Button variant="ghost">Cancel</Button>
              </DrawerClose>
              <Button onClick={() => void onCommit()} disabled={!commitMessage.trim() || busy === "commit"}>
                {busy === "commit" ? <Loader2 className="size-4 animate-spin" /> : <GitCommit className="size-4" />}
                Commit
              </Button>
            </DrawerFooter>
          </DrawerContent>
        </Drawer>
      )}
    </div>
  );
}

function GitGroup({
  title,
  entries,
  repo,
  onOpen,
}: {
  title: string;
  entries: GitStatusEntry[];
  repo: string;
  onOpen: (p: string) => void;
}) {
  const store = useCanvasStore();
  return (
    <div className="mb-1">
      <div className="flex items-center justify-between px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
        <span>{title}</span>
        <span className="font-mono text-[10px]">{entries.length}</span>
      </div>
      {entries.map((e) => {
        const fullPath = e.path.startsWith("/") ? e.path : `${repo}/${e.path}`;
        const isActive = store.activeTabId === `file:${fullPath}`;
        return (
          <button
            key={e.path}
            type="button"
            onClick={() => onOpen(fullPath)}
            className={cn(
              "flex w-full min-h-7 items-center gap-2 px-2 py-1 text-left text-[13px] transition-colors",
              isActive ? "bg-accent text-accent-foreground" : "hover:bg-accent/60",
            )}
            title={fullPath}
          >
            <StatusBadge letter={e.status} />
            <span className="min-w-0 flex-1 truncate font-mono text-[12px]">{e.path}</span>
          </button>
        );
      })}
    </div>
  );
}

function StatusBadge({ letter }: { letter: string }) {
  const map: Record<string, { bg: string; fg: string }> = {
    M: { bg: "bg-warning/15", fg: "text-warning" },
    A: { bg: "bg-success/15", fg: "text-success" },
    D: { bg: "bg-danger/15", fg: "text-danger" },
    R: { bg: "bg-info/15", fg: "text-info" },
    U: { bg: "bg-muted", fg: "text-muted-foreground" },
  };
  const c = map[letter] ?? map.U;
  return (
    <span
      className={cn(
        "inline-flex size-4 shrink-0 items-center justify-center rounded font-mono text-[9px] font-bold",
        c.bg,
        c.fg,
      )}
    >
      {letter || "?"}
    </span>
  );
}
