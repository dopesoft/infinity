"use client";

import { useEffect, useState } from "react";
import { CalendarDays, ListTodo, Loader2 } from "lucide-react";
import { ResponsiveModal, ResponsiveModalHeader } from "@/components/ui/responsive-modal";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import { Chip, ChipGroup } from "@/components/ui/chip";
import { cn } from "@/lib/utils";
import { createTodo } from "@/lib/api";
import { useIsDesktop } from "@/lib/use-media-query";
import type { Todo } from "@/lib/dashboard/types";

/* AddTodoModal - the boss-facing "Add todo" form.
 *
 * Routes through ResponsiveModal (Dialog on lg+, Drawer on mobile) per the
 * Studio modal contract. Writes through createTodo → POST /api/tasks/create,
 * which lands the row in mem_tasks with source='manual'. That's the same
 * table Jarvis reads via task_list, so a todo typed here is visible to the
 * agent immediately - no separate store, no sync.
 *
 * Optionality offered: title (required), notes, priority (low/med/high),
 * and a due date with quick-pick chips (Today / Tomorrow / Next week) plus
 * a custom date picker.
 */

const PRIORITIES: { value: Todo["priority"]; label: string; tone: string }[] = [
  { value: "low", label: "Low", tone: "data-[on=true]:bg-muted data-[on=true]:text-foreground" },
  { value: "med", label: "Medium", tone: "data-[on=true]:bg-rose-400/15 data-[on=true]:text-rose-400" },
  { value: "high", label: "High", tone: "data-[on=true]:bg-danger/15 data-[on=true]:text-danger" },
];

// toISODate formats a Date as YYYY-MM-DD in the boss's local timezone (the
// shape <input type="date"> expects). Computed in handlers only, never in a
// useState initializer - keeps SSR/CSR hydration in agreement.
function toISODate(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

function dueChip(offsetDays: number): string {
  const d = new Date();
  d.setDate(d.getDate() + offsetDays);
  return toISODate(d);
}

export function AddTodoModal({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Fired after a successful create so the dashboard can refetch as a
   *  belt-and-braces fallback to the realtime mem_tasks subscription. */
  onCreated?: () => void;
}) {
  const [title, setTitle] = useState("");
  const [notes, setNotes] = useState("");
  const [priority, setPriority] = useState<Todo["priority"]>("med");
  const [due, setDue] = useState(""); // YYYY-MM-DD or ""
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const isDesktop = useIsDesktop();

  // Reset the form each time the modal opens so a previous draft never
  // bleeds into a fresh todo.
  useEffect(() => {
    if (open) {
      setTitle("");
      setNotes("");
      setPriority("med");
      setDue("");
      setSaving(false);
      setError(null);
    }
  }, [open]);

  const canSubmit = title.trim().length > 0 && !saving;

  async function submit() {
    if (!canSubmit) return;
    setSaving(true);
    setError(null);
    const res = await createTodo({
      title: title.trim(),
      body: notes.trim() || undefined,
      priority,
      due_at: due || undefined,
    });
    if (!res) {
      setSaving(false);
      setError("Couldn't save that todo. Try again.");
      return;
    }
    onCreated?.();
    onOpenChange(false);
  }

  const quickDue: { label: string; value: string }[] = [
    { label: "Today", value: dueChip(0) },
    { label: "Tomorrow", value: dueChip(1) },
    { label: "Next week", value: dueChip(7) },
  ];

  return (
    <ResponsiveModal
      open={open}
      onOpenChange={onOpenChange}
      title="Add todo"
      description="Lands on your dashboard and is visible to Jarvis."
      size="md"
      header={
        <ResponsiveModalHeader
          icon={<ListTodo className="size-4" />}
          title="Add todo"
          subtitle="Visible to Jarvis the moment you save it"
        />
      }
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={saving}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!canSubmit}>
            {saving ? <Loader2 className="size-4 animate-spin" /> : null}
            {saving ? "Adding…" : "Add todo"}
          </Button>
        </>
      }
    >
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        {/* Title */}
        <div className="space-y-1.5">
          <Label htmlFor="todo-title">Title</Label>
          <Input
            id="todo-title"
            autoFocus={isDesktop}
            inputMode="text"
            placeholder="Call insurance about the claim"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            onKeyDown={(e) => {
              // Enter submits from the single-line title for speed; Shift+Enter
              // is irrelevant on an input so this stays intuitive.
              if (e.key === "Enter") {
                e.preventDefault();
                void submit();
              }
            }}
          />
        </div>

        {/* Priority - segmented */}
        <div className="space-y-1.5">
          <Label>Priority</Label>
          <div className="grid grid-cols-3 gap-2">
            {PRIORITIES.map((p) => (
              <button
                key={p.value}
                type="button"
                data-on={priority === p.value}
                onClick={() => setPriority(p.value)}
                className={cn(
                  "h-9 rounded-md border border-input bg-background text-sm font-medium text-muted-foreground transition-colors hover:bg-accent",
                  "data-[on=true]:border-transparent",
                  p.tone,
                )}
              >
                {p.label}
              </button>
            ))}
          </div>
        </div>

        {/* Due date - quick chips + custom picker */}
        <div className="space-y-1.5">
          <Label htmlFor="todo-due">Due date</Label>
          <ChipGroup wrap size="md" aria-label="Due date presets">
            {quickDue.map((c) => (
              <Chip
                key={c.label}
                raised={due === c.value}
                aria-pressed={due === c.value}
                onClick={() => setDue(due === c.value ? "" : c.value)}
              >
                {c.label}
              </Chip>
            ))}
            {due ? (
              <Chip tone="danger" loud onClick={() => setDue("")}>
                Clear
              </Chip>
            ) : null}
          </ChipGroup>
          <div className="relative">
            <CalendarDays className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              id="todo-due"
              type="date"
              value={due}
              onChange={(e) => setDue(e.target.value)}
              className="pl-9"
            />
          </div>
        </div>

        {/* Notes */}
        <div className="space-y-1.5">
          <Label htmlFor="todo-notes">Notes</Label>
          <Textarea
            id="todo-notes"
            inputMode="text"
            placeholder="Optional context Jarvis should know…"
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            className="min-h-20"
          />
        </div>

        {error ? <p className="text-sm text-danger">{error}</p> : null}
      </form>
    </ResponsiveModal>
  );
}
