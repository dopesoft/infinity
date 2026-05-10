"use client";

import { useEffect, useState } from "react";
import {
  IconChevronDown,
  IconPlus,
  IconRefresh,
  IconTrash,
  IconUserCircle,
} from "@tabler/icons-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import {
  deleteProfileFact,
  fetchProfile,
  upsertProfileFact,
  type ProfileFactDTO,
} from "@/lib/api";

// BossProfilePanel — surfaces and edits the always-on identity primer.
// Every fact here is prepended to every system prompt the agent sees.
//
// Mobile: collapsed by default to avoid eating screen above the memory list.
// Tap the header to expand. Desktop (lg+): always expanded.
export function BossProfilePanel() {
  const [facts, setFacts] = useState<ProfileFactDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [adding, setAdding] = useState(false);
  const [busy, setBusy] = useState(false);
  const [draft, setDraft] = useState({ title: "", content: "", importance: 8 });
  const [collapsed, setCollapsed] = useState<boolean | null>(null);

  useEffect(() => {
    setCollapsed(window.matchMedia("(min-width: 1024px)").matches ? false : true);
  }, []);

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
      <header className="flex items-center justify-between gap-2 border-b">
        <button
          type="button"
          onClick={() => setCollapsed((c) => !c)}
          className="flex min-h-12 flex-1 items-center gap-2 px-3 py-2 text-left lg:min-h-0 lg:py-2 lg:cursor-default"
          aria-expanded={!collapsed}
        >
          <IconUserCircle className="size-4 text-muted-foreground" aria-hidden />
          <span className="text-[11px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
            Boss profile
          </span>
          <span className="hidden text-[10px] text-muted-foreground/70 sm:inline">
            always-on primer
          </span>
          {!loading && facts.length > 0 && (
            <span className="font-mono text-[10px] text-muted-foreground/70">
              {facts.length}
            </span>
          )}
          <IconChevronDown
            className={cn(
              "ml-auto size-4 text-muted-foreground transition-transform lg:hidden",
              !collapsed && "rotate-180",
            )}
            aria-hidden
          />
        </button>
        <div className="flex items-center gap-1 px-2">
          <Button
            type="button"
            size="icon"
            variant="ghost"
            onClick={() => load()}
            aria-label="Refresh"
            disabled={loading}
            className="size-11 lg:size-8"
          >
            <IconRefresh className="size-4" />
          </Button>
          <Button
            type="button"
            size="sm"
            variant={adding ? "secondary" : "ghost"}
            onClick={() => {
              setAdding((v) => !v);
              setCollapsed(false);
            }}
            className="h-11 px-3 lg:h-8 lg:px-2"
          >
            <IconPlus className="size-4" />
            <span className="ml-1">{adding ? "Cancel" : "Add"}</span>
          </Button>
        </div>
      </header>

      <div className={cn("space-y-2 p-3", collapsed && "hidden lg:block")}>
        {adding && (
          <div className="space-y-2 rounded-lg border bg-background/60 p-2">
            <Input
              placeholder="Title (e.g. 'Tooling preferences')"
              value={draft.title}
              onChange={(e) => setDraft((d) => ({ ...d, title: e.target.value }))}
              inputMode="text"
            />
            <Textarea
              placeholder="Fact content. One or two dense sentences. Always prepended to every prompt."
              value={draft.content}
              onChange={(e) => setDraft((d) => ({ ...d, content: e.target.value }))}
              rows={3}
              inputMode="text"
            />
            <div className="flex items-center justify-between gap-2">
              <label className="flex items-center gap-2 text-xs text-muted-foreground">
                Importance
                <Input
                  type="number"
                  min={1}
                  max={10}
                  value={draft.importance}
                  onChange={(e) =>
                    setDraft((d) => ({ ...d, importance: Number(e.target.value) || 1 }))
                  }
                  inputMode="numeric"
                  className="w-16 px-2 text-right font-mono"
                />
              </label>
              <Button onClick={save} disabled={busy}>
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
                className="flex items-start justify-between gap-1 rounded-md border bg-background/40 py-1.5 pl-2.5 pr-1"
              >
                <div className="min-w-0 flex-1 py-1">
                  <div className="flex items-center gap-1.5">
                    <span className="truncate text-sm font-medium text-foreground lg:text-xs">
                      {f.title}
                    </span>
                    <span className="font-mono text-[10px] text-muted-foreground">
                      i{f.importance}
                    </span>
                  </div>
                  <p className="mt-0.5 line-clamp-3 text-xs leading-snug text-muted-foreground lg:text-[11px]">
                    {f.content}
                  </p>
                </div>
                <Button
                  type="button"
                  size="icon"
                  variant="ghost"
                  className="size-11 shrink-0 lg:size-8"
                  onClick={() => remove(f.id)}
                  aria-label={`Delete ${f.title}`}
                >
                  <IconTrash className="size-4" />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}
