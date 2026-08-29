"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import {
  Activity,
  Brain,
  Clock,
  CornerDownLeft,
  MessageSquare,
  Plus,
  Search,
  Sparkles,
  type LucideIcon,
} from "lucide-react";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { searchAll, type SearchHit } from "@/lib/api";
import { cn } from "@/lib/utils";

/**
 * CommandPalette - ⌘K over everything.
 *
 * This is the search the app never had. Twelve thousand memories, forty
 * skills, nine automations and hundreds of sessions, and the only search box
 * used to filter the dashboard you were already looking at.
 *
 * `components/ui/command.tsx` has been in the repo, fully built, with ZERO
 * callers. Built-but-unwired is the same as never coded, so this is the
 * caller.
 *
 * Shape (matching the mockup exactly): results grouped by what they are,
 * with the LAST group always being "Do something" - so the same two
 * keystrokes that find a thing can also start a job.
 */

const KIND_META: Record<string, { label: string; Icon: LucideIcon; order: number }> = {
  surfaced: { label: "Needs you", Icon: Sparkles, order: 0 },
  memory: { label: "Memory", Icon: Brain, order: 1 },
  skill: { label: "Skills", Icon: Sparkles, order: 2 },
  automation: { label: "Automations", Icon: Clock, order: 3 },
  session: { label: "Conversations", Icon: MessageSquare, order: 4 },
};

function kindMeta(kind: string) {
  return (
    KIND_META[kind] ?? {
      // An unknown kind is a table Core learned to search after this shipped.
      // It renders under its own name rather than being dropped.
      label: kind.charAt(0).toUpperCase() + kind.slice(1),
      Icon: Activity,
      order: 50,
    }
  );
}

/** What "Do something" offers for the text currently typed. */
function actionsFor(query: string): { label: string; href: string }[] {
  const q = query.trim();
  if (!q) return [];
  return [
    { label: `Ask Jarvis about “${q}”`, href: `/live?ask=${encodeURIComponent(q)}` },
    { label: `Start a conversation about “${q}”`, href: `/live?new=1&ask=${encodeURIComponent(q)}` },
  ];
}

export function CommandPalette({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const router = useRouter();
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [loading, setLoading] = useState(false);
  const seq = useRef(0);

  // Debounced fetch. The palette is keystroke-driven, so an in-flight
  // response for an older query must never overwrite a newer one - hence the
  // sequence guard as well as the abort.
  useEffect(() => {
    if (!open) return;
    const q = query.trim();
    if (!q) {
      setHits([]);
      setLoading(false);
      return;
    }
    const mine = ++seq.current;
    const ac = new AbortController();
    setLoading(true);
    const t = window.setTimeout(async () => {
      const res = await searchAll(q, ac.signal);
      if (mine !== seq.current) return;
      setHits(res?.hits ?? []);
      setLoading(false);
    }, 140);
    return () => {
      window.clearTimeout(t);
      ac.abort();
    };
  }, [query, open]);

  // Reset on close so reopening never flashes the previous search.
  useEffect(() => {
    if (!open) {
      setQuery("");
      setHits([]);
    }
  }, [open]);

  const groups = useMemo(() => {
    const by = new Map<string, SearchHit[]>();
    for (const hit of hits) {
      const list = by.get(hit.kind);
      if (list) list.push(hit);
      else by.set(hit.kind, [hit]);
    }
    return [...by.entries()].sort(
      (a, b) => kindMeta(a[0]).order - kindMeta(b[0]).order,
    );
  }, [hits]);

  const go = useCallback(
    (href: string) => {
      onOpenChange(false);
      router.push(href);
    },
    [onOpenChange, router],
  );

  const actions = actionsFor(query);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="top-[12%] max-w-xl translate-y-0 gap-0 overflow-hidden p-0"
        // The palette owns its own list scrolling; the dialog must not add a
        // second scroller around it.
      >
        <DialogTitle className="sr-only">Search everything</DialogTitle>
        <Command shouldFilter={false} className="bg-transparent">
          <CommandInput
            value={query}
            onValueChange={setQuery}
            placeholder="Search everything"
          />
          <CommandList className="max-h-[min(60dvh,420px)]">
            {query.trim() && !loading && hits.length === 0 && (
              <CommandEmpty>Nothing matches that.</CommandEmpty>
            )}

            {groups.map(([kind, list]) => {
              const meta = kindMeta(kind);
              const Icon = meta.Icon;
              return (
                <CommandGroup key={kind} heading={meta.label}>
                  {list.map((hit) => (
                    <CommandItem
                      key={`${kind}:${hit.id}`}
                      value={`${kind}:${hit.id}`}
                      onSelect={() => go(hit.href)}
                      className="gap-2.5"
                    >
                      <Icon className="size-4 shrink-0 text-quiet" aria-hidden />
                      <span className="min-w-0 flex-1 truncate">{hit.title}</span>
                      {hit.meta ? (
                        <span className="shrink-0 truncate text-[11px] text-quiet">
                          {hit.meta}
                        </span>
                      ) : null}
                    </CommandItem>
                  ))}
                </CommandGroup>
              );
            })}

            {actions.length > 0 && (
              <CommandGroup heading="Do something">
                {actions.map((action) => (
                  <CommandItem
                    key={action.href}
                    value={action.href}
                    onSelect={() => go(action.href)}
                    className="gap-2.5"
                  >
                    <Plus className="size-4 shrink-0 text-quiet" aria-hidden />
                    <span className="min-w-0 flex-1 truncate">{action.label}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}

            {!query.trim() && (
              <div className="flex items-center gap-2 px-4 py-6 text-[13px] text-quiet">
                <Search className="size-4" aria-hidden />
                Memories, skills, automations, conversations, and anything he
                surfaced.
              </div>
            )}
          </CommandList>
          <div className="flex items-center gap-4 border-t border-hairline px-4 py-2 font-mono text-[10px] uppercase tracking-[0.1em] text-quiet">
            <span className="flex items-center gap-1">
              <CornerDownLeft className="size-3" aria-hidden /> open
            </span>
            <span>esc close</span>
            <span className={cn("ml-auto", !loading && "invisible")}>searching…</span>
          </div>
        </Command>
      </DialogContent>
    </Dialog>
  );
}

/**
 * useCommandPalette - the ⌘K / Ctrl-K binding, owned in one place so no
 * surface has to register its own listener.
 *
 * `/` is deliberately NOT bound globally: it belongs to whichever page-level
 * search field is on screen, and stealing it would break typing in the
 * composer.
 */
export function useCommandPalette() {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        setOpen((v) => !v);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  return { open, setOpen };
}
