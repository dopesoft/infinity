"use client";

import * as React from "react";
import { PanelRight } from "lucide-react";
import { LayoutModeSwitch } from "@/components/canvas/LayoutModeSwitch";
import { Chip, ChipGroup } from "@/components/ui/chip";
import { useCanvasStore } from "@/lib/canvas/store";

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
      <ChipGroup
        count={changes}
        countLabel={`${changes} files differ from HEAD`}
        className="lg:hidden"
      >
        <Chip
          iconOnly
          raised={open}
          icon={<PanelRight />}
          onClick={() => store.setLayout(open ? "chat" : "split")}
          aria-pressed={open}
          aria-label={
            open
              ? "Close the workbench"
              : "Open the workbench: files, browser, changes and what he made"
          }
          title="Files, browser, changes and what he made"
        />
      </ChipGroup>
    </>
  );
}
