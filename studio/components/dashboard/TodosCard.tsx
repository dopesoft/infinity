"use client";

import { motion } from "framer-motion";
import { CheckCheck, Circle, ListTodo, Plus, Sparkles } from "lucide-react";
import { Section } from "./Section";
import { cn } from "@/lib/utils";
import { dayLabel } from "@/lib/dashboard/format";
import type { DashboardItem, Todo } from "@/lib/dashboard/types";

/* Todos — your tasks, not the agent's work board.
 *
 * Each row is tappable to open the ObjectViewer. The check button toggles
 * inline (optimistic). Source badge on rows the agent created — visible
 * proof Jarvis is filling these in for you.
 */
export function TodosCard({
  todos,
  onOpen,
  onToggle,
}: {
  todos: Todo[];
  onOpen: (item: DashboardItem) => void;
  onToggle: (id: string) => void;
}) {
  const open = todos.filter((t) => !t.done);
  const doneCount = todos.length - open.length;

  return (
    <Section
      title="Todos"
      Icon={ListTodo}
      delay={0.1}
      badge={open.length}
      action={doneCount > 0 ? { label: `${doneCount} done`, href: "/memory" } : undefined}
    >
      <div className="space-y-2 rounded-xl border bg-card p-3">
        <ul className="space-y-1">
          {open.map((t) => (
            <TodoRow
              key={t.id}
              t={t}
              onOpen={() => onOpen({ kind: "todo", data: t })}
              onToggle={() => onToggle(t.id)}
            />
          ))}
        </ul>

        <button
          type="button"
          className="inline-flex w-full items-center justify-center gap-1 rounded-md border border-dashed border-border py-2 text-xs text-muted-foreground transition-colors hover:border-foreground/30 hover:text-foreground"
        >
          <Plus className="size-3.5" aria-hidden />
          Add todo
        </button>
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
  const priorityTone =
    t.priority === "high" ? "text-danger" : t.priority === "med" ? "text-rose-400" : "text-muted-foreground";
  return (
    <li className="group flex items-center gap-2">
      <motion.button
        type="button"
        whileTap={{ scale: 0.85 }}
        onClick={(e) => {
          e.stopPropagation();
          onToggle();
        }}
        aria-label="Mark done"
        className={cn(
          "inline-flex size-7 shrink-0 items-center justify-center rounded-full border transition-all",
          "border-border bg-background hover:border-foreground/40",
        )}
      >
        <Circle className="size-3.5 text-muted-foreground/60 transition-opacity group-hover:opacity-100" aria-hidden />
      </motion.button>
      <button
        type="button"
        onClick={onOpen}
        className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-accent/50"
      >
        <span className="min-w-0 flex-1 truncate text-sm text-foreground">{t.title}</span>
        {t.source === "agent" ? (
          <span
            aria-label="Created by Jarvis"
            title="Created by Jarvis"
            className="inline-flex items-center gap-0.5 rounded-full bg-info/10 px-1.5 py-0.5 text-[10px] font-mono uppercase tracking-wider text-info"
          >
            <Sparkles className="size-2.5" aria-hidden />
            agent
          </span>
        ) : t.source === "email" ? (
          <span className="rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-mono uppercase tracking-wider text-muted-foreground">
            email
          </span>
        ) : null}
        {t.dueAt ? (
          <span
            className={cn(
              "shrink-0 font-mono text-[10px] uppercase tracking-wider",
              priorityTone,
            )}
            suppressHydrationWarning
          >
            {dayLabel(t.dueAt).toLowerCase()}
          </span>
        ) : null}
      </button>
    </li>
  );
}

// (CheckCheck import is referenced for tree-shake safety even though we use Circle.)
void CheckCheck;
