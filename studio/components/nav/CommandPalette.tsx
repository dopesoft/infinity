"use client";

import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { CornerDownLeft, Plus, Search } from "lucide-react";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { type SearchHit } from "@/lib/api";
import { kindMeta, useGlobalSearch } from "@/lib/search/useGlobalSearch";
import { useRecordSheet } from "@/components/search/RecordSheet";
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
  // Same hook the dashboard search uses. The debounce, the abort and the
  // stale-response guard used to live here in full; two copies of that is how
  // two boxes start answering the same question differently.
  const { hits, groups, loading, failed } = useGlobalSearch(query, open);
  const recordSheet = useRecordSheet();

  // Reset on close so reopening never flashes the previous search.
  useEffect(() => {
    if (!open) setQuery("");
  }, [open]);

  const go = useCallback(
    (href: string) => {
      onOpenChange(false);
      router.push(href);
    },
    [onOpenChange, router],
  );

  // A hit opens IN PLACE — the same sheet the dashboard opens, so one result
  // behaves one way wherever it was found. It used to route-push `?focus=<id>`
  // at a page that never read the parameter, which meant six of the eight
  // kinds landed you somewhere with no sign of the thing you searched for.
  // A conversation still navigates: /live?session= genuinely goes somewhere,
  // and "open the conversation" is the whole intent of finding one.
  const openHit = useCallback(
    (hit: SearchHit) => {
      if (hit.kind === "session") {
        go(hit.href);
        return;
      }
      onOpenChange(false);
      recordSheet.open(hit);
    },
    [go, onOpenChange, recordSheet],
  );

  const actions = actionsFor(query);

  return (
    <>
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
              <CommandEmpty>
                {failed
                  ? "I could not run that search — Core is not responding."
                  : "Nothing matches that."}
              </CommandEmpty>
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
                      onSelect={() => openHit(hit)}
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
    {/* Sibling of the palette, not a child: opening a hit CLOSES the palette,
        and a sheet mounted inside it would go with it. */}
    {recordSheet.sheet}
    </>
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
