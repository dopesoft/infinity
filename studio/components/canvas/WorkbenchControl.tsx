"use client";

import * as React from "react";
import { PanelRight } from "lucide-react";
import { LayoutModeSwitch } from "@/components/canvas/LayoutModeSwitch";
import { useCanvasStore } from "@/lib/canvas/store";
import { cn } from "@/lib/utils";

/**
 * WorkbenchControl — the door to the workbench, and the only one.
 *
 * THE BUG THIS FIXES. `LayoutModeSwitch` was rendered inside the workbench
 * pane, and the pane only renders once the layout is already open. So from
 * the default state — the conversation is the page — there was no control
 * anywhere that opened the files, the browser, the changes or what he made.
 * The only ways in were the layout guessing on its own (the agent writes a
 * file) or the commit bar's Review, which needs pending changes to exist.
 * A closed door with the handle on the inside.
 *
 * It lives in the session header now: the row he is already looking at,
 * always mounted, in every layout mode.
 *
 * Two form factors, one job, both owned here rather than by the consumer:
 *
 *   lg+     the three widths — Chat / Split / Build — because on a desktop
 *           the question is how much room the work gets, not whether.
 *   below   one button, because on a phone the workbench is a sheet and the
 *           only question is open or shut. It carries the pending-changes
 *           count, since that is the state you would want to be interrupted
 *           for; nothing else earns a badge.
 */
export function WorkbenchControl({
  changes = 0,
  className,
}: {
  /** Files differing from HEAD. 0 renders no badge. */
  changes?: number;
  className?: string;
}) {
  const store = useCanvasStore();
  const open = store.layout !== "chat";

  return (
    <>
      <LayoutModeSwitch className={className} />
      <button
        type="button"
        onClick={() => store.setLayout(open ? "chat" : "split")}
        aria-pressed={open}
        aria-label={
          open
            ? "Close the workbench"
            : "Open the workbench: files, browser, changes and what he made"
        }
        title="Files, browser, changes and what he made"
        /* The `after` pseudo is the touch target: 44px to a thumb without
           the button itself growing past the 40px header row. */
        className={cn(
          "relative inline-flex size-8 shrink-0 items-center justify-center rounded-lg transition-colors lg:hidden",
          "after:absolute after:left-1/2 after:top-1/2 after:size-11 after:-translate-x-1/2 after:-translate-y-1/2 after:content-['']",
          open ? "bg-muted text-foreground" : "text-quiet hover:bg-accent hover:text-foreground",
        )}
      >
        <PanelRight className="size-4" aria-hidden />
        {changes > 0 ? (
          <span
            className="absolute -right-1 -top-1 grid min-w-[1rem] place-items-center rounded-full bg-warning px-1 font-mono text-[9px] leading-4 tabular-nums text-warning-foreground"
            aria-hidden
          >
            {changes > 99 ? "99+" : changes}
          </span>
        ) : null}
      </button>
    </>
  );
}
