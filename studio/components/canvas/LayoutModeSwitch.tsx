"use client";

import * as React from "react";
import { useCanvasStore, type LayoutMode } from "@/lib/canvas/store";
import { Chip, ChipGroup } from "@/components/ui/chip";
import { cn } from "@/lib/utils";

/**
 * LayoutModeSwitch — Chat / Split / Build.
 *
 * One split was wrong because you do two different things in here. Reading a
 * diff while you talk wants the conversation big. Watching a preview redraw
 * itself while he types wants the browser big and the conversation reduced
 * to a running commentary down the side. Same page, one control.
 *
 * The dot means the layout is still choosing for itself. Moving this control
 * by hand turns that off for the rest of the session — auto that fights you
 * is worse than no auto, and that rule is what makes the other five safe.
 */
const MODES: { id: LayoutMode; label: string }[] = [
  { id: "chat", label: "Chat" },
  { id: "split", label: "Split" },
  { id: "build", label: "Build" },
];

export function LayoutModeSwitch({ className }: { className?: string }) {
  const store = useCanvasStore();
  return (
    <ChipGroup
      role="group"
      aria-label="Workbench width"
      pip={store.layoutAuto}
      pipTitle="Choosing for itself. Move this and it stops."
      className={cn("hidden lg:inline-flex", className)}
    >
      {MODES.map((m) => {
        const on = store.layout === m.id;
        return (
          <Chip
            key={m.id}
            raised={on}
            aria-pressed={on}
            onClick={() => store.setLayout(m.id)}
          >
            {m.label}
          </Chip>
        );
      })}
    </ChipGroup>
  );
}
