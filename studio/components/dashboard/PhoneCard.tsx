"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { ArrowUp, BookUser, Building2, Grip, Loader2, Pencil, Phone, PhoneIncoming, PhoneOutgoing, Plus, Trash2, User } from "lucide-react";
import { Section } from "./Section";
import { ScrollList } from "./ScrollList";
import { DASHBOARD_LIST_ROWS } from "./listHeight";
import { SurfaceRow } from "./SurfaceCard";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { ModalSection } from "@/components/ui/modal-content";
import { useRuns } from "@/lib/runs";
import { useWebSocket } from "@/lib/ws/provider";
import { deletePhoneContact, phoneAsk, phoneContacts, savePhoneContact, type PhoneContact } from "@/lib/api";
import { cn } from "@/lib/utils";
import type { DashboardItem } from "@/lib/dashboard/types";

/* PhoneCard - Jarvis's phone line on the dashboard.
 *
 * Three states in the card header, one slot:
 *   idle      → dialpad button; tap slides down the call-errand field
 *   live call → the button BECOMES the call: "Outgoing · 00:47" ticking,
 *               pulsing; tap opens the live transcript modal
 *   call ends → the modal (if open) swaps in the outcome summary; the
 *               durable row lands in the list below via realtime
 *
 * Live state rides two existing rails: the mem_runs row (kind=phone.call,
 * status=running - server-tracked, survives navigation/refresh) drives the
 * indicator + timer, and phone_live WS events stream the transcript lines
 * into the modal. Neither costs a DB write per line.
 */

type LiveLine = { speaker: string; text: string };
type LiveCall = {
  callId: string;
  direction: "inbound" | "outbound";
  number: string;
  /** Who that is, per the phone book. "Incoming call" told you nothing when
   *  Jarvis knew perfectly well it was Ariana. */
  name: string;
  lines: LiveLine[];
  done: boolean;
  summary: string;
  status: string;
};

export function PhoneCard({
  items,
  /* 0 by default: the cards used to fade in on staggered timers, which read as
     a row that settles rather than a row that is simply there. */
  delay = 0,
  onOpen,
  matchHeight,
}: {
  items: DashboardItem[];
  delay?: number;
  onOpen: (item: DashboardItem) => void;
  /**
   * The dashboard's shared list height (px), measured off the Email card.
   * Call rows are ~2 lines where an email row is ~3, so clipping this list
   * at 4 ROWS left it half the height of the card the grid stretched it to,
   * with dead space below and a scrollbox that ended mid-card. Clipping at
   * the shared pixel line instead fills the card with as many calls as fit
   * and scrolls the rest. Null before the reference reports → `max` applies.
   */
  matchHeight?: number | null;
}) {
  // ── call-errand field ────────────────────────────────────────────────
  const [open, setOpen] = useState(false);
  const [bookOpen, setBookOpen] = useState(false);
  const [contacts, setContacts] = useState<PhoneContact[] | null>(null);
  const [prompt, setPrompt] = useState("");
  const [busy, setBusy] = useState(false);
  const [state, setState] = useState<"idle" | "sent" | "error">("idle");

  // ── live call ────────────────────────────────────────────────────────
  const ws = useWebSocket();
  const { latest: liveRun } = useRuns({ kind: "phone.call", limit: 3 });
  // The errand turn itself (find number → brief → dial). "On it" is only
  // an acceptance; THIS is what succeeded or died - and a dead errand must
  // be loudly visible, never silence (the never-hide-errors law).
  const { latest: askRun } = useRuns({ kind: "phone.ask", limit: 3 });
  const callRunning = liveRun?.status === "running";
  const [live, setLive] = useState<LiveCall | null>(null);
  const [modalOpen, setModalOpen] = useState(false);
  const transcriptEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    return ws.subscribe((ev) => {
      if (ev.type !== "phone_live") return;
      const p = ev.phone_live;
      setLive((cur) => {
        const fresh: LiveCall =
          cur && cur.callId === p.call_id
            ? { ...cur }
            : { callId: p.call_id, direction: p.direction, number: "", name: "", lines: [], done: false, summary: "", status: "" };
        if (p.number) fresh.number = p.number;
        if (p.name) fresh.name = p.name;
        if (p.done) {
          fresh.done = true;
          fresh.summary = p.summary ?? "";
          fresh.status = p.status ?? "";
        } else if (p.text) {
          fresh.lines = [...fresh.lines, { speaker: p.speaker ?? "?", text: p.text }];
        }
        return fresh;
      });
    });
  }, [ws]);

  // Auto-scroll the live transcript as lines arrive.
  useEffect(() => {
    if (modalOpen) transcriptEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [live?.lines.length, modalOpen]);

  // Ticking duration while the run is live (from the server-tracked row, so
  // it's correct after a refresh mid-call).
  const [nowTick, setNowTick] = useState(0);
  useEffect(() => {
    if (!callRunning) return;
    const t = window.setInterval(() => setNowTick((n) => n + 1), 1000);
    return () => window.clearInterval(t);
  }, [callRunning]);

  const elapsed = useMemo(() => {
    void nowTick; // re-derive every tick
    if (!callRunning || !liveRun?.started_at) return "";
    const secs = Math.max(0, Math.floor((Date.now() - new Date(liveRun.started_at).getTime()) / 1000));
    const m = Math.floor(secs / 60);
    const s = secs % 60;
    return `${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
  }, [callRunning, liveRun?.started_at, nowTick]);

  // Direction: live WS events are authoritative; the run label ("outbound
  // call to …") covers the refresh-mid-call case before a line arrives.
  const direction: "inbound" | "outbound" =
    live && !live.done ? live.direction : liveRun?.label?.startsWith("outbound") ? "outbound" : "inbound";
  const DirIcon = direction === "outbound" ? PhoneOutgoing : PhoneIncoming;

  // Keep the contact book fresh in realtime: a completed call updates the
  // history, and the call list (items) is realtime-published, so refetch
  // whenever it changes (and the book is open).
  useEffect(() => {
    if (bookOpen) void phoneContacts().then(setContacts);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items.length, bookOpen]);

  async function submit() {
    const p = prompt.trim();
    if (!p || busy) return;
    setBusy(true);
    const ok = await phoneAsk(p);
    setBusy(false);
    setState(ok ? "sent" : "error");
    if (ok) {
      setPrompt("");
      window.setTimeout(() => {
        setOpen(false);
        setState("idle");
      }, 3500);
    }
  }

  return (
    <>
      <Section
        title="Phone"
        delay={delay}
        badge={items.length}
        headerExtra={
          callRunning ? (
            <button
              type="button"
              onClick={() => setModalOpen(true)}
              aria-label="View the live call"
              className="inline-flex h-8 items-center gap-1.5 rounded-lg px-2 text-[12.5px] font-medium text-brand transition-colors hover:bg-accent/60"
            >
              <span className="size-[7px] animate-pulse-soft rounded-full bg-brand" aria-hidden />
              <DirIcon className="size-3" aria-hidden />
              <span className="capitalize">{direction === "outbound" ? "Outgoing" : "Incoming"}</span>
              <span className="font-mono tabular-nums">{elapsed}</span>
            </button>
          ) : (
            <>
            <button
              type="button"
              onClick={() => {
                setBookOpen(true);
                void phoneContacts().then(setContacts);
              }}
              aria-label="Contact book - call someone back"
              aria-expanded={bookOpen}
              className={cn(
                "inline-flex size-8 items-center justify-center rounded-lg transition-colors",
                bookOpen ? "bg-accent text-foreground" : "text-quiet hover:bg-accent/60 hover:text-foreground",
              )}
            >
              <BookUser className="size-4" />
            </button>
            <button
              type="button"
              onClick={() => setOpen((v) => !v)}
              aria-label="Have Jarvis make a call"
              aria-expanded={open}
              className={cn(
                "inline-flex size-8 items-center justify-center rounded-lg transition-colors",
                open ? "bg-accent text-foreground" : "text-quiet hover:bg-accent/60 hover:text-foreground",
              )}
            >
              <Grip className="size-4" />
            </button>
            </>
          )
        }
      >
        {askRun?.status === "error" ? (
          <div className="mb-3 min-w-0 rounded-[10px] bg-danger/10 px-3 py-2.5">
            <p className="text-[13.5px] font-medium text-danger">Your call errand failed</p>
            <p className="mt-0.5 break-words text-[12px] leading-relaxed text-muted-foreground">
              {askRun.human_error?.summary ?? askRun.error ?? "The run died before dialing."}
            </p>
          </div>
        ) : askRun?.status === "running" && !callRunning ? (
          <div className="mb-3 flex min-w-0 items-center gap-2 rounded-[10px] bg-muted px-3 py-2.5 text-[12px] text-muted-foreground">
            <Loader2 className="size-3.5 shrink-0 animate-spin text-brand" aria-hidden />
      Working your call errand, finding the number and briefing the call…
          </div>
        ) : null}

        {/* Slide-down call errand field (the dialpad button toggles it). */}
        <AnimatePresence initial={false}>
          {open && !callRunning ? (
            <motion.div
              key="call-errand"
              initial={{ height: 0, opacity: 0 }}
              animate={{ height: "auto", opacity: 1 }}
              exit={{ height: 0, opacity: 0 }}
              transition={{ duration: 0.2, ease: [0.2, 0.7, 0.2, 1] }}
              className="overflow-hidden"
            >
              {state === "sent" ? (
                <div className="mb-3 flex min-h-11 items-center gap-2 rounded-[10px] bg-brand/10 px-3 text-[12.5px] text-brand">
                  <Phone className="size-3.5 shrink-0" aria-hidden />
         On it, the outcome will land here and on your phone.
                </div>
              ) : (
                <form
                  className="relative mb-3"
                  onSubmit={(e) => {
                    e.preventDefault();
                    void submit();
                  }}
                >
                  <input
                    type="text"
                    value={prompt}
                    onChange={(e) => setPrompt(e.target.value)}
                    placeholder="Call Sanson's Pizza in Frisco, order a pepperoni for pickup…"
                    autoFocus
                    inputMode="text"
                    autoCapitalize="sentences"
                    autoCorrect="on"
                    className={cn(
                      "h-11 w-full rounded-lg border border-input bg-background pl-3 pr-11 text-sm",
                      "transition-colors focus:border-foreground/40 focus:outline-none",
                    )}
                  />
                  <button
                    type="submit"
                    disabled={busy || prompt.trim() === ""}
                    aria-label="Send call errand"
                    className={cn(
                      "absolute right-1.5 top-1/2 inline-flex size-8 -translate-y-1/2 items-center justify-center rounded-lg transition-colors",
                      prompt.trim() === ""
                        ? "text-muted-foreground"
                        : "bg-brand text-brand-foreground hover:opacity-90",
                      "disabled:cursor-not-allowed disabled:opacity-60",
                    )}
                  >
                    {busy ? <Loader2 className="size-4 animate-spin" /> : <ArrowUp className="size-4" />}
                  </button>
                  {state === "error" ? (
                    <p className="mt-1.5 text-xs text-danger">
           That didn&apos;t go through, try again in a moment.
                    </p>
                  ) : null}
                </form>
              )}
            </motion.div>
          ) : null}
        </AnimatePresence>

        {items.length === 0 ? (
          <p className="py-2 text-[13px] text-quiet">
            No calls yet, Jarvis answers his line and logs every call here.
          </p>
        ) : (
          <ScrollList max={DASHBOARD_LIST_ROWS} maxHeight={matchHeight ?? undefined}>
            <div className="flex min-w-0 flex-col">
              {items.map((it) =>
                it.kind === "surface" ? (
                  <SurfaceRow
                    key={`${it.kind}-${it.data.id}`}
                    item={it.data}
                    onClick={() => onOpen(it)}
                  />
                ) : null,
              )}
            </div>
          </ScrollList>
        )}
      </Section>

      <ContactBookModal
        onChanged={() => void phoneContacts().then(setContacts)}
        open={bookOpen}
        onOpenChange={setBookOpen}
        contacts={contacts}
        onCall={(c) => {
          setBookOpen(false);
          setPrompt(`Call ${c.name ?? c.number} at ${c.number}: `);
          setOpen(true);
        }}
      />

      {/* Live-call modal: the transcript view, but streaming. When the call
          ends the outcome summary slots in on top - same shape as the
          durable card's body, just live. */}
      <ResponsiveModal
        open={modalOpen}
        onOpenChange={setModalOpen}
        title={
          (direction === "outbound" ? "Outgoing call" : "Incoming call") +
          (live?.name ? (direction === "outbound" ? " to " : " from ") + live.name : "") +
          (live?.number ? " · " + live.number : "")
        }
        description={
          callRunning
            ? `Live · ${elapsed}`
            : live?.status && live.status !== "completed"
              ? cleanCallStatus(live.status)
              : "Call ended"
        }
        size="md"
      >
        <div className="space-y-3">
          {live?.done && live.summary ? (
            <ModalSection label="Outcome">
              <p className="text-sm">{live.summary}</p>
            </ModalSection>
          ) : null}

          {live && live.lines.length > 0 ? (
            <div className="max-h-[50dvh] min-w-0 space-y-3 overflow-y-auto rounded-[10px] bg-muted p-3 scroll-touch">
              {live.lines.map((l, i) => (
                <div key={i} className="flex min-w-0 gap-3">
                  <span className="w-14 shrink-0 pt-0.5 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                    {l.speaker}
                  </span>
                  <p className="min-w-0 flex-1 break-words text-sm">{l.text}</p>
                </div>
              ))}
              {callRunning && !live.done ? (
                <div className="flex items-center gap-2 pl-[68px] text-xs text-muted-foreground">
                  <span className="size-1.5 animate-pulse rounded-full bg-success" aria-hidden />
                  live
                </div>
              ) : null}
              <div ref={transcriptEndRef} />
            </div>
          ) : (
            <p className="py-2 text-[13px] text-quiet">
              {callRunning
        ? "Connected, the transcript streams in as they talk."
                : "No transcript captured for this call."}
            </p>
          )}
        </div>
      </ResponsiveModal>
    </>
  );
}

function cleanCallStatus(status: string): string {
  return (
    {
      "no-answer": "No answer",
      busy: "Line was busy",
      failed: "Call failed to connect",
      canceled: "Call canceled",
      completed: "Call ended",
    }[status] ?? "Call ended"
  );
}

function ContactIcon({ kind, className }: { kind?: string; className?: string }) {
  if (kind === "org") return <Building2 className={className} aria-hidden />;
  if (kind === "person") return <User className={className} aria-hidden />;
  return <Phone className={className} aria-hidden />;
}

/* ContactBookModal - the phone book. ResponsiveModal owns the
 * dialog-vs-drawer split (drawer on mobile, per the house rule). Desktop:
 * master-detail - searchable name list left, selected contact's detail
 * right. Mobile: the same two panes stacked as list → detail
 * (tap a name, back arrow returns).
 *
 * The book is EDITABLE here, because Jarvis learns contacts from calls and can
 * mishear a name, and a book you cannot correct fills up with rubbish. Add,
 * rename, re-note, delete. Deleting confirms INLINE rather than stacking a
 * second modal on top of this one: nested dialogs are a trap on a phone.
 */
function ContactBookModal({
  open,
  onOpenChange,
  contacts,
  onCall,
  onChanged,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  contacts: PhoneContact[] | null;
  onCall: (c: PhoneContact) => void;
  onChanged: () => void;
}) {
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<PhoneContact | null>(null);
  // null = not editing. A contact = editing it. "new" = adding one.
  const [editing, setEditing] = useState<PhoneContact | "new" | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const all = contacts ?? [];
    if (!q) return all;
    return all.filter(
      (c) => (c.name ?? "").toLowerCase().includes(q) || c.number.includes(q),
    );
  }, [contacts, query]);

  // History entries: newest first, split on the rolling separator.
  const entries = useMemo(
    () => (selected?.history ?? "").split(" | Previously: ").filter(Boolean),
    [selected],
  );

  // Leaving a contact drops any half-finished edit, so reopening never lands
  // you in a stale form.
  const select = (c: PhoneContact | null) => {
    setSelected(c);
    setEditing(null);
    setConfirmDelete(false);
  };

  const remove = async () => {
    if (!selected) return;
    await deletePhoneContact(selected.number);
    select(null);
    onChanged();
  };

  return (
    <ResponsiveModal open={open} onOpenChange={onOpenChange} title="Contacts" size="lg">
      <div className="grid min-h-[40dvh] grid-cols-1 gap-3 lg:grid-cols-[240px_1fr]">
        {/* Left: search + names. On mobile this pane hides once a contact is
            selected (detail takes over with a back control). */}
        <div className={cn("min-w-0 space-y-2", (selected || editing) && "hidden lg:block")}>
          <div className="flex items-center gap-2">
            <input
              type="text"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search contacts…"
              inputMode="search"
              autoCapitalize="none"
              className="h-10 min-w-0 flex-1 rounded-lg border border-input bg-background px-3 text-sm transition-colors focus:border-foreground/40 focus:outline-none"
            />
            <button
              type="button"
              onClick={() => {
                setSelected(null);
                setEditing("new");
              }}
              aria-label="Add a contact"
              className="inline-flex size-10 shrink-0 items-center justify-center rounded-lg border border-input text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              <Plus className="size-4" />
            </button>
          </div>
          <div className="max-h-[45dvh] space-y-0.5 overflow-y-auto scroll-touch">
            {contacts === null ? (
              <div className="flex items-center justify-center p-4">
                <Loader2 className="size-4 animate-spin text-muted-foreground" />
              </div>
            ) : filtered.length === 0 ? (
              <p className="p-3 text-center text-xs text-muted-foreground">
                {query ? "No matches." : "No contacts yet. Add one, or Jarvis will as he calls."}
              </p>
            ) : (
              filtered.map((c) => (
                <button
                  key={c.number}
                  type="button"
                  onClick={() => select(c)}
                  className={cn(
                    "flex w-full min-w-0 items-center gap-2.5 rounded-lg px-2.5 py-2 text-left transition-colors hover:bg-accent",
                    selected?.number === c.number && "bg-accent",
                  )}
                >
                  <ContactIcon kind={c.kind} className="size-4 shrink-0 text-muted-foreground" />
                  <span className="flex min-w-0 flex-col">
                    <span className="truncate text-sm font-medium">{c.name ?? c.number}</span>
                    <span className="truncate text-[11px] text-muted-foreground">
                      {c.name ? c.number : "unnamed"}
                      {c.location ? ` · ${c.location}` : ""}
                    </span>
                  </span>
                </button>
              ))
            )}
          </div>
        </div>

        {/* Right: the selected contact, or the add/edit form. */}
        <div className={cn("min-w-0", !selected && !editing && "hidden lg:block")}>
          {editing ? (
            <ContactForm
              contact={editing === "new" ? null : editing}
              onCancel={() => {
                setEditing(null);
                if (editing === "new") setSelected(null);
              }}
              onSaved={() => {
                setEditing(null);
                select(null);
                onChanged();
              }}
            />
          ) : selected ? (
            <div className="space-y-3">
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <button
                    type="button"
                    onClick={() => select(null)}
                    className="mb-1 text-[11px] font-medium text-muted-foreground hover:text-foreground lg:hidden"
                  >
                    ← All contacts
                  </button>
                  <h3 className="flex items-center gap-2 truncate text-base font-semibold tracking-tight">
                    <ContactIcon kind={selected.kind} className="size-4 shrink-0 text-muted-foreground" />
                    {selected.name ?? selected.number}
                  </h3>
                  <p className="text-xs text-muted-foreground">
                    {selected.number}
                    {selected.location ? ` · ${selected.location}` : ""}
                  </p>
                </div>
                <div className="flex shrink-0 items-center gap-1.5">
                  <button
                    type="button"
                    onClick={() => setEditing(selected)}
                    aria-label="Edit contact"
                    className="inline-flex size-9 items-center justify-center rounded-lg border border-input text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                  >
                    <Pencil className="size-3.5" />
                  </button>
                  <button
                    type="button"
                    onClick={() => onCall(selected)}
                    className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-brand px-3 text-xs font-medium text-brand-foreground hover:opacity-90"
                  >
                    <Phone className="size-3.5" />
                    Call
                  </button>
                </div>
              </div>

              {selected.note ? (
                <p className="break-words text-xs leading-relaxed text-muted-foreground">{selected.note}</p>
              ) : null}

              <div className="max-h-[34dvh] space-y-2 overflow-y-auto scroll-touch">
                {entries.length === 0 ? (
                  <p className="text-[13px] text-quiet">No calls with them yet.</p>
                ) : (
                  entries.map((e, i) => (
                    <div key={i} className="min-w-0 rounded-[10px] bg-muted px-3 py-2.5">
                      <p className="break-words text-[13px] leading-relaxed">{e}</p>
                    </div>
                  ))
                )}
              </div>

              {/* Delete confirms in place. A second modal over a drawer is a
                  trap on a phone: you cannot see what you are agreeing to. */}
              {confirmDelete ? (
                <div className="flex flex-wrap items-center gap-2 rounded-[10px] bg-danger/10 p-3">
                  <p className="min-w-0 flex-1 text-xs">
                    Remove {selected.name ?? selected.number} from your phone book?
                  </p>
                  <button
                    type="button"
                    onClick={() => setConfirmDelete(false)}
                    className="h-9 rounded-lg border border-input px-3 text-xs font-medium hover:bg-accent"
                  >
                    Keep
                  </button>
                  <button
                    type="button"
                    onClick={remove}
                    className="h-9 rounded-lg bg-danger px-3 text-xs font-medium text-white hover:opacity-90"
                  >
                    Remove
                  </button>
                </div>
              ) : (
                <button
                  type="button"
                  onClick={() => setConfirmDelete(true)}
                  className="inline-flex items-center gap-1.5 text-[11px] font-medium text-muted-foreground transition-colors hover:text-danger"
                >
                  <Trash2 className="size-3.5" />
                  Remove contact
                </button>
              )}
            </div>
          ) : (
            <div className="flex h-full min-h-32 items-center justify-center p-4 text-center text-[13px] text-quiet">
              Pick a contact to see your history with them.
            </div>
          )}
        </div>
      </div>
    </ResponsiveModal>
  );
}

/* ContactForm - add or correct one contact.
 *
 * Deliberately short: name and number are all that is required, everything else
 * earns its place. The number is validated by the SERVER (the same rule that
 * decides what is dialable), and its message is shown verbatim, so the form can
 * never accept something that would fail at dial time.
 */
function ContactForm({
  contact,
  onCancel,
  onSaved,
}: {
  contact: PhoneContact | null;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState(contact?.name ?? "");
  const [number, setNumber] = useState(contact?.number ?? "");
  const [kind, setKind] = useState<"person" | "org">(contact?.kind ?? "person");
  const [location, setLocation] = useState(contact?.location ?? "");
  const [note, setNote] = useState(contact?.note ?? "");
  const [aliases, setAliases] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const save = async () => {
    setSaving(true);
    setError("");
    const res = await savePhoneContact({
      name: name.trim(),
      number: number.trim(),
      kind,
      location: location.trim(),
      note: note.trim(),
      aliases: aliases.split(",").map((a) => a.trim()).filter(Boolean),
      was: contact?.number,
    });
    setSaving(false);
    if (res.ok) onSaved();
    else setError(res.error);
  };

  const canSave = name.trim() !== "" && number.trim() !== "" && !saving;

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-base font-semibold tracking-tight">
          {contact ? "Edit contact" : "New contact"}
        </h3>
        <button
          type="button"
          onClick={onCancel}
          className="text-[11px] font-medium text-muted-foreground hover:text-foreground"
        >
          Cancel
        </button>
      </div>

      <div className="space-y-2.5">
        <Field label="Name">
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Ariana"
            autoCapitalize="words"
            className="h-11 w-full rounded-lg border border-input bg-background px-3 text-sm focus:border-foreground/40 focus:outline-none"
          />
        </Field>

        <Field label="Number">
          <input
            type="tel"
            value={number}
            onChange={(e) => setNumber(e.target.value)}
            placeholder="929-310-0906"
            inputMode="tel"
            className="h-11 w-full rounded-lg border border-input bg-background px-3 text-sm focus:border-foreground/40 focus:outline-none"
          />
        </Field>

        {/* Person or business: a two-up toggle, not a select. One tap, and you
            can see both options without opening anything. */}
        <Field label="Type">
          <div className="grid grid-cols-2 gap-1.5">
            {(["person", "org"] as const).map((k) => (
              <button
                key={k}
                type="button"
                onClick={() => setKind(k)}
                className={cn(
                  "inline-flex h-11 items-center justify-center gap-1.5 rounded-lg border text-xs font-medium transition-colors",
                  kind === k
                    ? "border-foreground/30 bg-accent text-foreground"
                    : "border-input text-muted-foreground hover:bg-accent/50",
                )}
              >
                <ContactIcon kind={k} className="size-3.5" />
                {k === "person" ? "Person" : "Business"}
              </button>
            ))}
          </div>
        </Field>

        {kind === "org" ? (
          <Field label="Which one" hint="So Jarvis never asks you twice">
            <input
              type="text"
              value={location}
              onChange={(e) => setLocation(e.target.value)}
              placeholder="The one on Preston Road"
              className="h-11 w-full rounded-lg border border-input bg-background px-3 text-sm focus:border-foreground/40 focus:outline-none"
            />
          </Field>
        ) : null}

        <Field label="Also called" hint="Comma separated, so “call my wife” works">
          <input
            type="text"
            value={aliases}
            onChange={(e) => setAliases(e.target.value)}
            placeholder="my wife, Ari"
            autoCapitalize="none"
            className="h-11 w-full rounded-lg border border-input bg-background px-3 text-sm focus:border-foreground/40 focus:outline-none"
          />
        </Field>

        <Field label="Note" hint="Anything Jarvis should know about them">
          <textarea
            value={note}
            onChange={(e) => setNote(e.target.value)}
            rows={2}
            placeholder="My wife. Mother of Valentino."
            className="w-full resize-none rounded-lg border border-input bg-background px-3 py-2 text-sm focus:border-foreground/40 focus:outline-none"
          />
        </Field>
      </div>

      {error ? (
        <p className="rounded-lg border border-danger/40 bg-danger/10 p-2.5 text-xs text-danger">{error}</p>
      ) : null}

      <button
        type="button"
        disabled={!canSave}
        onClick={save}
        className="inline-flex h-11 w-full items-center justify-center gap-2 rounded-lg bg-brand text-sm font-medium text-brand-foreground transition-opacity hover:opacity-90 disabled:opacity-40"
      >
        {saving ? <Loader2 className="size-4 animate-spin" /> : null}
        {contact ? "Save changes" : "Add to phone book"}
      </button>
    </div>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block space-y-1.5">
      <span className="flex items-baseline gap-2">
        <span className="text-[11px] font-medium text-muted-foreground">{label}</span>
        {hint ? <span className="truncate text-[10px] text-muted-foreground/70">{hint}</span> : null}
      </span>
      {children}
    </label>
  );
}
