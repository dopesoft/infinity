"use client";

import { useEffect, useState } from "react";
import { IconPlus, IconRefresh, IconTrash, IconUserCircle } from "@tabler/icons-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  deleteProfileFact,
  fetchProfile,
  upsertProfileFact,
  type ProfileFactDTO,
} from "@/lib/api";

// BossProfilePanel — surfaces and edits the always-on identity primer.
// Every fact here is prepended to every system prompt the agent sees.
export function BossProfilePanel() {
  const [facts, setFacts] = useState<ProfileFactDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [adding, setAdding] = useState(false);
  const [busy, setBusy] = useState(false);
  const [draft, setDraft] = useState({ title: "", content: "", importance: 8 });

  async function load() {
    setLoading(true);
    const rows = await fetchProfile();
    setFacts(rows ?? []);
    setLoading(false);
  }

  useEffect(() => {
    load();
  }, []);

  async function save() {
    if (!draft.title.trim() || !draft.content.trim()) return;
    setBusy(true);
    const ok = await upsertProfileFact(draft);
    setBusy(false);
    if (!ok) return;
    setDraft({ title: "", content: "", importance: 8 });
    setAdding(false);
    await load();
  }

  async function remove(id: string) {
    setBusy(true);
    await deleteProfileFact(id);
    setBusy(false);
    await load();
  }

  return (
    <section className="rounded-xl border bg-card/60 backdrop-blur-sm">
      <header className="flex items-center justify-between gap-2 border-b px-3 py-2">
        <div className="flex items-center gap-2">
          <IconUserCircle className="size-4 text-muted-foreground" aria-hidden />
          <span className="text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
            Boss profile
          </span>
          <span className="text-[10px] text-muted-foreground/70">always-on primer</span>
        </div>
        <div className="flex items-center gap-1">
          <Button
            type="button"
            size="icon"
            variant="ghost"
            onClick={() => load()}
            aria-label="Refresh"
            disabled={loading}
            className="h-7 w-7"
          >
            <IconRefresh className="size-3.5" />
          </Button>
          <Button
            type="button"
            size="sm"
            variant={adding ? "secondary" : "ghost"}
            onClick={() => setAdding((v) => !v)}
            className="h-7 px-2 text-xs"
          >
            <IconPlus className="size-3.5" /> {adding ? "Cancel" : "Add"}
          </Button>
        </div>
      </header>

      <div className="space-y-2 p-3">
        {adding && (
          <div className="space-y-2 rounded-lg border bg-background/60 p-2">
            <Input
              placeholder="Title (e.g. 'Tooling preferences')"
              value={draft.title}
              onChange={(e) => setDraft((d) => ({ ...d, title: e.target.value }))}
              className="h-9 text-sm"
              inputMode="text"
            />
            <Textarea
              placeholder="Fact content. One or two dense sentences. Always prepended to every prompt."
              value={draft.content}
              onChange={(e) => setDraft((d) => ({ ...d, content: e.target.value }))}
              rows={3}
              inputMode="text"
              className="text-sm"
            />
            <div className="flex items-center justify-between gap-2">
              <label className="flex items-center gap-1 text-[11px] text-muted-foreground">
                Importance
                <input
                  type="number"
                  min={1}
                  max={10}
                  value={draft.importance}
                  onChange={(e) =>
                    setDraft((d) => ({ ...d, importance: Number(e.target.value) || 1 }))
                  }
                  className="ml-1 w-12 rounded border bg-background px-1 py-0.5 text-right font-mono text-xs"
                />
              </label>
              <Button size="sm" onClick={save} disabled={busy} className="h-8">
                {busy ? "Saving…" : "Save"}
              </Button>
            </div>
          </div>
        )}

        {loading ? (
          <p className="text-xs text-muted-foreground">Loading…</p>
        ) : facts.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            No profile facts yet. Add one — Jarvis sees these on every turn.
          </p>
        ) : (
          <ul className="space-y-1.5">
            {facts.map((f) => (
              <li
                key={f.id}
                className="group flex items-start justify-between gap-2 rounded-md border bg-background/40 px-2.5 py-2"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-1.5">
                    <span className="truncate text-xs font-medium text-foreground">{f.title}</span>
                    <span className="font-mono text-[10px] text-muted-foreground">
                      i{f.importance}
                    </span>
                  </div>
                  <p className="mt-0.5 line-clamp-3 text-[11px] leading-snug text-muted-foreground">
                    {f.content}
                  </p>
                </div>
                <Button
                  type="button"
                  size="icon"
                  variant="ghost"
                  className="size-6 shrink-0 opacity-60 hover:opacity-100"
                  onClick={() => remove(f.id)}
                  aria-label={`Delete ${f.title}`}
                >
                  <IconTrash className="size-3.5" />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}
