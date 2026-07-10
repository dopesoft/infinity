"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { ArrowUp, BookUser, Grip, Loader2, Phone, PhoneIncoming, PhoneOutgoing } from "lucide-react";
import { Section } from "./Section";
import { ScrollList } from "./ScrollList";
import { SurfaceRow } from "./SurfaceCard";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { ModalSection } from "@/components/ui/modal-content";
import { useRuns } from "@/lib/runs";
import { useWebSocket } from "@/lib/ws/provider";
import { phoneAsk, phoneContacts, type PhoneContact } from "@/lib/api";
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
  lines: LiveLine[];
  done: boolean;
  summary: string;
};

export function PhoneCard({
  items,
  delay = 0.25,
  onOpen,
}: {
  items: DashboardItem[];
  delay?: number;
  onOpen: (item: DashboardItem) => void;
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
            : { callId: p.call_id, direction: p.direction, number: "", lines: [], done: false, summary: "" };
        if (p.number) fresh.number = p.number;
        if (p.done) {
          fresh.done = true;
          fresh.summary = p.summary ?? "";
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
        Icon={Phone}
        delay={delay}
        badge={items.length}
        headerExtra={
          callRunning ? (
            <button
              type="button"
              onClick={() => setModalOpen(true)}
              aria-label="View the live call"
              className="inline-flex h-7 items-center gap-1.5 rounded-full border border-success/40 bg-success/10 px-2.5 text-[11px] font-medium text-success"
            >
              <span className="size-1.5 animate-pulse rounded-full bg-success" aria-hidden />
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
                if (contacts === null) void phoneContacts().then(setContacts);
              }}
              aria-label="Contact book - call someone back"
              aria-expanded={bookOpen}
              className={cn(
                "inline-flex size-7 items-center justify-center rounded-md transition-colors",
                bookOpen
                  ? "bg-brand/15 text-brand"
                  : "text-muted-foreground hover:bg-accent hover:text-foreground",
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
                "inline-flex size-7 items-center justify-center rounded-md transition-colors",
                open
                  ? "bg-brand/15 text-brand"
                  : "text-muted-foreground hover:bg-accent hover:text-foreground",
              )}
            >
              <Grip className="size-4" />
            </button>
            </>
          )
        }
      >
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
                <div className="mb-3 flex min-h-11 items-center gap-2 rounded-xl border border-success/30 bg-success/10 px-3 text-xs text-success">
                  <Phone className="size-3.5 shrink-0" aria-hidden />
                  On it — the outcome will land here and on your phone.
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
                      "h-11 w-full rounded-xl border border-input bg-background pl-3 pr-11 text-sm",
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
                      That didn&apos;t go through — try again in a moment.
                    </p>
                  ) : null}
                </form>
              )}
            </motion.div>
          ) : null}
        </AnimatePresence>

        {items.length === 0 ? (
          <div className="rounded-xl border border-dashed bg-card/30 p-4 text-center text-xs text-muted-foreground">
            No calls yet — Jarvis answers his line and logs every call here.
          </div>
        ) : (
          <ScrollList max={4}>
            <ul className="space-y-2">
              {items.map((it) => (
                <li key={`${it.kind}-${it.data.id}`}>
                  {it.kind === "surface" ? (
                    <SurfaceRow item={it.data} onClick={() => onOpen(it)} />
                  ) : null}
                </li>
              ))}
            </ul>
          </ScrollList>
        )}
      </Section>

      <ContactBookModal
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
          (live?.number ? " · " + live.number : "")
        }
        description={callRunning ? `Live · ${elapsed}` : "Call ended"}
        size="md"
      >
        <div className="space-y-3">
          {live?.done && live.summary ? (
            <ModalSection label="Outcome">
              <p className="text-sm">{live.summary}</p>
            </ModalSection>
          ) : null}

          {live && live.lines.length > 0 ? (
            <div className="max-h-[50dvh] min-w-0 space-y-3 overflow-y-auto rounded-xl border bg-card/40 p-3 scroll-touch">
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
            <div className="rounded-xl border border-dashed bg-card/30 p-4 text-center text-xs text-muted-foreground">
              {callRunning
                ? "Connected — the transcript streams in as they talk."
                : "No transcript captured for this call."}
            </div>
          )}
        </div>
      </ResponsiveModal>
    </>
  );
}

/* ContactBookModal - the dial-back book. ResponsiveModal owns the
 * dialog-vs-drawer split (drawer on mobile, per the house rule). Desktop:
 * master-detail - searchable name list left, selected contact's call
 * history right. Mobile: the same two panes stacked as list → detail
 * (tap a name, back arrow returns).
 */
function ContactBookModal({
  open,
  onOpenChange,
  contacts,
  onCall,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  contacts: PhoneContact[] | null;
  onCall: (c: PhoneContact) => void;
}) {
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<PhoneContact | null>(null);

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
    () => (selected?.history ?? selected?.last ?? "").split(" | Previously: ").filter(Boolean),
    [selected],
  );

  return (
    <ResponsiveModal open={open} onOpenChange={onOpenChange} title="Contacts" size="lg">
      <div className="grid min-h-[40dvh] grid-cols-1 gap-3 lg:grid-cols-[240px_1fr]">
        {/* Left: search + names. On mobile this pane hides once a contact is
            selected (detail takes over with a back control). */}
        <div className={cn("min-w-0 space-y-2", selected && "hidden lg:block")}>
          <input
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search contacts…"
            inputMode="search"
            autoCapitalize="none"
            className="h-10 w-full rounded-lg border border-input bg-background px-3 text-sm transition-colors focus:border-foreground/40 focus:outline-none"
          />
          <div className="max-h-[45dvh] space-y-0.5 overflow-y-auto scroll-touch">
            {contacts === null ? (
              <div className="flex items-center justify-center p-4">
                <Loader2 className="size-4 animate-spin text-muted-foreground" />
              </div>
            ) : filtered.length === 0 ? (
              <p className="p-3 text-center text-xs text-muted-foreground">
                {query ? "No matches." : "No call history yet."}
              </p>
            ) : (
              filtered.map((c) => (
                <button
                  key={c.number}
                  type="button"
                  onClick={() => setSelected(c)}
                  className={cn(
                    "flex w-full min-w-0 flex-col rounded-lg px-2.5 py-2 text-left transition-colors hover:bg-accent",
                    selected?.number === c.number && "bg-accent",
                  )}
                >
                  <span className="truncate text-sm font-medium">{c.name ?? c.number}</span>
                  {c.name ? (
                    <span className="truncate text-[11px] text-muted-foreground">{c.number}</span>
                  ) : null}
                </button>
              ))
            )}
          </div>
        </div>

        {/* Right: the selected contact's story. */}
        <div className={cn("min-w-0", !selected && "hidden lg:block")}>
          {selected ? (
            <div className="space-y-3">
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <button
                    type="button"
                    onClick={() => setSelected(null)}
                    className="mb-1 text-[11px] font-medium text-muted-foreground hover:text-foreground lg:hidden"
                  >
                    ← All contacts
                  </button>
                  <h3 className="truncate text-base font-semibold tracking-tight">
                    {selected.name ?? selected.number}
                  </h3>
                  <p className="text-xs text-muted-foreground">{selected.number}</p>
                </div>
                <button
                  type="button"
                  onClick={() => onCall(selected)}
                  className="inline-flex h-9 shrink-0 items-center gap-1.5 rounded-lg bg-brand px-3 text-xs font-medium text-brand-foreground hover:opacity-90"
                >
                  <Phone className="size-3.5" />
                  Call
                </button>
              </div>
              <div className="max-h-[40dvh] space-y-2 overflow-y-auto scroll-touch">
                {entries.map((e, i) => (
                  <div key={i} className="rounded-xl border bg-card/40 p-3">
                    <p className="break-words text-xs leading-relaxed">{e}</p>
                  </div>
                ))}
              </div>
            </div>
          ) : (
            <div className="flex h-full min-h-32 items-center justify-center rounded-xl border border-dashed bg-card/30 p-4 text-center text-xs text-muted-foreground">
              Pick a contact to see your history with them.
            </div>
          )}
        </div>
      </div>
    </ResponsiveModal>
  );
}
