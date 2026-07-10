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
                setBookOpen((v) => {
                  const next = !v;
                  if (next) {
                    setOpen(true);
                    if (contacts === null) void phoneContacts().then(setContacts);
                  }
                  return next;
                });
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
              {bookOpen && contacts !== null ? (
              <div className="mb-3 max-h-44 space-y-1 overflow-y-auto rounded-xl border bg-card/40 p-1.5 scroll-touch">
                {contacts.length === 0 ? (
                  <p className="p-2 text-center text-xs text-muted-foreground">No call history yet.</p>
                ) : (
                  contacts.map((c) => (
                    <button
                      key={c.number}
                      type="button"
                      onClick={() => {
                        setPrompt(`Call ${c.name ?? c.number} at ${c.number}: `);
                        setBookOpen(false);
                      }}
                      className="flex w-full min-w-0 items-center gap-2 rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-accent"
                    >
                      <Phone className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
                      <span className="min-w-0 flex-1 truncate text-xs font-medium">
                        {c.name ?? c.number}
                        {c.name ? <span className="ml-1.5 font-normal text-muted-foreground">{c.number}</span> : null}
                      </span>
                    </button>
                  ))
                )}
              </div>
            ) : null}
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
                      "transition-colors focus:border-foreground/30 focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 ring-offset-background",
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
