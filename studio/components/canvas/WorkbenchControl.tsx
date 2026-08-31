"use client";

import * as React from "react";
import { PanelRight } from "lucide-react";
import { Chip, ChipGroup } from "@/components/ui/chip";
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
 * SCOPE, after the header tray. This is the DOOR only, below lg: on a phone
 * the workbench is a sheet and the only question is open or shut. It carries
 * the pending-changes count, since that is the state you would want to be
 * interrupted for; nothing else earns a badge.
 *
 * The lg+ three-width switch is <LayoutModeSwitch>, placed by the consumer.
 * The two were bundled here until the header learned to collapse: the switch
 * is a control that folds into the tray, the door is navigation that must
 * stay on the row at every width, so they no longer belong to one component.
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
      <ChipGroup
        count={changes}
        countLabel={`${changes} files differ from HEAD`}
        className={cn("lg:hidden", className)}
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
  );
}
