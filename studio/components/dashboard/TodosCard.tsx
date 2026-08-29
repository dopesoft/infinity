"use client";

import { motion } from "framer-motion";
import { Check, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ListRow } from "@/components/ui/list-row";
import { Section } from "./Section";
import { ScrollList } from "./ScrollList";
import { DASHBOARD_LIST_ROWS } from "./listHeight";
import { cn } from "@/lib/utils";
import { dayLabel } from "@/lib/dashboard/format";
import type { DashboardItem, Todo } from "@/lib/dashboard/types";

/* Todos - your tasks, not the agent's work board.
 *
 * Each row is tappable to open the ObjectViewer. The check button toggles
 * inline (optimistic). Rows the agent created say so in their meta line -
 * visible proof Jarvis is filling these in for you.
 *
 * MAJORDOMO SWEEP: the row is a `ListRow` with the tick box in the new
 * `leadingAction` slot, so the checkbox sits OUTSIDE the row's own button
 * instead of the card hand-rolling a two-button flex to avoid nesting them.
 * The "agent" / "email" source pills and the due-date stamp were three
 * separate coloured badges; they are one quiet meta line now. "Add todo" was a
 * full-width dashed rectangle - §1.2 has no dashed boxes, so it is a plain
 * ghost button, which is also a 44px touch target for the first time.
 */
export function TodosCard({
  todos,
  onOpen,
  onToggle,
  onAdd,
  matchHeight,
}: {
  todos: Todo[];
  onOpen: (item: DashboardItem) => void;
  onToggle: (id: string) => void;
  onAdd: () => void;
  /** Legacy explicit pixel cap (ScrollList "matched" mode). No longer threaded
   *  by the dashboard - see `./listHeight` for why. */
  matchHeight?: number | null;
}) {
  const open = todos.filter((t) => !t.done);
  const doneCount = todos.length - open.length;

  return (
    <Section
      title="To do"
      badge={open.length}
      action={doneCount > 0 ? { label: `${doneCount} done`, href: "/memory" } : undefined}
    >
      <div className="min-w-0">
        {open.length === 0 ? (
          <p className="py-2 text-[13px] text-quiet">Nothing on your list.</p>
        ) : (
          <ScrollList max={DASHBOARD_LIST_ROWS} maxHeight={matchHeight ?? undefined}>
            <div className="flex min-w-0 flex-col">
              {open.map((t) => (
                <TodoRow
                  key={t.id}
                  t={t}
                  onOpen={() => onOpen({ kind: "todo", data: t })}
                  onToggle={() => onToggle(t.id)}
                />
              ))}
            </div>
          </ScrollList>
        )}

        <Button
          type="button"
          variant="ghost"
          onClick={onAdd}
          className="mt-1 h-11 w-full justify-start px-0 text-[13.5px] font-medium text-quiet hover:bg-transparent hover:text-foreground"
        >
          <Plus className="size-4" aria-hidden />
          Add todo
        </Button>
      </div>
    </Section>
  );
}

function TodoRow({
  t,
  onOpen,
  onToggle,
}: {
  t: Todo;
  onOpen: () => void;
  onToggle: () => void;
}) {
  // Due dates carry the urgency: high priority reads danger, medium warning,
  // everything else stays grey (§1.4 - colour means something).
  const dueTone =
    t.priority === "high" ? "text-danger" : t.priority === "med" ? "text-warning" : "text-quiet";
  const source = t.source === "agent" ? "from Jarvis" : t.source === "email" ? "from email" : "";

  return (
    <ListRow
      title={t.title}
      onClick={onOpen}
      meta={
        source ? (
          <span>{source}</span>
        ) : null
      }
      leadingAction={
        <motion.button
          type="button"
          whileTap={{ scale: 0.85 }}
          onClick={(e) => {
            e.stopPropagation();
            onToggle();
          }}
          aria-label="Mark done"
          /* 18px on the page, 44px to a thumb: the padding grows the hit area
             and the matching negative margin gives the space back to layout,
             so the tick lines up with every other row's 18px leading glyph
             while still clearing the touch-target rule. */
          className="group -m-[13px] inline-flex size-11 shrink-0 items-center justify-center text-quiet transition-colors hover:text-foreground"
        >
          <span className="inline-flex size-[18px] items-center justify-center rounded-full border border-border transition-colors group-hover:border-foreground/50">
            <Check
              className="size-3 opacity-0 transition-opacity group-hover:opacity-100"
              aria-hidden
            />
          </span>
        </motion.button>
      }
      trailing={
        t.dueAt ? (
          <span
            className={cn("shrink-0 text-[12px] tabular-nums", dueTone)}
            suppressHydrationWarning
          >
            {dayLabel(t.dueAt).toLowerCase()}
          </span>
        ) : null
      }
    />
  );
}
