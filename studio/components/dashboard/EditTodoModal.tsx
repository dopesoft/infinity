"use client";

import { useEffect, useState } from "react";
import { CalendarDays, Loader2, Pencil } from "lucide-react";
import { ResponsiveModal, ResponsiveModalHeader } from "@/components/ui/responsive-modal";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";
import { updateTodo } from "@/lib/api";
import { useIsDesktop } from "@/lib/use-media-query";
import type { Todo } from "@/lib/dashboard/types";

const PRIORITIES: { value: Todo["priority"]; label: string; tone: string }[] = [
  { value: "low", label: "Low", tone: "data-[on=true]:bg-muted data-[on=true]:text-foreground" },
  { value: "med", label: "Medium", tone: "data-[on=true]:bg-rose-400/15 data-[on=true]:text-rose-400" },
  { value: "high", label: "High", tone: "data-[on=true]:bg-danger/15 data-[on=true]:text-danger" },
];

function todoDateValue(iso?: string): string {
  const trimmed = iso?.trim() ?? "";
  if (!trimmed) return "";
  const bare = trimmed.match(/^(\d{4}-\d{2}-\d{2})/);
  if (bare) return bare[1];
  const d = new Date(trimmed);
  if (Number.isNaN(d.getTime())) return "";
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

export function EditTodoModal({
  todo,
  open,
  onOpenChange,
  onSaved,
}: {
  todo: Todo | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSaved?: () => void;
}) {
  const [title, setTitle] = useState("");
  const [notes, setNotes] = useState("");
  const [priority, setPriority] = useState<Todo["priority"]>("med");
  const [due, setDue] = useState("");
  const [status, setStatus] = useState<"open" | "done" | "dropped">("open");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const isDesktop = useIsDesktop();

  useEffect(() => {
    if (!open || !todo) return;
    setTitle(todo.title);
    setNotes(todo.body ?? "");
    setPriority(todo.priority ?? "med");
    setDue(todoDateValue(todo.dueAt));
    setStatus(todo.done ? "done" : "open");
    setSaving(false);
    setError(null);
  }, [open, todo]);

  const canSubmit = !!todo && title.trim().length > 0 && !saving;

  async function submit() {
    if (!todo || !canSubmit) return;
    setSaving(true);
    setError(null);
    const ok = await updateTodo({
      id: todo.id,
      title: title.trim(),
      body: notes.trim(),
      priority: priority ?? "med",
      due_at: due,
      status,
    });
    if (!ok) {
      setSaving(false);
      setError("Couldn't save those changes. Check the date and try again.");
      return;
    }
    onSaved?.();
    onOpenChange(false);
  }

  return (
    <ResponsiveModal
      open={open}
      onOpenChange={onOpenChange}
      title="Edit todo"
      description="Update the dashboard todo Jarvis can also see."
      size="md"
      header={
        <ResponsiveModalHeader
          icon={<Pencil className="size-4" />}
          title="Edit todo"
          subtitle="Changes save to the dashboard task row"
        />
      }
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={saving}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!canSubmit}>
            {saving ? <Loader2 className="size-4 animate-spin" /> : null}
            {saving ? "Saving..." : "Save changes"}
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
        <div className="space-y-1.5">
          <Label htmlFor="edit-todo-title">Title</Label>
          <Input
            id="edit-todo-title"
            autoFocus={isDesktop}
            inputMode="text"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
          />
        </div>

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

        <div className="space-y-1.5">
          <Label>Status</Label>
          <div className="grid grid-cols-3 gap-2">
            {(["open", "done", "dropped"] as const).map((value) => (
              <button
                key={value}
                type="button"
                data-on={status === value}
                onClick={() => setStatus(value)}
                className={cn(
                  "h-9 rounded-md border border-input bg-background text-sm font-medium capitalize text-muted-foreground transition-colors hover:bg-accent",
                  "data-[on=true]:border-transparent data-[on=true]:bg-info/15 data-[on=true]:text-info",
                )}
              >
                {value}
              </button>
            ))}
          </div>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="edit-todo-due">Due date</Label>
          <div className="relative">
            <CalendarDays className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              id="edit-todo-due"
              type="date"
              value={due}
              onChange={(e) => setDue(e.target.value)}
              className="pl-9"
            />
          </div>
          {due ? (
            <button
              type="button"
              onClick={() => setDue("")}
              className="text-xs text-muted-foreground underline-offset-2 hover:underline"
            >
              Clear due date
            </button>
          ) : null}
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="edit-todo-notes">Notes</Label>
          <Textarea
            id="edit-todo-notes"
            inputMode="text"
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
