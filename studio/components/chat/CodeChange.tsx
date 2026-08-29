"use client";

import * as React from "react";

import { Inset } from "@/components/ui/inset";
import { buildCodeChange, changeStats } from "@/lib/diff";
import { cn } from "@/lib/utils";

/**
 * CodeChangeView — the file being written, and what changed in it.
 *
 * The boss's complaint, verbatim: "UNLIKE claude which shows the file its
 * working on and a sampling of the code written … I notice files popping up in
 * my changes tab, so this system is totally not transparent." An edit step used
 * to render `new_string` as an untinted mono blob inside an Inset — you could
 * see that something had been written, never what changed about it, and never
 * which file without opening the row's key/value grid.
 *
 * So a write step now reads the way a diff reads anywhere else: the path with
 * the filename carrying the weight, `+142 −8` beside it, and a real tinted hunk
 * underneath.
 *
 * BRIDGE-AGNOSTIC BY CONSTRUCTION. It takes a path and a before/after pair, so
 * it renders identically for `claude_code__edit` on the Mac, `fs_edit` in the
 * cloud workspace, and a nested step forwarded out of a Claude Code run. The
 * bridge decides which model writes the code; it was never supposed to decide
 * how much of it the boss can see.
 *
 * Reuse-first: the hunk goes through the existing `<Inset variant="diff">`, so
 * the tinting, the scroller, the overflow discipline and the radius are the
 * ones every other diff in Studio already uses. Nothing here is a new box.
 */
export function CodeChangeView({
  path,
  before,
  after,
  moreFiles = 0,
  className,
}: {
  path?: string;
  before?: string;
  after?: string;
  /** Other files touched by the same call, named on one quiet line. */
  moreFiles?: number;
  className?: string;
}) {
  const change = React.useMemo(
    () => buildCodeChange(before ?? "", after ?? ""),
    [before, after],
  );
  if (!change.unified && !path) return null;

  const stats = changeStats(change);
  return (
    <div className={cn("min-w-0 max-w-full space-y-1.5", className)}>
      {path ? <FileHeading path={path} stats={stats} added={change.added} removed={change.removed} /> : null}
      {change.unified ? <Inset variant="diff" text={change.unified} /> : null}
      {change.hidden > 0 || moreFiles > 0 ? (
        <p className="font-sans text-[12px] text-quiet">
          {change.hidden > 0
            ? `+${change.hidden} more line${change.hidden === 1 ? "" : "s"} in this change`
            : null}
          {change.hidden > 0 && moreFiles > 0 ? " · " : null}
          {moreFiles > 0
            ? `${moreFiles} more file${moreFiles === 1 ? "" : "s"} in this call`
            : null}
        </p>
      ) : null}
    </div>
  );
}

/**
 * The path, read the way a person reads one: the directory recedes, the
 * filename carries, and the counts sit at the end in tabular figures so a
 * column of them lines up.
 */
function FileHeading({
  path,
  stats,
  added,
  removed,
}: {
  path: string;
  stats: string;
  added: number;
  removed: number;
}) {
  const cut = path.lastIndexOf("/");
  const dir = cut >= 0 ? path.slice(0, cut + 1) : "";
  const name = cut >= 0 ? path.slice(cut + 1) : path;
  return (
    <div className="flex min-w-0 items-baseline gap-2">
      <span className="min-w-0 flex-1 truncate font-mono text-[12px]" title={path}>
        {dir ? <span className="text-quiet">{dir}</span> : null}
        <span className="text-foreground">{name}</span>
      </span>
      {stats ? (
        <span className="shrink-0 font-mono text-[12px] tabular-nums">
          {added > 0 ? <span className="text-success">+{added}</span> : null}
          {added > 0 && removed > 0 ? <span className="text-quiet"> </span> : null}
          {removed > 0 ? <span className="text-danger">−{removed}</span> : null}
        </span>
      ) : null}
    </div>
  );
}
