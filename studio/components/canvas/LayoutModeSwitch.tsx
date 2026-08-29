"use client";

import * as React from "react";
import { useCanvasStore, type LayoutMode } from "@/lib/canvas/store";
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
    <span
      role="group"
      aria-label="Workbench width"
      className={cn("relative hidden items-center gap-px rounded-lg bg-muted p-0.5 lg:flex", className)}
    >
      {store.layoutAuto ? (
        <span
          aria-label="Choosing for itself"
          title="Choosing for itself. Move this and it stops."
          className="absolute -right-0.5 -top-0.5 size-1.5 rounded-full bg-brand"
        />
      ) : null}
      {MODES.map((m) => {
        const on = store.layout === m.id;
        return (
          <button
            key={m.id}
            type="button"
            aria-pressed={on}
            onClick={() => store.setLayout(m.id)}
            className={cn(
              "h-5 rounded-md px-2 text-[10.5px] transition-colors",
              on ? "bg-background text-foreground shadow-sm" : "text-quiet hover:text-foreground",
            )}
          >
            {m.label}
          </button>
        );
      })}
    </span>
  );
}
