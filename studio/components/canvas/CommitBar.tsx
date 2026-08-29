"use client";

import * as React from "react";
import { Button } from "@/components/ui/button";
import { canvasGitCommit, canvasGitPush, canvasGitStage } from "@/lib/canvas/api";
import { cn } from "@/lib/utils";

/**
 * CommitBar — committing in three taps, from wherever you are.
 *
 * WHY IT SITS HERE AND NOT IN A TAB
 *
 * The moment a turn ends with changed files, this appears above the
 * composer, in every layout mode. You should not have to go and find a
 * Changes tab to save work he just did — that trip is the reason edits sat
 * uncommitted for days.
 *
 * The message is already written from what he actually did, so committing is
 * reading one line and tapping once. Edit puts the cursor in it, Review opens
 * the diff.
 *
 * WHAT IT WILL NEVER DO: commit on its own. Push is a separate, deliberate
 * act. If you ignore the bar it simply stays until you deal with it — it
 * never times out and it never decides for you.
 */
export function CommitBar({
  repo,
  sessionId,
  fileCount,
  /** The one-line message, drafted from the turn. Empty = nothing to say yet. */
  message,
  onMessageChange,
  onReview,
  onCommitted,
  className,
}: {
  repo: string;
  sessionId?: string;
  fileCount: number;
  message: string;
  onMessageChange: (m: string) => void;
  onReview: () => void;
  onCommitted?: () => void;
  className?: string;
}) {
  const [editing, setEditing] = React.useState(false);
  const [busy, setBusy] = React.useState<"commit" | "push" | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const inputRef = React.useRef<HTMLInputElement | null>(null);

  if (fileCount <= 0) return null;

  async function run(push: boolean) {
    const msg = message.trim();
    if (!msg) {
      setEditing(true);
      inputRef.current?.focus();
      return;
    }
    setBusy(push ? "push" : "commit");
    setError(null);
    try {
      await canvasGitStage({ repo, session_id: sessionId });
      const res = await canvasGitCommit({ repo, message: msg, session_id: sessionId });
      // A queued Trust contract is not a failure — it means the boss has to
      // approve, and saying "committed" here would be a lie.
      if (res?.status === "pending") {
        setError("Waiting on your approval in Chat.");
        return;
      }
      if (res?.status === "denied") {
        setError(res.reason || "You denied that one.");
        return;
      }
      if (push) await canvasGitPush({ repo, session_id: sessionId });
      onCommitted?.();
    } catch {
      // Never let a failure read as a success: the bar stays, with the reason.
      setError("I could not commit that. The changes are still here.");
    } finally {
      setBusy(null);
    }
  }

  return (
    <div
      className={cn(
        "flex min-w-0 shrink-0 flex-col gap-2 border-t border-hairline bg-muted px-3 py-2.5",
        className,
      )}
    >
      <div className="flex min-w-0 items-center gap-2">
        <span className="size-1.5 shrink-0 rounded-full bg-warning" aria-hidden />
        {editing ? (
          <input
            ref={inputRef}
            value={message}
            onChange={(e) => onMessageChange(e.target.value)}
            onBlur={() => setEditing(false)}
            autoFocus
            aria-label="Commit message"
            className="h-7 min-w-0 flex-1 rounded-md bg-background px-2 text-[12px] outline-none ring-1 ring-ring"
          />
        ) : (
          <button
            type="button"
            onClick={() => setEditing(true)}
            className="min-w-0 flex-1 truncate text-left text-[11.5px] text-muted-foreground transition-colors hover:text-foreground"
          >
            {message.trim() || `${fileCount} file${fileCount === 1 ? "" : "s"} changed`}
          </button>
        )}
        <Button
          size="sm"
          variant="ghost"
          className="h-7 shrink-0 px-2 text-[11px]"
          onClick={onReview}
        >
          Review {fileCount}
        </Button>
        <Button
          size="sm"
          className="h-7 shrink-0 px-2.5 text-[11px]"
          disabled={busy !== null}
          onClick={() => void run(false)}
        >
          {busy === "commit" ? "Committing…" : "Commit"}
        </Button>
      </div>
      {error ? <p className="pl-3.5 text-[11px] text-danger">{error}</p> : null}
    </div>
  );
}
