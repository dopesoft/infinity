"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import {
  Plus,
  FolderGit2,
  MessageCircle,
  Trash2,
  Check,
} from "lucide-react";
import {
  Drawer,
  DrawerContent,
  DrawerHeader,
  DrawerTitle,
  DrawerTrigger,
} from "@/components/ui/drawer";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { SearchInput } from "@/components/ui/search-input";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { deleteSession, fetchSessions, type SessionDTO } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";
import { useIsDesktop } from "@/lib/use-media-query";

/**
 * SessionsDrawer — session switcher. Honors the global pattern:
 * <Dialog> on lg+ (centered modal), <Drawer> on <lg (bottom sheet).
 *
 * Tap a row → onSelect(id). Pressing "+ New" → onNew(). Search filters by
 * name / template slug / id.
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

// formatRowDate renders "Today 2:15 PM" / "Yesterday 9:04 PM" / "Mar 7, 4:12 PM"
// — short enough to fit on one line next to the title, scannable at a
// glance. Locale-aware: respects the boss's system clock format. SSR
// guard: when `now` is 0 (pre-mount) we return the raw timestamp without
// the day prefix.
function formatRowDate(iso: string, now: number): string {
  const t = Date.parse(iso);
  if (!t) return "";
  const d = new Date(t);
  const time = d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  if (!now) return time;
  const sameDay = (a: Date, b: Date) =>
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate();
  const today = new Date(now);
  const yesterday = new Date(now - 86_400_000);
  if (sameDay(d, today)) return `Today ${time}`;
  if (sameDay(d, yesterday)) return `Yesterday ${time}`;
  const date = d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  return `${date}, ${time}`;
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
  const isDesktop = useIsDesktop();
  // Per-row delete affordance state. confirmingId is the row currently
  // in the "Cancel / Delete" inline-confirm state (single-row delete).
  // deletingId tracks the in-flight DELETE so the row's button can
  // disable + the row doesn't blink-disappear before the response.
  const [confirmingId, setConfirmingId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  // Multi-select state. There's no explicit "selection mode" toggle — each
  // row carries its own subtle circle selector that's always present, so
  // ticking one is the entry into bulk delete. The header trash button
  // appears only when selectedIds is non-empty (no competing X buttons,
  // no two-step "enter selection mode" gesture).
  const [selectedIds, setSelectedIds] = useState<Set<string>>(() => new Set());
  const [confirmingBulk, setConfirmingBulk] = useState(false);
  const [bulkDeleting, setBulkDeleting] = useState(false);

  // Long-press detection refs. Touch + mouse both flow through pointer
  // events here. Threshold matches iOS's own context-menu cadence (~450ms)
  // so the gesture feels native rather than laggy.
  const pressTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pressStartRef = useRef<{ x: number; y: number } | null>(null);
  const pressFiredRef = useRef(false);

  function clearPressTimer() {
    if (pressTimerRef.current) {
      clearTimeout(pressTimerRef.current);
      pressTimerRef.current = null;
    }
  }

  // rowPressProps wraps a row's main click button with the long-press
  // gesture: a sustained touch opens the row's single-row delete
  // confirm (same UI the desktop hover-trash triggers). The matching
  // click is swallowed so the long-press doesn't also fire the row's
  // onClick → switch-session. Multi-select stays on the always-visible
  // per-row circle on mobile so a long-press is never confused with
  // "tap to open." Movement beyond ~8px cancels — keeps a real scroll
  // from being mis-read as a press.
  function rowPressProps(id: string, onTap: () => void) {
    return {
      onPointerDown: (e: React.PointerEvent) => {
        if (e.pointerType === "mouse" && e.button !== 0) return;
        pressStartRef.current = { x: e.clientX, y: e.clientY };
        pressFiredRef.current = false;
        clearPressTimer();
        pressTimerRef.current = setTimeout(() => {
          pressFiredRef.current = true;
          setConfirmingId(id);
          if (typeof navigator !== "undefined" && "vibrate" in navigator) {
            try {
              navigator.vibrate(10);
            } catch {
              /* haptics unavailable — ignore */
            }
          }
        }, 450);
      },
      onPointerMove: (e: React.PointerEvent) => {
        const start = pressStartRef.current;
        if (!start) return;
        const dx = e.clientX - start.x;
        const dy = e.clientY - start.y;
        if (dx * dx + dy * dy > 64) {
          clearPressTimer();
        }
      },
      onPointerUp: () => {
        clearPressTimer();
        pressStartRef.current = null;
      },
      onPointerCancel: () => {
        clearPressTimer();
        pressStartRef.current = null;
        pressFiredRef.current = false;
      },
      onClick: (e: React.MouseEvent) => {
        // Swallow the click that follows a long-press release so the
        // reveal doesn't immediately switch sessions on the user.
        if (pressFiredRef.current) {
          e.preventDefault();
          pressFiredRef.current = false;
          return;
        }
        onTap();
      },
    };
  }

  useEffect(() => {
    setNow(Date.now());
  }, []);

  async function refresh() {
    const list = await fetchSessions();
    setSessions(list ?? []);
  }

  useEffect(() => {
    if (open) refresh();
  }, [open]);

  useRealtime("mem_sessions", refresh);

  // Reset transient state when the drawer closes — otherwise reopening
  // could land mid-confirm or with stale selections. Also clean up any
  // straggling long-press timer on unmount.
  useEffect(() => {
    if (!open) {
      setConfirmingId(null);
      setSelectedIds(new Set());
      setConfirmingBulk(false);
    }
  }, [open]);
  useEffect(() => () => clearPressTimer(), []);

  function toggleSelected(id: string) {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  async function doBulkDelete() {
    if (bulkDeleting || selectedIds.size === 0) return;
    setBulkDeleting(true);
    const ids = Array.from(selectedIds);
    const results = await Promise.all(ids.map((id) => deleteSession(id)));
    const deleted = new Set(ids.filter((_, i) => results[i]));
    if (deleted.size > 0) {
      // Optimistic remove + handle the current-session case the same way
      // single-delete does: if the current session was in the batch,
      // swap to the latest remaining (or onNew if we wiped the lot).
      setSessions((prev) => prev.filter((s) => !deleted.has(s.id)));
      if (deleted.has(currentId)) {
        const remaining = sessions.filter((x) => !deleted.has(x.id));
        if (remaining.length > 0) {
          onSelect(remaining[0].id);
        } else {
          onNew();
        }
      }
    }
    setBulkDeleting(false);
    setSelectedIds(new Set());
    setConfirmingBulk(false);
  }

  async function doDelete(id: string) {
    if (!id || deletingId) return;
    setDeletingId(id);
    const ok = await deleteSession(id);
    if (ok) {
      // Optimistically drop the row so the list reflects the delete
      // even before the next realtime tick. The realtime subscription
      // would also drive this, but waiting on it makes the click feel
      // unresponsive.
      setSessions((prev) => prev.filter((s) => s.id !== id));
      setConfirmingId(null);
      // Deleting the session the boss is currently in: switch to the
      // most-recently-started remaining session — the API hands us rows
      // newest-first, so `sessions` minus the deleted row gives that for
      // free. If we just deleted the only one, fall back to a fresh
      // session. We do NOT close the drawer — the boss is probably about
      // to delete more, and the modal/drawer is the workbench for that.
      if (id === currentId) {
        const remaining = sessions.filter((x) => x.id !== id);
        if (remaining.length > 0) {
          onSelect(remaining[0].id);
        } else {
          onNew();
        }
      }
    }
    setDeletingId(null);
  }

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

  // Header trash control. Hidden when nothing is selected — keeps the
  // header clean and avoids competing with the modal's own close X.
  // Shows up the moment any row is ticked, in destructive style with
  // the count, and opens the bulk-confirm strip on click.
  const headerControls =
    selectedIds.size > 0 ? (
      <Button
        type="button"
        variant="destructive"
        size="sm"
        onClick={() => setConfirmingBulk(true)}
        disabled={bulkDeleting}
        aria-label={`Delete ${selectedIds.size} selected session${selectedIds.size === 1 ? "" : "s"}`}
        title="Delete selected"
        className="h-9 shrink-0 gap-1.5 px-2.5"
      >
        <Trash2 className="size-4" />
        <span className="font-mono text-xs tabular-nums">{selectedIds.size}</span>
        <span className="hidden sm:inline">Delete</span>
      </Button>
    ) : null;

  const body = (
    <>
      <div className="px-4 pb-2 pt-1">
        <div className="flex items-center gap-2">
          <div className="flex-1">
            <SearchInput
              value={q}
              onValueChange={setQ}
              placeholder="Search by name, framework, or id…"
            />
          </div>
          <Button onClick={handleNew} className="shrink-0" aria-label="Start a new session">
            <Plus className="size-4" />
            <span className="hidden sm:inline">New</span>
          </Button>
        </div>
      </div>
      {/* Bulk-delete confirm strip — sits between the search bar and the
          list so the count + actions are within thumb reach on mobile and
          can't be lost under a long list. Title-cased phrasing keeps it
          honest: this destroys N rows. */}
      {confirmingBulk && (
        <div className="mx-4 mb-2 flex items-center gap-2 rounded-md border border-danger/40 bg-danger/5 px-3 py-2">
          <span className="min-w-0 flex-1 truncate text-sm">
            Delete{" "}
            <span className="font-medium">
              {selectedIds.size} session{selectedIds.size === 1 ? "" : "s"}
            </span>
            ?
          </span>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setConfirmingBulk(false)}
            disabled={bulkDeleting}
            className="h-9 px-3 text-xs"
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            size="sm"
            onClick={doBulkDelete}
            disabled={bulkDeleting || selectedIds.size === 0}
            className="h-9 px-3 text-xs"
          >
            {bulkDeleting ? "Deleting…" : "Delete"}
          </Button>
        </div>
      )}
      <div className="max-h-[70dvh] overflow-y-auto px-2 pb-4 scroll-touch lg:max-h-[60vh]">
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
                {rows.map((s) => {
                  const isConfirming = confirmingId === s.id;
                  const isDeleting = deletingId === s.id;
                  const isSelected = selectedIds.has(s.id);
                  const displayName = s.name?.trim() || `${s.id.slice(0, 8)}…`;
                  const rowMeta = (
                    <>
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
                          <span suppressHydrationWarning>{formatRowDate(s.started_at, now)}</span>
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
                    </>
                  );
                  // Show the persistent circle selector whenever it'd
                  // be useful: any time the batch is non-empty, or a
                  // hovered row (so first-time discovery is "hover and
                  // see the circle"), or this row is itself selected
                  // (so it never disappears while ticked).
                  const showSelector = selectedIds.size > 0 || isSelected;
                  return (
                    <li
                      key={s.id}
                      className={cn(
                        // `group` so the per-row trash + circle selector
                        // can fade in on hover (desktop). The
                        // current-session ring stays in every state so
                        // the boss never loses context.
                        "group rounded-lg",
                        s.id === currentId && "bg-accent/60 ring-1 ring-info",
                        isSelected && "bg-danger/5",
                      )}
                    >
                      {isConfirming ? (
                        // Inline single-row confirm — keeps the gesture
                        // tight and reads the same on phone or desktop.
                        <div className="flex min-h-12 items-center gap-2 px-3 py-2">
                          <span className="min-w-0 flex-1 truncate text-sm">
                            Delete{" "}
                            <span className="font-medium">{displayName}</span>?
                          </span>
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            onClick={() => setConfirmingId(null)}
                            disabled={isDeleting}
                            className="h-9 px-3 text-xs"
                          >
                            Cancel
                          </Button>
                          <Button
                            type="button"
                            variant="destructive"
                            size="sm"
                            onClick={() => doDelete(s.id)}
                            disabled={isDeleting}
                            className="h-9 px-3 text-xs"
                            aria-label={`Confirm delete ${displayName}`}
                          >
                            {isDeleting ? "Deleting…" : "Delete"}
                          </Button>
                        </div>
                      ) : (
                        <div className="flex items-center gap-1 pr-1">
                          {/* Circle selector — always rendered so its
                              click target is stable, but only visually
                              present (opacity-100) once a batch is
                              forming, on hover (desktop discovery), or
                              when this row is itself selected. Tapping
                              it toggles selection without navigating. */}
                          <button
                            type="button"
                            onClick={(e) => {
                              e.stopPropagation();
                              toggleSelected(s.id);
                            }}
                            aria-pressed={isSelected}
                            aria-label={`${isSelected ? "Unselect" : "Select"} ${displayName}`}
                            title={isSelected ? "Unselect" : "Select"}
                            className={cn(
                              "inline-flex size-11 shrink-0 items-center justify-center rounded-md transition-opacity",
                              // Always-on on touch devices (no hover to
                              // discover with). On hover-capable
                              // pointers, fade in on hover OR when
                              // there's already a batch / this row is
                              // ticked.
                              "[@media(hover:none)]:opacity-100",
                              showSelector
                                ? "opacity-100"
                                : "opacity-0 group-hover:opacity-100 focus-visible:opacity-100",
                            )}
                          >
                            <span
                              className={cn(
                                "inline-flex size-5 items-center justify-center rounded-full border-2 transition-colors",
                                isSelected
                                  ? "border-danger bg-danger text-background"
                                  : "border-muted-foreground/40 bg-background text-transparent",
                              )}
                              aria-hidden
                            >
                              <Check className="size-3" />
                            </span>
                          </button>
                          <button
                            type="button"
                            {...rowPressProps(s.id, () => handleSelect(s.id))}
                            className={cn(
                              "flex min-w-0 min-h-12 flex-1 items-center gap-2 rounded-lg px-3 py-2 text-left transition-colors hover:bg-accent select-none",
                            )}
                          >
                            {rowMeta}
                          </button>
                          {/* Per-row trash — desktop quick-delete via
                              hover; keyboard users see it via
                              focus-visible. On touch the path is
                              long-press → adds to selection + header
                              trash handles the action, so this
                              affordance stays cleanly desktop-only. */}
                          <button
                            type="button"
                            onClick={(e) => {
                              e.stopPropagation();
                              setConfirmingId(s.id);
                            }}
                            aria-label={`Delete ${displayName}`}
                            title="Delete session"
                            className={cn(
                              "inline-flex size-11 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-opacity",
                              "hover:bg-danger/10 hover:text-danger focus-visible:opacity-100",
                              "opacity-0 group-hover:opacity-100",
                            )}
                          >
                            <Trash2 className="size-4" />
                          </button>
                        </div>
                      )}
                    </li>
                  );
                })}
              </ul>
            </div>
          ))
        )}
      </div>
    </>
  );

  if (isDesktop) {
    return (
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogTrigger asChild>{trigger}</DialogTrigger>
        <DialogContent className="max-w-xl gap-0 p-0">
          {/* Right padding leaves room for shadcn's auto-rendered close X
              so the selection controls sit cleanly to its left. */}
          <DialogHeader className="flex-row items-center justify-between gap-2 space-y-0 pr-12">
            <DialogTitle>Sessions</DialogTitle>
            {headerControls}
          </DialogHeader>
          {body}
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <Drawer open={open} onOpenChange={setOpen}>
      <DrawerTrigger asChild>{trigger}</DrawerTrigger>
      <DrawerContent>
        <DrawerHeader className="flex flex-row items-center justify-between gap-2 text-left">
          <DrawerTitle>Sessions</DrawerTitle>
          {headerControls}
        </DrawerHeader>
        {body}
      </DrawerContent>
    </Drawer>
  );
}
