"use client";

import { useCallback, useEffect, useState } from "react";
import {
  ArrowDownToLine,
  ArrowUpFromLine,
  GitBranch,
  GitCommit,
  Loader2,
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
 * CanvasGitPanel - VS Code Source Control parity, minus the heavy stuff.
 *
 * What it does:
 *   - Polls git status (read-only, no Trust prompt) every 4s while focused.
 *   - Click a changed file → opens Monaco in diff mode in the right pane.
 *   - Stage all / Commit / Push / Pull - each composes a single bash command
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
  // When true the commit flow continues straight into a push - that's the
  // "Push to GitHub" one-tap ship. Commit-only sets it false.
  const [pushAfterCommit, setPushAfterCommit] = useState(true);
  const isDesktop = useIsDesktop();

  const refresh = useCallback(async () => {
    if (!store.root) return;
    const next = await fetchCanvasGitStatus(store.root, sessionId ?? "");
    if (next) setStatus(next);
  }, [store.root, sessionId]);

  useEffect(() => {
    // When the workspace root clears (session has no project_path -
    // see CanvasFrame), drop any stale status so the panel can't
    // show ghost entries / counts alongside the "Set a workspace
    // root first" banner. Also stop the poll until root returns.
    if (!store.root) {
      setStatus(null);
      return;
    }
    void refresh();
    const id = setInterval(() => void refresh(), STATUS_POLL_MS);
    return () => clearInterval(id);
  }, [refresh, store.root]);

  // onCommit stages everything, commits, and (when invoked from "Push to
  // GitHub") pushes - so the boss never has to think about stage→commit→push
  // as three separate steps. Commit-only stops after the commit.
  const onCommit = async () => {
    const msg = commitMessage.trim();
    if (!msg) return;
    setBusy("commit");
    setToast(null);
    setCommitOpen(false);

    // Stage all first so untracked/unstaged files are included - no separate
    // "stage" step for the boss to remember. Harmless on an already-clean tree.
    await canvasGitStage({ repo: store.root, session_id: sessionId ?? undefined });

    const res = await canvasGitCommit({
      repo: store.root,
      message: msg,
      session_id: sessionId ?? undefined,
    });
    if (res?.status !== "executed") {
      setBusy(null);
      setToast(
        res?.contract_id
          ? { kind: "ok", text: "Approve in Trust to commit." }
          : { kind: "err", text: res?.reason ?? "Commit failed." },
      );
      return;
    }
    setCommitMessage("");

    if (!pushAfterCommit) {
      setBusy(null);
      setToast({ kind: "ok", text: "Committed." });
      void refresh();
      return;
    }

    // Continue straight into the push - this is the GitHub ship.
    setBusy("push");
    const pr = await canvasGitPush({
      repo: store.root,
      remote: "origin",
      branch: status?.branch,
      session_id: sessionId ?? undefined,
    });
    setBusy(null);
    if (pr?.status === "executed") {
      setToast({ kind: "ok", text: "Pushed to GitHub." });
    } else if (pr?.contract_id) {
      setToast({ kind: "ok", text: "Committed - approve in Trust to finish the push." });
    } else {
      setToast({ kind: "err", text: pr?.reason ?? "Committed, but push failed." });
    }
    void refresh();
  };

  // onShipToGitHub is the single "deploy my code to GitHub" action. If there's
  // uncommitted work, it opens the commit dialog (which then commits + pushes);
  // if the tree is clean but has unpushed commits, it pushes straight away.
  const onShipToGitHub = () => {
    const hasUncommitted = (status?.entries ?? []).length > 0 || store.dirtyPaths.size > 0;
    if (hasUncommitted) {
      setPushAfterCommit(true);
      setCommitOpen(true);
      return;
    }
    void onPush();
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
  // Files Jarvis wrote/edited this session (from tool calls), independent of
  // git. This is what makes edits show in Changes even when the workspace root
  // isn't a git repo (e.g. a /workspace scratch dir on the cloud computer).
  const sessionWrites = Array.from(store.dirtyPaths).sort();
  const hasChanges = (status?.entries ?? []).length > 0 || sessionWrites.length > 0;
  const canShip = hasChanges || (status?.ahead ?? 0) > 0;

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* Header */}
      <div className="flex items-center gap-1 border-b px-2 py-1.5">
        <div className="flex min-w-0 flex-1 items-center gap-1.5 text-xs text-muted-foreground">
          <GitBranch className="size-3.5 shrink-0" />
          <span className="truncate font-mono" title={status?.branch}>
            {status?.branch ?? "-"}
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

      {/* Actions - ONE obvious "ship to GitHub" primary (stage + commit + push
          in a single flow), with commit-only and pull as secondary. */}
      <div className="space-y-1.5 border-b px-2 py-2">
        <Button
          type="button"
          className="h-9 w-full justify-center gap-2 bg-brand font-medium text-brand-foreground hover:bg-brand/90"
          disabled={!store.root || !!busy || !canShip}
          onClick={onShipToGitHub}
          title="Stage, commit, and push everything to GitHub"
        >
          {busy === "push" || busy === "commit" ? (
            <Loader2 className="size-4 animate-spin" />
          ) : (
            <ArrowUpFromLine className="size-4" />
          )}
          Push to GitHub
          {(status?.ahead ?? 0) > 0 && !hasChanges && (
            <span className="font-mono text-[11px] opacity-80">↑{status?.ahead}</span>
          )}
        </Button>
        <div className="grid grid-cols-2 gap-1.5">
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="h-8 justify-center gap-1.5"
            disabled={!store.root || !!busy || !hasChanges}
            onClick={() => {
              setPushAfterCommit(false);
              setCommitOpen(true);
            }}
            title="Commit locally without pushing"
          >
            <GitCommit className="size-3.5" />
            Commit only
          </Button>
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="h-8 justify-center gap-1.5"
            disabled={!store.root || !!busy}
            onClick={() => void onPull()}
            title="Pull latest from GitHub"
          >
            {busy === "pull" ? (
              <Loader2 className="size-3.5 animate-spin" />
            ) : (
              <ArrowDownToLine className="size-3.5" />
            )}
            Pull
            {(status?.behind ?? 0) > 0 && (
              <span className="font-mono text-[11px] opacity-80">↓{status?.behind}</span>
            )}
          </Button>
        </div>
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
        {store.root && status === null && sessionWrites.length === 0 && (
          <div className="flex items-center justify-center py-8 text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
          </div>
        )}
        {/* What Jarvis touched this session — independent of git, with a count
            in the header ("This session · 5"). Shows files written to non-repo
            dirs (e.g. a /workspace scratch dir) that git status would miss.
            Clicking a row opens it in the canvas. */}
        {store.root && sessionWrites.length > 0 && (
          <GitGroup
            title="This session"
            entries={sessionWrites.map((p) => ({
              path:
                store.root && p.startsWith(store.root + "/")
                  ? p.slice(store.root.length + 1)
                  : p,
              status: "M",
              staged: false,
            }))}
            repo={store.root}
            onOpen={openFile}
          />
        )}
        {store.root && status && status.entries.length === 0 && sessionWrites.length === 0 && (
          <div className="px-3 py-6 text-xs text-muted-foreground">
            Working tree clean.
          </div>
        )}
        {store.root && staged.length > 0 && <GitGroup title="Staged" entries={staged} repo={store.root} onOpen={openFile} />}
        {store.root && unstaged.length > 0 && <GitGroup title="Changes" entries={unstaged} repo={store.root} onOpen={openFile} />}
        {store.root && untracked.length > 0 && <GitGroup title="Untracked" entries={untracked} repo={store.root} onOpen={openFile} />}
      </div>

      {/* Commit modal - Dialog on desktop, Drawer on mobile per project convention. */}
      {isDesktop ? (
        <Dialog open={commitOpen} onOpenChange={setCommitOpen}>
          <DialogContent className="max-w-lg gap-0 p-0">
            <DialogHeader>
              <DialogTitle>{pushAfterCommit ? "Push to GitHub" : "Commit changes"}</DialogTitle>
              <DialogDescription>
                {pushAfterCommit
                  ? "Stages everything, commits with this message, then pushes to GitHub."
                  : "Stages everything and commits locally - no push."}
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
              <Button
                className={cn(pushAfterCommit && "bg-brand text-brand-foreground hover:bg-brand/90")}
                onClick={() => void onCommit()}
                disabled={!commitMessage.trim() || busy === "commit" || busy === "push"}
              >
                {busy === "commit" || busy === "push" ? (
                  <Loader2 className="size-4 animate-spin" />
                ) : pushAfterCommit ? (
                  <ArrowUpFromLine className="size-4" />
                ) : (
                  <GitCommit className="size-4" />
                )}
                {pushAfterCommit ? "Commit & push" : "Commit"}
              </Button>
            </div>
          </DialogContent>
        </Dialog>
      ) : (
        <Drawer open={commitOpen} onOpenChange={setCommitOpen}>
          <DrawerContent>
            <DrawerHeader className="text-left">
              <DrawerTitle>{pushAfterCommit ? "Push to GitHub" : "Commit changes"}</DrawerTitle>
              <DrawerDescription>
                {pushAfterCommit
                  ? "Stages everything, commits with this message, then pushes to GitHub."
                  : "Stages everything and commits locally - no push."}
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
              <Button
                className={cn(pushAfterCommit && "bg-brand text-brand-foreground hover:bg-brand/90")}
                onClick={() => void onCommit()}
                disabled={!commitMessage.trim() || busy === "commit" || busy === "push"}
              >
                {busy === "commit" || busy === "push" ? (
                  <Loader2 className="size-4 animate-spin" />
                ) : pushAfterCommit ? (
                  <ArrowUpFromLine className="size-4" />
                ) : (
                  <GitCommit className="size-4" />
                )}
                {pushAfterCommit ? "Commit & push" : "Commit"}
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
