"use client";

import { useState } from "react";
import { Loader2, Plus, Sparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageSectionHeader } from "@/components/ui/page-tabs";
import { writeCockpit } from "@/lib/pursuits/pc/api";
import { Field } from "./CoachCard";
import { EmptyNote, PCCard } from "./PCPrimitives";
import type { PCCockpit } from "@/lib/pursuits/pc/types";

/* Success memories - the material the coach returns to at rehearsal time.
 *
 * Which memory gets rehearsed on a given day is decided server-side, so this
 * panel only banks and lists them. That keeps the cockpit and a coaching chat
 * pointing at the same memory rather than each picking its own favourite.
 */
export function MemoriesPanel({
  cockpit,
  onUpdated,
}: {
  cockpit: PCCockpit;
  onUpdated: (next: PCCockpit) => void;
}) {
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const todaysId = cockpit.rehearsal_memory?.id;

  async function save() {
    const t = title.trim();
    if (!t) return;
    setSaving(true);
    setError(null);
    try {
      const next = await writeCockpit(cockpit.pursuit.id, "memory", {
        title: t,
        body: body.trim(),
      });
      setTitle("");
      setBody("");
      onUpdated(next);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save that.");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="space-y-4">
      <PCCard>
        <PageSectionHeader title="bank a memory" />
        <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
          Something that genuinely worked. Specific and sensory beats abstract: what you actually
          saw and heard when it happened is what the rehearsal has to work with.
        </p>
        <form
          className="mt-3 space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <Input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="What happened, in a few words"
            inputMode="text"
            aria-label="Memory title"
            className="min-w-0"
          />
          <Field
            label="What it was actually like"
            value={body}
            onChange={setBody}
            placeholder="Optional. What you saw, heard, and felt at the time."
            rows={3}
          />
          {error ? (
            <p className="text-sm text-danger" role="alert">
              {error}
            </p>
          ) : null}
          <Button type="submit" disabled={!title.trim() || saving} className="w-full sm:w-auto">
            {saving ? (
              <Loader2 className="size-4 animate-spin" aria-hidden />
            ) : (
              <Plus className="size-4" aria-hidden />
            )}
            Bank it
          </Button>
        </form>
      </PCCard>

      <PCCard>
        <PageSectionHeader title="banked" count={cockpit.memories.length} />
        {cockpit.memories.length === 0 ? (
          <EmptyNote className="mt-3">
            Nothing banked yet. The first one usually turns up the moment a proof action lands
            better than expected.
          </EmptyNote>
        ) : (
          <ul className="mt-3 space-y-3">
            {cockpit.memories.map((m) => (
              <li key={m.id} className="min-w-0 border-b border-dashed pb-3 last:border-0 last:pb-0">
                <p className="flex min-w-0 items-center gap-1.5 break-words text-sm font-medium text-foreground">
                  {m.id === todaysId ? (
                    <Sparkles className="size-3.5 shrink-0 text-foreground" aria-hidden />
                  ) : null}
                  {m.title}
                </p>
                {m.body.trim() ? (
                  <p className="mt-1 whitespace-pre-wrap break-words text-sm text-muted-foreground">
                    {m.body}
                  </p>
                ) : null}
                {m.id === todaysId ? (
                  <p className="mt-1 font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
                    today&apos;s rehearsal
                  </p>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </PCCard>
    </div>
  );
}
