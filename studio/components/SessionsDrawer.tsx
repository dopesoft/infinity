"use client";

import { useEffect, useMemo, useState } from "react";
import { Plus, Search, FolderGit2, MessageCircle } from "lucide-react";
import {
  Drawer,
  DrawerContent,
  DrawerHeader,
  DrawerTitle,
  DrawerTrigger,
} from "@/components/ui/drawer";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { fetchSessions, type SessionDTO } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";

/**
 * SessionsDrawer — bottom-sheet/desktop-modal session switcher.
 *
 * Mounted by SessionHeader; opens when the boss taps the session name
 * (or the chevron next to it). Lists every session grouped by recency,
 * shows a small project chip for sessions that have scaffolded an app,
 * supports search, and pinned at top is a "New session" action.
 *
 * Clicking a session calls onSelect(id); the parent (Live) handles
 * routing/state changes. Switching does NOT spawn a fresh session — it
 * loads the chosen one's transcript via the existing useChat hydration.
 */
type Group = "Today" | "Yesterday" | "This week" | "Older";

function bucketize(s: SessionDTO, now: number): Group {
  const t = Date.parse(s.started_at) || 0;
  const d = now - t;
  const ONE_DAY = 86_400_000;
  if (d < ONE_DAY) return "Today";
  if (d < 2 * ONE_DAY) return "Yesterday";
  if (d < 7 * ONE_DAY) return "This week";
  return "Older";
}

function templateLabel(t?: string): string {
  if (!t) return "";
  if (t.startsWith("capacitor-")) return `capacitor`;
  return t;
}

export function SessionsDrawer({
  currentId,
  onSelect,
  onNew,
  trigger,
}: {
  currentId: string;
  onSelect: (id: string) => void;
  onNew: () => void;
  trigger: React.ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const [sessions, setSessions] = useState<SessionDTO[]>([]);
  const [q, setQ] = useState("");
  const [now, setNow] = useState<number>(0);

  useEffect(() => {
    setNow(Date.now());
  }, []);

  // Refresh whenever the drawer opens; also stay live via the realtime hook
  // so a name landing in via the Haiku auto-namer shows up without a re-open.
  async function refresh() {
    const list = await fetchSessions();
    setSessions(list ?? []);
  }

  useEffect(() => {
    if (open) refresh();
  }, [open]);

  useRealtime("mem_sessions", refresh);

  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    if (!needle) return sessions;
    return sessions.filter((s) => {
      const name = (s.name ?? "").toLowerCase();
      const tmpl = (s.project_template ?? "").toLowerCase();
      const id = s.id.toLowerCase();
      return name.includes(needle) || tmpl.includes(needle) || id.includes(needle);
    });
  }, [sessions, q]);

  const grouped = useMemo(() => {
    if (now === 0) return [] as Array<{ group: Group; rows: SessionDTO[] }>;
    const order: Group[] = ["Today", "Yesterday", "This week", "Older"];
    const map = new Map<Group, SessionDTO[]>();
    for (const s of filtered) {
      const g = bucketize(s, now);
      const arr = map.get(g) ?? [];
      arr.push(s);
      map.set(g, arr);
    }
    return order
      .map((g) => ({ group: g, rows: map.get(g) ?? [] }))
      .filter((b) => b.rows.length > 0);
  }, [filtered, now]);

  function handleSelect(id: string) {
    onSelect(id);
    setOpen(false);
  }

  function handleNew() {
    onNew();
    setOpen(false);
  }

  return (
    <Drawer open={open} onOpenChange={setOpen}>
      <DrawerTrigger asChild>{trigger}</DrawerTrigger>
      <DrawerContent className="lg:mx-auto lg:max-w-2xl">
        <DrawerHeader className="text-left">
          <DrawerTitle>Sessions</DrawerTitle>
        </DrawerHeader>
        <div className="px-4 pb-2">
          <div className="flex items-center gap-2">
            <div className="relative flex-1">
              <Search className="pointer-events-none absolute left-2 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={q}
                onChange={(e) => setQ(e.target.value)}
                placeholder="Search by name, framework, or id…"
                inputMode="search"
                className="pl-8"
              />
            </div>
            <Button onClick={handleNew} className="shrink-0" aria-label="Start a new session">
              <Plus className="size-4" />
              <span className="hidden sm:inline">New</span>
            </Button>
          </div>
        </div>
        <div className="max-h-[70dvh] overflow-y-auto px-2 pb-4 scroll-touch">
          {grouped.length === 0 ? (
            <p className="px-3 py-6 text-center text-sm text-muted-foreground">
              No sessions match. Start a fresh one above.
            </p>
          ) : (
            grouped.map(({ group, rows }) => (
              <div key={group} className="px-1 py-1">
                <div className="px-2 pb-1 pt-3 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                  {group}
                </div>
                <ul className="space-y-1">
                  {rows.map((s) => (
                    <li key={s.id}>
                      <button
                        type="button"
                        onClick={() => handleSelect(s.id)}
                        className={cn(
                          "flex w-full min-h-12 items-center gap-2 rounded-lg px-3 py-2 text-left transition-colors hover:bg-accent",
                          s.id === currentId && "bg-accent/60 ring-1 ring-info",
                        )}
                      >
                        {s.project_path ? (
                          <FolderGit2 className="size-4 shrink-0 text-info" aria-hidden />
                        ) : (
                          <MessageCircle className="size-4 shrink-0 text-muted-foreground" aria-hidden />
                        )}
                        <div className="min-w-0 flex-1">
                          <div className="truncate text-sm font-medium">
                            {s.name?.trim() || (
                              <span className="font-mono text-xs text-muted-foreground">
                                {s.id.slice(0, 8)}…
                              </span>
                            )}
                          </div>
                          <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
                            <span>{s.message_count} msg</span>
                            {s.live && (
                              <span className="inline-flex items-center gap-1">
                                <span className="size-1.5 rounded-full bg-success" /> live
                              </span>
                            )}
                          </div>
                        </div>
                        {s.project_template && (
                          <Badge variant="outline" className="shrink-0 text-[10px]">
                            {templateLabel(s.project_template)}
                          </Badge>
                        )}
                      </button>
                    </li>
                  ))}
                </ul>
              </div>
            ))
          )}
        </div>
      </DrawerContent>
    </Drawer>
  );
}
