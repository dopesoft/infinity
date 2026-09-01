"use client";

import * as React from "react";
import { useAppRouter } from "@/lib/loading";
import { motion, AnimatePresence } from "framer-motion";
import {
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  Circle,
  ExternalLink,
  AlignLeft,
  MapPin,
  Paperclip,
  Pencil,
  Repeat,
  Send,
  Sparkles,
  Users,
  Video,
  X,
  MessagesSquare,
} from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { authedFetch, triggerCron, cancelWork, postSurfaceAction, canvasProjectActivate } from "@/lib/api";
import { stashPendingDoc } from "@/lib/canvas/store";
import { RunIndicator, useRuns } from "@/lib/runs";
import { useWebSocket } from "@/lib/ws/provider";
import {
  ResponsiveModal,
  ResponsiveModalHeader,
} from "@/components/ui/responsive-modal";
import { Button } from "@/components/ui/button";
import { Inset } from "@/components/ui/inset";
import { Textarea } from "@/components/ui/textarea";
import {
  ModalChips,
  ModalDl,
  ModalField,
  ModalHtml,
  ModalPayload,
  ModalPre,
  ModalSection,
  ModalUrl,
} from "@/components/ui/modal-content";
import { PlanTimeline } from "./PlanTimeline";
import { EditTodoModal } from "./EditTodoModal";
import { cn } from "@/lib/utils";
import { clockTime, dayLabel, eventDate, formatDuration, fullDateTime, relTime } from "@/lib/dashboard/format";
import { seedSession } from "@/lib/dashboard/seed";
import { parseLabeledBody } from "@/lib/dashboard/parseBody";
import type {
  Approval,
  Artifact,
  CalendarEvent,
  DashboardItem,
  FollowUp,
  Pursuit,
  RecordDetail,
  Reflection,
  Saved,
  SurfaceItem,
  Todo,
  ActivityEvent as ActivityEv,
  WorkItem,
} from "@/lib/dashboard/types";

/* ObjectViewer - the responsive preview surface for every dashboard item.
 *
 * Per the ObjectViewer pattern:
 *  - Dialog on lg+, Drawer on <lg.
 *  - Renders the artifact in its native form (full email body, full diff,
 *    full event card, etc.) - *not* a summary.
 *  - "Discuss with Jarvis" is the only path to a seeded /live session.
 *  - Many items also surface kind-specific quick actions (approve, snooze,
 *    mark done, dismiss) so the user can act without ever opening a chat.
 */
export function ObjectViewer({
  item,
  onClose,
  onResolved,
}: {
  item: DashboardItem | null;
  onClose: () => void;
  onResolved?: (item: DashboardItem) => void;
}) {
  const open = item !== null;
  // Event modals get a tinted lavender footer + justify-between layout
  // (label left, RSVP cluster right) matching the design ref. For
  // organizer-owned events (no self responseStatus) we drop the footer
  // entirely - there's nothing to RSVP and the design doesn't show a
  // bar in that case either.
  const isEvent = item?.kind === "event";
  const isInviteeEvent = isEvent && !!item?.data && (item.data as CalendarEvent).responseStatus;
  const hasFooter = !isEvent || isInviteeEvent;
  // Majordomo §7: the footer is `justify-between` for every kind - secondary
  // and destructive actions left, the ONE primary right. The event RSVP bar
  // keeps its lavender ground (it is a designed surface, not modal chrome).
  const footerOverride = isEvent
    ? "bg-violet-100/70 dark:bg-violet-950/40 justify-between"
    : "justify-between";
  // The email viewer gets a wider, fixed-height frame on desktop so it reads
  // comfortably AND doesn't grow/shrink as the lazy email body loads. Other
  // kinds keep the snug, content-sized `lg` modal.
  const isFollowup = item?.kind === "followup";
  const [editingTodo, setEditingTodo] = React.useState<Todo | null>(null);
  return (
    <>
      <ResponsiveModal
        open={open}
        onOpenChange={(o) => (!o ? onClose() : null)}
        size={isFollowup ? "xl" : "lg"}
        desktopHeight={isFollowup ? "tall" : "auto"}
        title={item ? getViewerTitle(item) : "Item"}
        description={item ? getViewerKindLabel(item) : undefined}
        header={item ? <ItemHeader item={item} /> : undefined}
        footer={
          item && hasFooter ? (
            <ViewerActions
              item={item}
              onResolved={onResolved}
              onClose={onClose}
              onEditTodo={setEditingTodo}
            />
          ) : undefined
        }
        footerClassName={footerOverride}
      >
        <AnimatePresence mode="wait">
          {item ? <ViewerBody key={getViewerKey(item)} item={item} /> : null}
        </AnimatePresence>
      </ResponsiveModal>
      <EditTodoModal
        todo={editingTodo}
        open={!!editingTodo}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setEditingTodo(null);
        }}
      />
    </>
  );
}

/* ItemHeader - ONE header shape for every kind (Majordomo §7).
 *
 * Was: an icon chip + a mono uppercase eyebrow + the title + a kind subtitle
 * for most kinds, plus a bespoke hand-rolled header for events with its own
 * typography and a tone-coded classification pill. That is the "a header with
 * a title, some subtext, then inside that another header with more subtext"
 * the boss called out.
 *
 * Now: title + ONE context line, for all eleven kinds, through the shared
 * ResponsiveModalHeader. Everything the eyebrow / pill / bespoke date row used
 * to say is a segment of the context line (see viewerContext), so no
 * information is lost - the event's classification, date, time and duration
 * all still show, as words instead of as chrome. The calendar deep-link
 * survives as the header's trailing slot because it is an ACTION, not chrome. */
function ItemHeader({ item }: { item: DashboardItem }) {
  const link = item.kind === "event" ? item.data.htmlLink : undefined;
  return (
    <ResponsiveModalHeader
      title={getViewerTitle(item)}
      subtitle={<span suppressHydrationWarning>{viewerContext(item)}</span>}
      trailing={
        link ? (
          <a
            href={link}
            target="_blank"
            rel="noreferrer noopener"
            aria-label="Open in Google Calendar"
            className="inline-flex size-8 items-center justify-center rounded-[10px] text-quiet transition-colors hover:bg-accent hover:text-foreground"
          >
            <ExternalLink className="size-4" aria-hidden />
          </a>
        ) : undefined
      }
    />
  );
}

// eventDateLine: "Thu, 1st April, 2026" format from the design ref.
// Uses ordinal suffix on the day. en-GB locale ordering (day-month-year).
function eventDateLine(iso: string, allDay?: boolean): string {
  const d = eventDate(iso, allDay);
  if (Number.isNaN(d.getTime())) return "";
  const weekday = d.toLocaleDateString("en-GB", { weekday: "short" });
  const day = d.getDate();
  const month = d.toLocaleDateString("en-GB", { month: "long" });
  const year = d.getFullYear();
  return `${weekday}, ${day}${ordinalSuffix(day)} ${month}, ${year}`;
}

function ordinalSuffix(n: number): string {
  const s = ["th", "st", "nd", "rd"];
  const v = n % 100;
  return s[(v - 20) % 10] || s[v] || s[0];
}

// eventDuration: humanised gap between two ISO times. "(2 hours)",
// "(30 min)", "(1 hour 30 min)". Matches the design ref parenthesised
// duration appended to the time range.
function eventDuration(startISO: string, endISO: string): string {
  const ms = new Date(endISO).getTime() - new Date(startISO).getTime();
  if (!Number.isFinite(ms) || ms <= 0) return "";
  const mins = Math.round(ms / 60000);
  if (mins < 60) return `${mins} min`;
  const hours = Math.floor(mins / 60);
  const rem = mins % 60;
  const hLabel = hours === 1 ? "1 hour" : `${hours} hours`;
  if (rem === 0) return hLabel;
  return `${hLabel} ${rem} min`;
}

// viewerContext: the ONE context line under every modal title (§7). Kind
// label, then status, then when - interpunct-joined, quiet ink, never a
// second title.
//
// This is also where every chip row that used to sit at the top of a body
// went. A segment is included only when it carries signal: an unranked item
// never shows "imp 0", a source-less item omits the source, a pursuit with no
// streak omits the streak. Nothing here may restate the title (§1.3), which is
// why a follow-up's subject and an event's own name never appear.
function viewerContext(item: DashboardItem): string {
  const { label } = headerMeta(item);
  const parts: string[] = [];
  const push = (v: string | number | null | undefined | false) => {
    if (v === null || v === undefined || v === false) return;
    const s = String(v).trim();
    if (s) parts.push(s);
  };

  switch (item.kind) {
    case "pursuit": {
      const p = item.data;
      push(label);
      push(p.cadence);
      push(p.status ? p.status.replace(/_/g, " ") : "");
      push(p.streakDays ? `${p.streakDays}d streak` : "");
      push(p.doneToday ? "done today" : "open today");
      break;
    }
    case "todo": {
      const t = item.data;
      push(label);
      push(t.priority ? `${t.priority} priority` : "");
      push(t.source);
      push(t.dueAt ? `due ${todoDueLabel(t.dueAt).toLowerCase()}` : "");
      break;
    }
    case "event": {
      const e = item.data;
      // The bespoke event header's classification pill and date/duration row,
      // rendered as words. "Meeting · Thu, 1st April, 2026 · 9:30am – 10:30am
      // · 1 hour".
      push((e.classification || "meeting").toLowerCase());
      push(eventDateLine(e.startsAt, e.allDay));
      if (e.allDay) {
        push("all day");
      } else {
        push(
          `${clockTime(e.startsAt)}${e.endsAt ? ` – ${clockTime(e.endsAt)}` : ""}`,
        );
        if (e.endsAt) push(eventDuration(e.startsAt, e.endsAt));
      }
      break;
    }
    case "reflection": {
      const r = item.data;
      push(label);
      push(`${r.evidenceCount} sources`);
      push(relTime(r.capturedAt));
      break;
    }
    case "approval": {
      const a = item.data;
      push(label);
      push(a.riskLevel ? `risk ${a.riskLevel}` : "");
      push(relTime(a.createdAt));
      break;
    }
    case "followup": {
      const f = item.data;
      push(label);
      push(f.source);
      push(relTime(f.receivedAt));
      break;
    }
    case "work": {
      const w = item.data;
      // Kind label, STATUS (the board column), then when. The engine
      // ("Voyager" / "GEPA") stays off the header and lives in Details, per
      // the readable-names rule.
      push(label);
      push(w.column);
      push(
        w.finishedAt
          ? `finished ${relTime(w.finishedAt)}`
          : w.startedAt
            ? `started ${relTime(w.startedAt)}`
            : w.scheduledFor
              ? `scheduled ${relTime(w.scheduledFor)}`
              : "",
      );
      push(w.durationMs ? formatDuration(w.durationMs) : "");
      break;
    }
    case "saved": {
      const s = item.data;
      push(label);
      push(s.kind);
      push(s.readingMinutes ? `${s.readingMinutes} min read` : "");
      push(`saved ${relTime(s.savedAt)}`);
      break;
    }
    case "artifact": {
      const a = item.data;
      push(label);
      push(a.kind);
      push(`built ${relTime(a.createdAt)}`);
      break;
    }
    case "activity": {
      const e = item.data;
      push(label);
      push(e.kind);
      push(e.future ? `in ${dayLabel(e.at).toLowerCase()}` : relTime(e.at));
      break;
    }
    case "surface": {
      const s = item.data;
      // The call log's own guard: every segment it could build restates the
      // title or is a constant. Kind label alone.
      if (s.surface === "calls") {
        push(label);
        break;
      }
      push(label);
      push(s.source);
      push(relTime(s.createdAt));
      if (typeof s.importance === "number" && s.importance >= 50) {
        push(`imp ${s.importance}`);
      }
      break;
    }
  }
  return parts.join(" · ");
}

function getViewerKey(item: DashboardItem): string {
  return `${item.kind}:${(item.data as { id: string }).id}`;
}

function getViewerTitle(item: DashboardItem): string {
  switch (item.kind) {
    case "pursuit": return item.data.title;
    case "todo": return item.data.title;
    case "event": return item.data.title;
    case "reflection": return item.data.title;
    case "approval": return item.data.title;
    case "followup": return item.data.subject ?? item.data.from;
    case "surface": return item.data.title;
    case "work": return item.data.title;
    case "saved": return item.data.title;
    case "artifact": return item.data.name;
    case "activity": return item.data.title;
    case "record": return item.data.title;
  }
}

function getViewerKindLabel(item: DashboardItem): string {
  switch (item.kind) {
    case "pursuit": return "Pursuit";
    case "todo": return "Todo";
    case "event": return "Calendar event";
    case "reflection": return "Reflection";
    case "approval": return "Approval";
    case "followup": return "Follow-up";
    case "surface": return surfaceKindLabel(item.data.surface);
    case "work": return "Agent work item";
    case "saved": return "Saved item";
    case "artifact": return "Made by Jarvis";
    case "activity": return "Activity event";
    // Go names the kind ("Memory", "Skill", …) in `subtitle`; Studio does not
    // keep a second copy of that mapping to drift out of sync.
    case "record": return item.data.subtitle || item.data.kind;
  }
}

// Surface items carry a free-form `surface` key. Titleize it for the
// kind label so an agent-invented surface still reads cleanly.
function surfaceKindLabel(surface: string): string {
  const t = surface.replace(/[-_]+/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
  return `${t} item`;
}

function ViewerBody({ item }: { item: DashboardItem }) {
  // ResponsiveModalBody is the scroll container; this wrapper only owns
  // the per-item enter/exit motion under AnimatePresence.
  return (
    <motion.div
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.18, ease: [0.2, 0.7, 0.2, 1] }}
      className="min-w-0 max-w-full"
    >
      <ViewerContent item={item} />
    </motion.div>
  );
}

// cronIDFromWorkItem returns the cron's UUID for a work item that maps to a
// triggerable cron (queued or done cron cards carry ids "cron-q-<uuid>" /
// "cron-d-<uuid>"), or "" for plans/workflows/skill runs that aren't crons.
// This is what lets the Agent Work detail offer "Run now" without a per-kind API.
function cronIDFromWorkItem(w: WorkItem): string {
  for (const prefix of ["cron-q-", "cron-d-"]) {
    if (w.id.startsWith(prefix)) return w.id.slice(prefix.length);
  }
  return "";
}

function ViewerActions({
  item,
  onResolved,
  onClose,
  onEditTodo,
}: {
  item: DashboardItem;
  onResolved?: (item: DashboardItem) => void;
  onClose?: () => void;
  onEditTodo?: (todo: Todo) => void;
}) {
  // Every dismiss/cancel/deny should close the modal immediately, not just
  // remove the row underneath it (which left the modal open over a now-empty
  // card and made the screen blink until the boss clicked away). resolveAndClose
  // removes the item AND closes the viewer in one go.
  const resolveAndClose = React.useCallback(() => {
    // onClose MUST run even if onResolved throws or no-ops for this kind —
    // closing the modal is the guarantee, removing the row is best-effort.
    try {
      if (onResolved) onResolved(item);
    } finally {
      if (onClose) onClose();
    }
  }, [item, onResolved, onClose]);
  // Every item gets a "Discuss with Jarvis" primary CTA. Kind-specific
  // secondary actions are preview-only "Open in <surface>" deep-links
  // (per the IA defrag), no inline approve/reject here anymore. The
  // one exception: follow-ups also get a "Dismiss" button so the boss
  // can drop a row without opening a chat for it — and dismissals are
  // durable (status='dismissed' is preserved by the connector poller's
  // ON CONFLICT DO NOTHING, so re-polled threads don't resurface).
  const router = useAppRouter();
  const [seeding, setSeeding] = React.useState(false);
  const [dismissing, setDismissing] = React.useState(false);
  const [cancelling, setCancelling] = React.useState(false);

  // ALL dismiss/cancel actions close the modal the INSTANT they're clicked
  // (optimistic) and fire their request in the background — the boss never
  // waits on the network, and the row is reconciled by realtime. This is the
  // fix for "I hit dismiss and the modal just sat there." resolveAndClose
  // removes the row (best-effort) AND closes the modal, guaranteed.
  function cancelWorkItem() {
    if (item.kind !== "work") return;
    const w = item.data;
    const rawId = w.id.replace(/^(plan|run)-/, "");
    setCancelling(true);
    resolveAndClose();
    void cancelWork({ kind: w.kind, id: rawId, sessionId: w.sessionId });
  }

  // Dismiss an awaiting approval-style work item (code proposal / trust)
  // surfaced on the Agent Work board — their own durable decide endpoints.
  function dismissWorkItem() {
    if (item.kind !== "work") return;
    const w = item.data;
    let url = "";
    let body: Record<string, string> = {};
    switch (w.kind) {
      case "code_proposal":
        url = `/api/voyager/code-proposals/${w.id.replace(/^code-/, "")}/decide`;
        body = { decision: "rejected" };
        break;
      case "trust":
        url = `/api/trust-contracts/${w.id.replace(/^trust-/, "")}/decide`;
        body = { decision: "denied" };
        break;
      default:
        return;
    }
    setDismissing(true);
    resolveAndClose();
    void authedFetch(url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
    });
  }

  function dismissFollowup() {
    if (item.kind !== "followup") return;
    const id = item.data.id;
    const origin = item.data.origin ?? "followup";
    setDismissing(true);
    resolveAndClose();
    void authedFetch("/api/followups/dismiss", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ id, origin }),
    });
  }

  function dismissCuriosity() {
    if (item.kind !== "approval" || item.data.kind !== "curiosity") return;
    const id = item.data.id;
    setDismissing(true);
    resolveAndClose();
    void authedFetch(`/api/curiosity/questions/${id}/decide`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ decision: "dismissed" }),
    });
  }

  function dismissCodeProposal() {
    if (item.kind !== "approval" || item.data.kind !== "code_proposal") return;
    const id = item.data.id;
    setDismissing(true);
    resolveAndClose();
    void authedFetch(`/api/voyager/code-proposals/${id}/decide`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ decision: "rejected" }),
    });
  }

  function dismissSurface() {
    if (item.kind !== "surface") return;
    const id = item.data.id;
    setDismissing(true);
    resolveAndClose();
    void authedFetch("/api/followups/dismiss", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ id, origin: "surface" }),
    });
  }

  // Folded "system" notes in the Activity feed carry a dismiss handle
  // (origin + id) mapping back to mem_surface_items, so they stay clearable
  // through the same endpoint without inventing a per-kind route.
  function dismissActivity() {
    if (item.kind !== "activity" || !item.data.dismiss) return;
    const { id, origin } = item.data.dismiss;
    setDismissing(true);
    resolveAndClose();
    void authedFetch("/api/followups/dismiss", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ id, origin }),
    });
  }

  async function discuss() {
    const id = (item.data as { id?: string }).id ?? "";
    // A record's OWN kind is what the seeded session needs in its turn-1
    // context block: "Kind: memory" tells Jarvis what he is looking at,
    // "Kind: record" tells him nothing. `record` is a rendering variant, not
    // a thing that exists in the database.
    const seedKind = item.kind === "record" ? item.data.kind : item.kind;
    setSeeding(true);
    try {
      const sessionId = await seedSession(seedKind, id, item.data);
      if (sessionId) {
        router.push(`/live?session=${encodeURIComponent(sessionId)}`);
      } else {
        // Seed failed - degrade to unseeded /live so the boss can still
        // start a chat. ObjectViewer will close as the route changes.
        router.push("/live");
      }
    } finally {
      setSeeding(false);
    }
  }

  function renderSecondary(): React.ReactNode {
    // Per the IA defrag (2026-05-16): dashboard rows for agent-originated
    // items are PREVIEW-ONLY. Every action button has been pulled in
    // favor of a single "Open in <canonical surface>" CTA so the boss
    // never has to choose between three places to act on the same item.
    //
    //   trust_*       → Settings > Trust (audit + pending), real-time
    //                   approval still lives inline in chat
    //   code_proposal → /lab Open issues
    //   activity (heartbeat finding) → /lab Open issues
    //
    // "Discuss with Jarvis" stays as the universal seeded-session path
    // for items that warrant a conversation (todos, follow-ups, saved,
    // pursuits, etc).
    if (item.kind === "approval" && item.data.kind.startsWith("trust_")) {
      return (
        <OpenInButton
          href="/settings?section=trust"
          label="Open in Approvals"
        />
      );
    }
    if (item.kind === "approval" && item.data.kind === "code_proposal") {
      // Dismiss sits next to "Open in Skills" so the boss can drop a proposal
      // in one tap without detouring through Lab. Same canonical decide
      // endpoint (status='rejected', durable on mem_code_proposals). The
      // proposal only re-surfaces if Voyager's source extractor re-detects
      // the same file-fight in a future session - a fresh signal, not a
      // resurrected row.
      // Order (left→right): destructive Dismiss is kept furthest from the
      // rightmost primary (Discuss) to avoid mis-taps; neutral "Open in Skills"
      // sits between them.
      return (
        <>
          <button
            type="button"
            onClick={dismissCodeProposal}
            disabled={dismissing}
            className="inline-flex h-10 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-[13px] font-medium text-foreground transition-colors hover:bg-accent disabled:opacity-60"
          >
            <X className={cn("size-3.5", dismissing && "animate-pulse")} aria-hidden />
            {dismissing ? "Dismissing..." : "Dismiss"}
          </button>
          <OpenInButton href="/skills" label="Open in Skills" />
        </>
      );
    }
    if (item.kind === "approval" && item.data.kind === "curiosity") {
      return (
        <button
          type="button"
          onClick={dismissCuriosity}
          disabled={dismissing}
          className="inline-flex h-10 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-[13px] font-medium text-foreground transition-colors hover:bg-accent disabled:opacity-60"
        >
          <X className={cn("size-3.5", dismissing && "animate-pulse")} aria-hidden />
          {dismissing ? "Dismissing..." : "Dismiss"}
        </button>
      );
    }
    if (item.kind === "record") {
      // A search hit that lives on some other page. Go supplies both the
      // destination and its label, so this stays one branch no matter how
      // many kinds the search learns; "Discuss with Jarvis" still trails as
      // the universal CTA. No link while the detail is in flight or failed -
      // an href we have not confirmed is a link to nowhere.
      if (!item.data.href || item.data.loading || item.data.failed) return null;
      return (
        <OpenInButton
          href={item.data.href}
          label={item.data.hrefLabel || "Open"}
        />
      );
    }
    if (item.kind === "artifact") {
      // Generated artifacts (apps, docs, dashboards Jarvis built) lead with
      // "Open" — it launches the live artifact wherever it lives. Discuss
      // still trails as the universal CTA so the boss can ask about it.
      return <ArtifactOpenButton artifact={item.data} onClose={onClose} />;
    }
    if (item.kind === "surface") {
      // Everything Jarvis surfaces (alerts, insights, digest, …) is
      // dismissable in one tap. Persistence is server-side (status =
      // 'dismissed' on mem_surface_items); the realtime publication keeps
      // the row gone across refresh / device.
      return (
        <button
          type="button"
          onClick={dismissSurface}
          disabled={dismissing}
          className="inline-flex h-10 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-[13px] font-medium text-foreground transition-colors hover:bg-accent disabled:opacity-60"
        >
          <X className={cn("size-3.5", dismissing && "animate-pulse")} aria-hidden />
          {dismissing ? "Dismissing..." : "Dismiss"}
        </button>
      );
    }
    if (item.kind === "activity") {
      // Folded operational "system" notes carry a dismiss handle → clear
      // them in one tap. Heartbeat findings have none → Open in Skills.
      if (item.data.dismiss) {
        return (
          <button
            type="button"
            onClick={dismissActivity}
            disabled={dismissing}
            className="inline-flex h-10 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-[13px] font-medium text-foreground transition-colors hover:bg-accent disabled:opacity-60"
          >
            <X className={cn("size-3.5", dismissing && "animate-pulse")} aria-hidden />
            {dismissing ? "Dismissing..." : "Dismiss"}
          </button>
        );
      }
      return <OpenInButton href="/skills" label="Open in Skills" />;
    }
    if (item.kind === "followup") {
      // Order (left→right): destructive Dismiss is kept furthest from the
      // rightmost primary (Discuss); the email's own action cluster (Draft /
      // Archive / Snooze) sits between them. Persistence is server-side
      // (status = 'dismissed' on mem_followups or mem_surface_items); the
      // poller re-poll path won't resurface it.
      return (
        <>
          <button
            type="button"
            onClick={dismissFollowup}
            disabled={dismissing}
            className="inline-flex h-10 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-[13px] font-medium text-foreground transition-colors hover:bg-accent disabled:opacity-60"
          >
            <X className={cn("size-3.5", dismissing && "animate-pulse")} aria-hidden />
            {dismissing ? "Dismissing..." : "Dismiss"}
          </button>
          <FollowupFooterActions followup={item.data} />
        </>
      );
    }
    if (item.kind === "event") {
      // RSVP buttons live in the footer per the attached design ("Are you
      // attending?" bar). Only render when the boss is an invitee (has a
      // self responseStatus) - organizer-owned events have no self slot
      // and accept/decline doesn't apply.
      const ev = item.data;
      if (!ev.responseStatus) return null;
      return <RsvpButtons event={ev} onResolved={onResolved} item={item} />;
    }
    if (item.kind === "todo") {
      return (
        <Button
          type="button"
          variant="outline"
          className="h-10 gap-1.5"
          onClick={() => {
            onEditTodo?.(item.data);
            onClose?.();
          }}
        >
          <Pencil className="size-3.5" aria-hidden />
          Edit
        </Button>
      );
    }
    if (item.kind === "work") {
      const w = item.data;
      // Approval-style awaiting work (code proposals, trust contracts) have their
      // own durable decide endpoints — drop them in one tap from the board, same
      // as their canonical surface, alongside an "Open in" deep-link.
      if (w.kind === "code_proposal") {
        // Destructive Dismiss leftmost, neutral "Open in Skills" between it and
        // the rightmost primary (Discuss).
        return (
          <>
            <button
              type="button"
              onClick={dismissWorkItem}
              disabled={dismissing}
              className="inline-flex h-10 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-[13px] font-medium text-foreground transition-colors hover:bg-accent disabled:opacity-60"
            >
              <X className={cn("size-3.5", dismissing && "animate-pulse")} aria-hidden />
              {dismissing ? "Dismissing..." : "Dismiss"}
            </button>
            <OpenInButton href="/skills" label="Open in Skills" />
          </>
        );
      }
      if (w.kind === "trust") {
        // Destructive Deny leftmost, neutral "Open in Approvals" between it and the
        // rightmost primary (Discuss).
        return (
          <>
            <button
              type="button"
              onClick={dismissWorkItem}
              disabled={dismissing}
       title="Deny this pending approval, the gated action won't run. The agent can re-request it later."
              className="inline-flex h-10 items-center gap-1.5 rounded-md border border-danger/40 bg-background px-3 text-[13px] font-medium text-danger transition-colors hover:bg-danger/10 disabled:opacity-60"
            >
              <X className={cn("size-3.5", dismissing && "animate-pulse")} aria-hidden />
              {dismissing ? "Denying..." : "Deny"}
            </button>
            <OpenInButton href="/settings?section=trust" label="Open in Approvals" />
          </>
        );
      }
      // Running or awaiting work the boss wants to kill: a stuck plan, an
      // in-flight cron/agent turn, a paused plan sitting in "awaiting". Stop
      // aborts the live turn, cancels the plan, and clears the card. Covers the
      // gap where the board had no way to cancel an active/awaiting item.
      const canCancel =
        (w.column === "running" || w.column === "awaiting") &&
        ["plan", "cron_run", "skill_run", "workflow"].includes(w.kind);
      if (canCancel) {
        return (
          <button
            type="button"
            onClick={cancelWorkItem}
            disabled={cancelling}
            title="Stop this item: aborts the running agent turn, cancels its plan, and clears the card."
            className="inline-flex h-10 items-center gap-1.5 rounded-md border border-danger/40 bg-background px-3 text-[13px] font-medium text-danger transition-colors hover:bg-danger/10 disabled:opacity-60"
          >
            <X className={cn("size-3.5", cancelling && "animate-pulse")} aria-hidden />
            {cancelling ? "Stopping..." : w.column === "awaiting" ? "Dismiss" : "Stop"}
          </button>
        );
      }
      // A scheduled (queued) job shouldn't be a read-only card — let the boss
      // fire it immediately. RunIndicator books a mem_runs row so the spinner
      // survives navigation/refresh, identical to the cron page's own Run now.
      const cronID = cronIDFromWorkItem(w);
      if (cronID) {
        return (
          <RunIndicator
            kind="cron"
            targetId={cronID}
            label="Run now"
            title="Fire this scheduled job immediately. The next regular run still happens on schedule. Progress survives navigation and refresh."
            showResult={false}
            onRun={async () => {
              // Close the modal immediately so the boss watches it move into
              // RUNNING on the board, rather than staring at the modal while the
              // (synchronous) run endpoint holds the request open. The run row is
              // booked server-side at fire time, so the board picks it up live.
              if (onClose) onClose();
              void triggerCron(cronID);
            }}
          />
        );
      }
      return null;
    }
    return null;
  }

  // Event modals own their entire footer (Are you attending? + RSVP
  // cluster) per the design ref - no Discuss CTA. Every other kind
  // keeps Discuss as the universal trailing primary.
  if (item.kind === "event") {
    return <>{renderSecondary()}</>;
  }
  // §7 footer anatomy: one LEFT cluster (secondary + destructive, however many
  // this kind has) and exactly ONE primary on the right. The wrapper is what
  // makes `justify-between` mean that rather than "spread three buttons".
  // It renders even when empty so the primary stays pinned right.
  return (
    <>
      {/* §7's left cluster on lg+. Below lg ResponsiveModal's footer dissolves
          this wrapper and stacks the buttons full width - the primitive owns
          that, so nothing here needs a breakpoint. */}
      <div className="flex min-w-0 flex-wrap items-center gap-2">{renderSecondary()}</div>
      <button
        type="button"
        onClick={discuss}
        disabled={seeding}
        className="inline-flex h-10 items-center gap-1.5 rounded-md bg-foreground px-4 text-sm font-medium text-background transition-all hover:opacity-90 disabled:opacity-60"
      >
        <Sparkles className={cn("size-3.5", seeding && "animate-pulse")} aria-hidden />
        Discuss with Jarvis
        <ArrowRight className="size-3.5" aria-hidden />
      </button>
    </>
  );
}

function FollowupFooterActions({ followup }: { followup: FollowUp }) {
  const actions = followup.actions ?? [];
  const { running, runningLabel } = useSurfaceActionActivity(followup.id);
  const [firing, setFiring] = React.useState<string | null>(null);

  if (actions.length === 0) return null;

  return (
    <div className="flex min-w-0 flex-wrap items-center gap-2">
      {actions.map((action) => {
        const variant =
          action.style === "primary"
            ? "default"
            : action.style === "danger"
              ? "destructive"
              : "outline";
        // Spin ONLY for this action: while we POST it (firing) or while the
        // in-flight run is this action (runningLabel match). A send_reply run no
        // longer spins Draft/Archive/Snooze.
        const pending = firing === action.id || (running && runningLabel === action.label);
        return (
          <Button
            key={action.id}
            type="button"
            size="sm"
            variant={variant}
            disabled={pending}
            className="h-10 gap-1.5"
            onClick={async () => {
              setFiring(action.id);
              try {
                await postSurfaceAction(followup.id, action.id);
              } finally {
                setFiring(null);
              }
            }}
          >
            {pending ? <Spinner className="size-3.5" aria-hidden /> : null}
            {pending && action.id === "draft_reply" ? "Drafting..." : action.label}
          </Button>
        );
      })}
    </div>
  );
}

// openArtifact - routes a generated artifact (mem_artifacts row) to wherever it
// actually lives, off its storage fields. Priority: a project/app on a bridge
// opens live in the canvas; a hosted/object-store URL opens directly; a GitHub
// repo opens on GitHub; everything else falls back to the canvas Library. No
// per-kind table — the storage shape is the whole contract.
function isURL(s: string): boolean {
  return /^https?:\/\//i.test(s);
}

async function openArtifact(
  a: Artifact,
  router: ReturnType<typeof useAppRouter>,
  onClose?: () => void,
) {
  // A canvas project is the high-value case — reopen it running, not a snapshot.
  if (a.kind === "project" && a.storagePath) {
    await canvasProjectActivate({ project_path: a.storagePath });
    router.push("/live");
    onClose?.();
    return;
  }
  // A hosted artifact (image on R2, a deployed site, a doc URL) opens directly.
  if (a.storageKind === "object_store" && a.storagePath && isURL(a.storagePath)) {
    window.open(a.storagePath, "_blank", "noopener,noreferrer");
    return;
  }
  if (a.githubUrl) {
    window.open(a.githubUrl, "_blank", "noopener,noreferrer");
    return;
  }
  // A generated document (doc/sheet/slides/report) opens straight into its own
  // canvas tab, focused and previewed — not dumped at the Library root. We hand
  // the DocMeta to the canvas via the pending-doc handoff, then route to /live.
  if (a.kind === "document" && a.storagePath) {
    stashPendingDoc({
      id: a.storagePath,
      filename: a.name,
      format: a.format ?? "",
      path: a.storagePath,
      bytes: a.bytes,
      markdown: a.markdown,
      pdfPath: a.pdfPath,
      htmlPath: a.htmlPath,
    });
    // If the chat this doc was made in still exists, reopen THAT chat (full
    // history) with the file open. If it was deleted, start a fresh chat with
    // the doc dropped in as context (Jarvis auto-greets via the seeded kick) so
    // the boss can pick up where the work left off without the old session.
    if (a.sessionAlive && a.sourceSessionId) {
      router.push(`/live?session=${encodeURIComponent(a.sourceSessionId)}`);
    } else {
      const seeded = await seedSession("artifact", a.id, a);
      router.push(seeded ? `/live?session=${encodeURIComponent(seeded)}` : "/live");
    }
    onClose?.();
    return;
  }
  // Anything else (a filesystem dataset/other) lives in the canvas Library.
  router.push("/live");
  onClose?.();
}

// ArtifactOpenButton - the prominent "Open" CTA for a generated artifact
// (app / doc / dashboard Jarvis built). Launches the live artifact; sits
// alongside the universal "Discuss with Jarvis" trailing button.
function ArtifactOpenButton({ artifact, onClose }: { artifact: Artifact; onClose?: () => void }) {
  const router = useAppRouter();
  const [opening, setOpening] = React.useState(false);
  return (
    <button
      type="button"
      onClick={async () => {
        setOpening(true);
        try {
          await openArtifact(artifact, router, onClose);
        } finally {
          setOpening(false);
        }
      }}
      disabled={opening}
      className="inline-flex h-10 items-center gap-1.5 rounded-md border border-tier-procedural/40 bg-tier-procedural/10 px-3 text-[13px] font-medium text-tier-procedural transition-colors hover:bg-tier-procedural/20 disabled:opacity-60"
    >
      <ExternalLink className={cn("size-3.5", opening && "animate-pulse")} aria-hidden />
      {opening ? "Opening..." : "Open"}
    </button>
  );
}

// OpenInButton - the canonical "preview-only" CTA for agent-originated
// dashboard rows. Routes the user to the canonical action surface so
// dashboard stays a viewing surface and Lab/Settings/Chat stay the
// places where work actually gets done. Closes the modal on click via
// the parent's onClose by relying on Next router; the modal's
// onOpenChange isn't called explicitly because navigating away from
// the dashboard URL closes it naturally.
function OpenInButton({ href, label }: { href: string; label: string }) {
  const router = useAppRouter();
  return (
    <button
      type="button"
      onClick={() => router.push(href)}
      className="inline-flex h-10 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-[13px] font-medium text-foreground transition-colors hover:bg-accent"
    >
      <ArrowRight className="size-3.5" aria-hidden />
      {label}
    </button>
  );
}

// RsvpButtons - "Are you attending?" footer block matching the attached
// design. Three buttons (Yes / Maybe / No), the current response is
// highlighted, in-flight state pulses. Routes through /api/calendar/
// events/:id/respond which itself wraps in runs.Track so the spinner
// survives navigation/refresh.
function RsvpButtons({
  event,
  onResolved,
  item,
}: {
  event: CalendarEvent;
  onResolved?: (item: DashboardItem) => void;
  item: DashboardItem;
}) {
  const [pending, setPending] = React.useState<string | null>(null);
  const [current, setCurrent] = React.useState<string | undefined>(
    event.responseStatus,
  );

  const respond = React.useCallback(
    async (response: "accepted" | "tentative" | "declined") => {
      if (pending) return;
      setPending(response);
      try {
        const res = await authedFetch(
          `/api/calendar/events/${encodeURIComponent(event.id)}/respond`,
          {
            method: "POST",
            headers: { "content-type": "application/json" },
            body: JSON.stringify({ response }),
          },
        );
        if (res.ok) {
          setCurrent(response);
          if (onResolved) onResolved(item);
        }
      } finally {
        setPending(null);
      }
    },
    [pending, event.id, onResolved, item],
  );

  // RsvpButtons renders DIRECTLY into the modal's footer slot. The
  // footer container already has `flex items-center justify-between`
  // (we override `justify-end` via footerClassName at the modal site)
  // plus the lavender bg. The label sits on the left, the three
  // buttons cluster on the right.
  return (
    <>
      <span className="text-[14px] text-foreground/90">
        Are you attending?
      </span>
      <div className="flex items-center gap-2">
        <RsvpChoice
          label="Yes"
          active={current === "accepted"}
          pending={pending === "accepted"}
          tone="accepted"
          onClick={() => respond("accepted")}
          disabled={!!pending}
        />
        <RsvpChoice
          label="Maybe"
          active={current === "tentative"}
          pending={pending === "tentative"}
          tone="tentative"
          onClick={() => respond("tentative")}
          disabled={!!pending}
        />
        <RsvpChoice
          label="No"
          active={current === "declined"}
          pending={pending === "declined"}
          tone="declined"
          onClick={() => respond("declined")}
          disabled={!!pending}
        />
      </div>
    </>
  );
}

// RsvpChoice - the three buttons from the design ref. Yes is always
// green-filled (matches design where "Yes" appears as a positive CTA
// regardless of selection state); Maybe + No are white pills with
// border. When ACTIVE (current response), the corresponding outlined
// button fills with its tone color too.
function RsvpChoice({
  label,
  active,
  pending,
  tone,
  onClick,
  disabled,
}: {
  label: string;
  active: boolean;
  pending: boolean;
  tone: "accepted" | "tentative" | "declined";
  onClick: () => void;
  disabled: boolean;
}) {
  // Yes is always green (per design ref - it's the affordance, not just
  // a state indicator). Maybe + No are white-pill outlined and only
  // fill when the boss has selected them.
  const isYes = tone === "accepted";
  const filled =
    isYes || active
      ? {
          accepted: "bg-emerald-500 text-white border-emerald-500 hover:bg-emerald-600",
          tentative: "bg-amber-500 text-white border-amber-500 hover:bg-amber-600",
          declined: "bg-rose-500 text-white border-rose-500 hover:bg-rose-600",
        }[tone]
      : "bg-background text-foreground border-border hover:bg-accent";
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={cn(
        "inline-flex h-9 items-center justify-center rounded-md border px-4 text-[14px] font-medium transition-colors disabled:opacity-60",
        filled,
        pending && "animate-pulse",
      )}
    >
      {label}
    </button>
  );
}

// ── Per-kind body content ─────────────────────────────────────────────────

function ViewerContent({ item }: { item: DashboardItem }) {
  switch (item.kind) {
    case "pursuit": return <PursuitBody p={item.data} />;
    case "todo": return <TodoBody t={item.data} />;
    case "event": return <EventBody e={item.data} />;
    case "reflection": return <ReflectionBody r={item.data} />;
    case "approval": return <ApprovalBody a={item.data} />;
    case "followup": return <FollowUpBody f={item.data} />;
    case "surface": return <SurfaceBody item={item.data} />;
    case "work": return <WorkBody w={item.data} />;
    case "saved": return <SavedBody s={item.data} />;
    case "artifact": return <ArtifactBody a={item.data} />;
    case "activity": return <ActivityBody e={item.data} />;
    case "record": return <RecordBody r={item.data} />;
  }
}

// ── Record ────────────────────────────────────────────────────────────────
/* RecordBody - anything the global search found that the dashboard does not
 * already hold hydrated: a memory, a skill, an automation, a session, a
 * lesson, a prediction, an observation.
 *
 * There is deliberately NO per-kind branch here, and there never should be.
 * Go's /api/object decides what this object's fields are called and where
 * "Open in …" points; Studio renders whatever it is handed. That is what makes
 * a ninth searchable table open in this sheet on the day Core learns to search
 * it, with zero changes to this file — the alternative is eight bodies here
 * that drift out of sync with eight queries there.
 *
 * `loading` is the optimistic state: the sheet opens instantly from the search
 * hit (title + meta) while the detail is still in flight, so a tap never buys
 * a spinner before a modal. `failed` is NOT the same as "no detail" and must
 * never render as a clean empty body - a record that could not be fetched says
 * so. */
function RecordBody({ r }: { r: RecordDetail }) {
  const loading = !!r.loading;
  const failed = !!r.failed;
  const hasBody = !!r.body?.trim();
  const entries = (r.fields ?? [])
    .filter((f) => f.value?.trim())
    .map((f) => ({
      k: f.label,
      v: f.mono ? <span className="font-mono text-[12px]">{f.value}</span> : f.value,
    }));

  return (
    <ViewerSections>
      {failed ? (
        <QuietNote>
          I could not load this one. It may have been removed since the search
          found it, or Core is not answering right now.
        </QuietNote>
      ) : null}
      {hasBody ? (
        <ModalSection label="Detail">
          <ModalPre>{r.body.trim()}</ModalPre>
        </ModalSection>
      ) : null}
      {entries.length > 0 ? (
        <ModalSection label="Details">
          <ModalDl entries={entries} />
        </ModalSection>
      ) : null}
      {!failed && !hasBody && entries.length === 0 ? (
        <QuietNote>
          {loading ? "Opening…" : "Nothing else is recorded against this one."}
        </QuietNote>
      ) : null}
    </ViewerSections>
  );
}

// ── Pursuit ───────────────────────────────────────────────────────────────
function PursuitBody({ p }: { p: Pursuit }) {
  // Cadence / streak / status moved into the header context line — they were
  // the chip row that used to sit on top of this body restating it.
  return (
    <ViewerSections>
      {p.progress ? (
        <ModalSection
          label="Progress"
          meta={`${p.progress.current}/${p.progress.target} ${p.progress.unit ?? ""}`}
        >
          <div className="space-y-2">
            <div className="h-1.5 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-brand"
                style={{ width: `${Math.min(100, Math.round((p.progress.current / p.progress.target) * 100))}%` }}
              />
            </div>
            <p className="text-quiet">
              {p.progress.current} of {p.progress.target} {p.progress.unit ?? "units"} so far.
            </p>
          </div>
        </ModalSection>
      ) : (
        <ModalSection label="Today">
          <p>
            {p.doneToday
              ? `Checked in today${p.doneAt ? ` at ${clockTime(p.doneAt)}` : ""}.`
              : "Not yet completed today."}
            {p.streakDays
              ? ` Current streak is ${p.streakDays} day${p.streakDays === 1 ? "" : "s"}.`
              : ""}
          </p>
        </ModalSection>
      )}
      {p.createdAt ? (
        <ModalSection label="Created">
          <span suppressHydrationWarning>{relTime(p.createdAt)}</span>
        </ModalSection>
      ) : null}
    </ViewerSections>
  );
}

/* ViewerSections - the modal body's section column.
 *
 * Sections are hairline-separated LABELLED ROWS now (§7), so the body must be
 * a plain stack with no `space-y-*`: a gap between two hairline-separated rows
 * reads as two disconnected fragments. Every body routes through this so the
 * rhythm is identical across all eleven kinds, and `first:border-t-0` in
 * ModalSection actually fires (it only works when the section is the first
 * child of its container). */
function ViewerSections({ children }: { children: React.ReactNode }) {
  return <div className="min-w-0 max-w-full">{children}</div>;
}

/* QuietNote - the honest one-liner an empty body gets instead of an empty
 * section (§7 "only sections with data render", §1.5 "descriptions stay on
 * empty states"). */
function QuietNote({ children }: { children: React.ReactNode }) {
  return <p className="py-1 text-[13px] leading-relaxed text-quiet">{children}</p>;
}

// ── Todo ──────────────────────────────────────────────────────────────────
function TodoBody({ t }: { t: Todo }) {
  // Priority / source / due are in the header context line; the title is the
  // header's title. What is left is the note, and only when there is one.
  const agentNote = t.source === "agent";
  if (!t.body?.trim() && !agentNote) {
    return <QuietNote>Nothing else recorded on this one.</QuietNote>;
  }
  return (
    <ViewerSections>
      {t.body?.trim() ? (
        <ModalSection label="Note">
          <p className="whitespace-pre-wrap break-words">{t.body}</p>
        </ModalSection>
      ) : null}
      {agentNote ? (
        <ModalSection label="Why">
          <p>Jarvis created this todo from your recent activity. Discuss to ask why.</p>
        </ModalSection>
      ) : null}
    </ViewerSections>
  );
}

function todoDueLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return dayLabel(iso);
  const relative = dayLabel(iso);
  if (["Today", "Tomorrow", "Yesterday"].includes(relative)) return relative;
  return d.toLocaleDateString([], {
    weekday: "short",
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

// ── CalendarEvent ─────────────────────────────────────────────────────────
function EventBody({ e }: { e: CalendarEvent }) {
  const prep = e.prep ?? [];
  const openPrep = prep.filter((p) => !p.done);
  const attendees = e.attendees ?? [];
  const counts = attendees.reduce(
    (acc, a) => {
      switch (a.responseStatus) {
        case "accepted":
          acc.yes++;
          break;
        case "tentative":
          acc.maybe++;
          break;
        case "declined":
          acc.no++;
          break;
        default:
          acc.pending++;
      }
      return acc;
    },
    { yes: 0, maybe: 0, no: 0, pending: 0 },
  );
  const attendeeSummary = [
    counts.yes ? `${counts.yes} yes` : "",
    counts.maybe ? `${counts.maybe} maybe` : "",
    counts.no ? `${counts.no} decline` : "",
    counts.pending ? `${counts.pending} pending` : "",
  ]
    .filter(Boolean)
    .join(", ");

  const recurrenceLabel = readableRecurrence(e.recurrence);
  const videoEntry =
    e.conference?.entryPoints?.find((ep) => ep.type === "video") ?? null;
  const videoUrl = videoEntry?.uri ?? e.hangoutLink ?? null;
  const videoLabel =
    videoEntry?.label ??
    (e.conference?.solutionName
      ? `Join ${e.conference.solutionName}`
      : "Join video call");

  // Per design ref: rows are generously spaced with consistent icon
  // gutter, plain sans-serif text, links in blue. No bg, no borders on
  // individual rows. Header (date/duration) lives in EventHeader and is
  // intentionally not re-rendered here.
  return (
    <div className="space-y-4 pt-4">
      {recurrenceLabel ? (
        <EventMetaRow icon={Repeat}>{recurrenceLabel}</EventMetaRow>
      ) : null}
      {e.location ? (
        <EventMetaRow icon={MapPin}>
          {/* Location is ALWAYS clickable. Google Maps' search-by-text
              endpoint handles both real addresses ("450 N State St,
              Chicago") and free-form room names ("Running with Scissors –
              Meeting Room") - the latter just runs a text search and
              shows the best match, which is harmless when there is none.
              Cheaper than parsing for address-ness and missing edge cases. */}
          <a
            href={`https://www.google.com/maps/search/?api=1&query=${encodeURIComponent(e.location)}`}
            target="_blank"
            rel="noreferrer noopener"
            className="inline-flex items-baseline gap-1 break-words text-foreground hover:text-info hover:underline"
          >
            {e.location}
          </a>
        </EventMetaRow>
      ) : null}
      {videoUrl ? (
        <EventMetaRow icon={Video}>
          <a
            href={videoUrl}
            target="_blank"
            rel="noreferrer noopener"
            className="inline-flex items-baseline gap-1 break-all text-[15px] font-medium text-info hover:underline"
          >
            {videoLabel}
            <ExternalLink className="size-3 shrink-0 self-center" aria-hidden />
          </a>
        </EventMetaRow>
      ) : null}

      {attendees.length > 0 ? (
        <EventMetaRow icon={Users}>
          <div className="space-y-2.5">
            <p className="text-foreground">
              {attendeeSummary || `${attendees.length} invited`}
            </p>
            <ul className="flex flex-wrap gap-2">
              {attendees.map((a, idx) => (
                <li
                  key={a.email + idx}
                  className="inline-flex items-center gap-2 rounded-full bg-muted/60 py-1 pl-1 pr-3 text-[13px] dark:bg-muted/40"
                >
                  <AttendeeAvatar name={a.displayName ?? a.email} index={idx} />
                  <span className="max-w-[200px] truncate font-medium text-foreground">
                    {a.displayName ?? a.email}
                  </span>
                  <AttendeeStatusIcon status={a.responseStatus} />
                </li>
              ))}
            </ul>
          </div>
        </EventMetaRow>
      ) : null}

      {e.description ? (
        <EventMetaRow icon={AlignLeft}>
          <ExpandableDescription text={e.description} />
        </EventMetaRow>
      ) : null}

      {prep.length > 0 ? (
        <ModalSection label="Prep" meta={`${openPrep.length}/${prep.length} open`}>
          <ul className="space-y-2">
            {prep.map((p) => (
              <li key={p.id} className="flex items-start gap-2">
                {p.done ? (
                  <CheckCircle2
                    className="mt-0.5 size-4 shrink-0 text-success"
                    aria-hidden
                  />
                ) : (
                  <Circle
                    className="mt-0.5 size-4 shrink-0 text-muted-foreground"
                    aria-hidden
                  />
                )}
                <div className="min-w-0 flex-1">
                  <p
                    className={cn(
                      "text-[13px]",
                      p.done
                        ? "text-muted-foreground line-through"
                        : "text-foreground",
                    )}
                  >
                    {p.label}
                  </p>
                  {p.rationale ? (
                    <p className="mt-0.5 text-[11px] italic text-muted-foreground">
                      {p.rationale}
                    </p>
                  ) : null}
                </div>
              </li>
            ))}
          </ul>
        </ModalSection>
      ) : null}
    </div>
  );
}

// EventMetaRow: icon + content row used for every body row in the event
// modal. Larger text (15px) + larger icons (size-[18px]) and a wider
// gutter than the generic ModalSection to match the design's airy feel.
function EventMetaRow({
  icon: Icon,
  children,
}: {
  icon: React.ComponentType<{ className?: string }>;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-start gap-3 text-[15px] text-foreground">
      <Icon
        className="mt-0.5 size-[18px] shrink-0 text-muted-foreground"
        aria-hidden
      />
      <div className="min-w-0 flex-1 break-words leading-relaxed">{children}</div>
    </div>
  );
}

// AttendeeAvatar: 26px filled circle with the first letter. Color
// derived from a stable hash of the name so the visual rhythm reads
// "different people" the way photo avatars would in the design ref.
// Photo fetching would require CSP + per-row HTTP to Google; the
// colored initial circle is the agreed substitute.
function AttendeeAvatar({ name, index }: { name: string; index: number }) {
  const initial = (name?.trim()?.[0] ?? "?").toUpperCase();
  const tone = avatarTones[(hashStr(name) + index) % avatarTones.length];
  return (
    <span
      className={cn(
        "inline-flex size-[26px] shrink-0 items-center justify-center rounded-full text-[11px] font-semibold uppercase text-white",
        tone,
      )}
    >
      {initial}
    </span>
  );
}

const avatarTones = [
  "bg-rose-500",
  "bg-orange-500",
  "bg-amber-500",
  "bg-emerald-500",
  "bg-teal-500",
  "bg-sky-500",
  "bg-indigo-500",
  "bg-violet-500",
  "bg-fuchsia-500",
  "bg-pink-500",
];

function hashStr(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) {
    h = (h * 31 + s.charCodeAt(i)) >>> 0;
  }
  return h;
}

// AttendeeStatusIcon: filled circular badge appended to each chip per
// the design ref - green ✓ (accepted), orange ? (tentative),
// red × (declined). Returns null for needs-action so unanswered
// invitees render as a bare chip (matches the design's two unstyled
// rows for the four "yes" Floyd/Jane/Ronald/Annette chips).
function AttendeeStatusIcon({ status }: { status?: string }) {
  switch (status) {
    case "accepted":
      return (
        <span
          aria-label="accepted"
          className="inline-flex size-[18px] shrink-0 items-center justify-center rounded-full bg-emerald-500 text-white"
        >
          <CheckCircle2 className="size-3" strokeWidth={3} aria-hidden />
        </span>
      );
    case "tentative":
      return (
        <span
          aria-label="tentative"
          className="inline-flex size-[18px] shrink-0 items-center justify-center rounded-full bg-amber-500 text-[11px] font-bold text-white"
        >
          ?
        </span>
      );
    case "declined":
      return (
        <span
          aria-label="declined"
          className="inline-flex size-[18px] shrink-0 items-center justify-center rounded-full bg-rose-500 text-white"
        >
          <X className="size-3" strokeWidth={3} aria-hidden />
        </span>
      );
    default:
      return null;
  }
}

// ExpandableDescription: shows the first ~220 chars with a "Show all"
// blue link affordance per the design. No bullet prefix - the parent
// EventMetaRow's AlignLeft icon already serves that purpose.
function ExpandableDescription({ text }: { text: string }) {
  const [expanded, setExpanded] = React.useState(false);
  const collapseLimit = 220;
  const needsExpand = text.length > collapseLimit;
  const shown = expanded || !needsExpand ? text : text.slice(0, collapseLimit) + "…";
  return (
    <div>
      <p className="whitespace-pre-wrap break-words text-foreground">{shown}</p>
      {needsExpand && !expanded ? (
        <button
          type="button"
          onClick={() => setExpanded(true)}
          className="mt-1.5 text-[14px] font-medium text-info hover:underline"
        >
          Show all
        </button>
      ) : null}
    </div>
  );
}

// readableRecurrence: tries to surface a one-line description of a Google
// RRULE blob. The native sync stores RRULE strings verbatim
// (e.g. "RRULE:FREQ=MONTHLY;BYDAY=1TH"); we render a best-effort
// English label. When parsing fails we fall back to "Recurring event"
// so the row stays informative without leaking RRULE syntax.
function readableRecurrence(rec?: string[]): string | null {
  if (!rec || rec.length === 0) return null;
  const rule = rec.find((r) => r.startsWith("RRULE:")) ?? rec[0];
  if (!rule) return null;
  const body = rule.replace(/^RRULE:/, "");
  const parts = Object.fromEntries(
    body
      .split(";")
      .map((seg) => seg.split("="))
      .filter((kv) => kv.length === 2)
      .map(([k, v]) => [k.toUpperCase(), v]),
  ) as Record<string, string>;
  const freq = parts.FREQ;
  const interval = Number(parts.INTERVAL ?? "1");
  const byday = parts.BYDAY;
  const days: Record<string, string> = {
    MO: "Mon",
    TU: "Tue",
    WE: "Wed",
    TH: "Thu",
    FR: "Fri",
    SA: "Sat",
    SU: "Sun",
  };
  if (freq === "DAILY") {
    return interval > 1 ? `Every ${interval} days` : "Daily";
  }
  if (freq === "WEEKLY") {
    if (byday) {
      const labels = byday.split(",").map((d) => days[d] ?? d);
      const prefix = interval > 1 ? `Every ${interval} weeks` : "Weekly";
      return `${prefix} on ${labels.join(", ")}`;
    }
    return interval > 1 ? `Every ${interval} weeks` : "Weekly";
  }
  if (freq === "MONTHLY") {
    if (byday) {
      // "1TH" → first Thursday, "-1FR" → last Friday
      const match = byday.match(/^(-?\d+)([A-Z]{2})$/);
      if (match) {
        const ord = Number(match[1]);
        const day = days[match[2]] ?? match[2];
        const ordLabel =
          ord === -1
            ? "last"
            : ord === 1
              ? "first"
              : ord === 2
                ? "second"
                : ord === 3
                  ? "third"
                  : ord === 4
                    ? "fourth"
                    : `${ord}th`;
        const prefix = interval > 1 ? `Every ${interval} months` : "Monthly";
        return `${prefix} on the ${ordLabel} ${day}`;
      }
    }
    return interval > 1 ? `Every ${interval} months` : "Monthly";
  }
  if (freq === "YEARLY") {
    return interval > 1 ? `Every ${interval} years` : "Yearly";
  }
  return "Recurring event";
}

// ── Reflection ────────────────────────────────────────────────────────────
function ReflectionBody({ r }: { r: Reflection }) {
  // A reflection is Jarvis thinking out loud, so it takes the voice face and
  // no chrome at all: no chip row (the header context line carries the source
  // count and the time) and no labelled section around a single paragraph.
  if (!r.body?.trim()) return <QuietNote>He hasn&apos;t written this one up yet.</QuietNote>;
  return (
    <p className="min-w-0 whitespace-pre-wrap break-words font-voice text-[15.5px] leading-[1.55] text-foreground">
      {r.body}
    </p>
  );
}

// ── Approval ──────────────────────────────────────────────────────────────
function ApprovalBody({ a }: { a: Approval }) {
  // Kind / time / risk are in the header context line now.
  const empty = !a.preview && !a.rationale && !a.toolCall && !a.diff && !a.question;
  if (empty) return <QuietNote>He hasn&apos;t said any more about this one yet.</QuietNote>;
  return (
    <ViewerSections>
      {/* What will happen, in the gate's plain words. Leads, because it's the
          thing the boss is actually deciding on — so it is voice, unlabelled,
          above the labelled rows rather than boxed in one. */}
      {a.preview ? (
        <p className="mb-3 min-w-0 whitespace-pre-wrap break-words font-voice text-[15.5px] leading-[1.55] text-foreground">
          {a.preview}
        </p>
      ) : null}

      {/* Why the gate fired. A footnote when a preview is carrying the
          explanation, the lead when it's all we have (code proposals and
          curiosity questions have no preview). */}
      {a.rationale ? (
        a.preview ? (
          <ModalSection label="Why">
            <p className="break-words">{a.rationale}</p>
          </ModalSection>
        ) : (
          <p className="mb-3 min-w-0 whitespace-pre-wrap break-words font-voice text-[15.5px] leading-[1.55] text-foreground">
            {a.rationale}
          </p>
        )
      ) : null}

      {/* Long strings in the args (a blog post, an email body, a document)
          surface as readable markdown above the JSON. Generic — the payload's
          shape decides, not the tool's name. */}
      {a.toolCall ? (
        <ModalPayload value={a.toolCall.args} meta={a.toolCall.name} />
      ) : null}

      {a.diff ? (
        // The hand-rolled per-line tinting that used to live here is now the
        // shared `<Inset variant="diff">` (same `diffLineClass` the chat's
        // tool cards use) — one tinting rule for every diff in Studio.
        <ModalSection label="Patch" meta={a.filePath ?? undefined}>
          <Inset variant="diff" text={a.diff} />
        </ModalSection>
      ) : null}

      {a.question ? (
        <ModalSection label="Jarvis asks">
          <p className="break-words">{a.question}</p>
          {a.context ? (
            // whitespace-pre-line so the multi-line diagnosis (what it does /
            // what it was doing / the real error / suggested fix) keeps its
            // line breaks instead of collapsing into a run-on.
            <p className="mt-2 whitespace-pre-line break-words text-[12px] leading-relaxed text-quiet">
              {a.context}
            </p>
          ) : null}
        </ModalSection>
      ) : null}
    </ViewerSections>
  );
}

// ── FollowUp ──────────────────────────────────────────────────────────────
function FollowUpBody({ f }: { f: FollowUp }) {
  // Triage chips. classification / intent / mode are separate metadata axes
  // that often overlap — intent "needs reply" + mode "reply" are the SAME
  // thing shown twice. dedupeChips collapses them: it drops any value
  // contained in a longer sibling (so "reply" disappears next to "needs
  // reply") and any exact duplicate.
  const triageChips = dedupeChips([
    metaStr(f.metadata, "classification", "category"),
    metaStr(f.metadata, "intent"),
    metaStr(f.metadata, "mode", "action"),
  ]);

  // The OFFICIAL time the email landed in the real inbox (the triage skill
  // captures the message's own date into metadata.received_at) — NOT when we
  // found/surfaced it. Fall back to the found-time only when it's missing.
  const receivedReal = metaStr(f.metadata, "received_at", "date", "received");

  // The full email is fetched lazily on open (nothing is loaded at poll
  // time). Connector-poll rows already carry plain text in f.body; surface
  // rows arrive with only a summary and pull the real email here.
  const { html, text, attachments, loading, error } = useFollowupMessage(f);
  const richHtml = (html ?? f.html ?? "").trim();
  // f.body is the real plain-text email ONLY on connector-poll rows. On
  // surface rows f.body is the list-chip subtext ("mr khaya • action likely
  // needed…") — NOT the email — so we never render it as the message body;
  // the real email arrives via the lazy fetch (text/html). This is what kept
  // showing the chip summary inside the opened modal.
  const pollBody = f.origin === "surface" ? "" : (f.body ?? "").trim();
  const plain = (text ?? "").trim() || pollBody;
  const actionActivity = useSurfaceActionActivity(f.id);

  // Once a reply is sent, the editable draft box becomes a read-only "Response".
  // f.sentReply is the durable server-persisted source (survives refresh/device);
  // optimisticSent flips the in-modal view instantly on a successful send POST.
  const [optimisticSent, setOptimisticSent] = React.useState("");
  const sentReply = (f.sentReply ?? "").trim() || optimisticSent.trim();

  return (
    <ViewerSections>
      {/* Account + triage read as WORDS now, not a row of bordered pills, and
          the source/time segments moved to the header context line so this
          line no longer restates it. The received date stays because the modal
          deliberately shows the OFFICIAL full date (received_at from the real
          inbox), never the listing's relative "Nd ago". */}
      <ModalChips className="mb-3">
        {f.account ? <span key="account">{f.account}</span> : null}
        {triageChips.map((c) => (
          <span key={c}>{c}</span>
        ))}
        <span key="when" suppressHydrationWarning>
          {receivedReal || fullDateTime(f.receivedAt)}
        </span>
      </ModalChips>

      {/* From. The SUBJECT is the modal's title, so repeating it here would be
          the second title §1.3 forbids — it is gone from the body. */}
      {f.from?.trim() ? (
        <ModalSection label="From">
          <span className="break-words font-medium text-foreground">{f.from}</span>
        </ModalSection>
      ) : null}

      {/* Context (summary) - ABOVE the email. Stays silent when there's no
          triage summary (raw poll rows). */}
      {f.summary?.trim() ? (
        <ModalSection label="Context">
          <ModalPre>{f.summary.trim()}</ModalPre>
        </ModalSection>
      ) : null}

      {sentReply ? (
        <SentReplySection text={sentReply} source={f.source} />
      ) : (
        <DraftReplyPanel
          itemId={f.id}
          draft={f.draft}
          streamedText={actionActivity.text}
          running={actionActivity.running}
          error={actionActivity.error}
          onSent={(t) => setOptimisticSent(t)}
        />
      )}

      {/* Message - the real email, rendered as HTML when available. */}
      <ModalSection
        label="Message"
        meta={
          f.threadUrl ? (
            <ModalUrl href={f.threadUrl} icon={<ExternalLink className="size-3" aria-hidden />}>
              open in {f.source}
            </ModalUrl>
          ) : null
        }
      >
        {richHtml ? (
          <ModalHtml html={richHtml} />
        ) : plain ? (
          <ModalPre>{plain}</ModalPre>
        ) : loading ? (
          <MessageSkeleton />
        ) : attachments.length > 0 ? (
          // No text body, but the email carries attachments - say so plainly
          // instead of falling back to the stale list subtext.
          <p className="text-[13px] text-muted-foreground">
            This email has no text body{attachments.length === 1 ? " - just an attachment." : " - just attachments."}
          </p>
        ) : error ? (
          // The fetch failed (commonly a revoked/expired connector account).
          // Show an honest error — NEVER the list-chip subtext — and point at
          // the real source so the boss can still read it + knows to reconnect.
          <div className="space-y-1.5">
            <p className="inline-flex items-center gap-1.5 text-[13px] text-warning">
              <AlertTriangle className="size-3.5 shrink-0" aria-hidden />
              Couldn&apos;t load the full email.
            </p>
            <p className="text-[12px] text-muted-foreground">
              The {f.source} connection may need reconnecting (Settings → Connectors).
              {f.threadUrl ? " You can still open it in " + f.source + " above." : ""}
            </p>
          </div>
        ) : (
          <p className="text-[13px] text-muted-foreground">
            No message content available.
          </p>
        )}

        {/* Attachments present on the email (display only - see note below). */}
        <AttachmentChips attachments={attachments} />

        {/* When we already show plain text but a richer HTML fetch is still
            in flight, hint at the upgrade without blocking the read. */}
        {!richHtml && plain && loading ? (
          <p className="mt-2 inline-flex items-center gap-1.5 text-[11px] text-muted-foreground">
            <Spinner className="size-3" aria-hidden /> loading full message…
          </p>
        ) : null}
      </ModalSection>
    </ViewerSections>
  );
}

// dedupeChips collapses the overlapping triage values. A value is dropped when
// a longer sibling already contains it (so "reply" vanishes next to "needs
// reply") or when it's an exact duplicate.
//
// It returns plain STRINGS now: §7 turned the modal's chip row into text, so
// the per-axis tone (classificationTone / intentTone / modeTone) that used to
// colour a bordered pill has no surface to paint. Those helpers still tone the
// chips on the dashboard's own Follow-ups CARD - only the modal reads as prose.
function dedupeChips(values: string[]): string[] {
  const present = values.filter((v) => v.trim() !== "");
  const out: string[] = [];
  for (const v of present) {
    const lv = v.toLowerCase();
    const dominated = present.some(
      (o) => o !== v && o.toLowerCase().includes(lv) && o.length > v.length,
    );
    if (dominated) continue;
    if (out.some((s) => s.toLowerCase() === lv)) continue;
    out.push(v);
  }
  return out;
}

// Pull a string-valued field from a follow-up's metadata bag.
// callTimeline: "Mon 13 Jul, 5:29pm → 5:34pm · 4m 36s" for a call card.
//
// Rendered right-aligned above the transcript, deliberately mirroring the email
// viewer's received-date line so the two read as one system. Reads the STRUCTURED
// times phone/monitor.go stamps into metadata (started_at / ended_at /
// duration_ms), never the "4m36s" prefix in the subtitle string.
//
// Degrades a segment at a time rather than all-or-nothing: the two
// "refused instructions" cards are ALERTS raised mid-call, not call records, so
// they carry no duration and never will. Whatever is known still shows.
function callTimeline(m: Record<string, unknown> | undefined): string {
  const started = metaStr(m, "started_at");
  const ended = metaStr(m, "ended_at");
  const durMs = typeof m?.duration_ms === "number" ? m.duration_ms : null;

  const clock = (iso: string): string => {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return "";
    return d
      .toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })
      .toLowerCase()
      .replace(/\s/g, "");
  };
  const day = (iso: string): string => {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return "";
    return d.toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" });
  };
  const spell = (ms: number): string => {
    const s = Math.round(ms / 1000);
    if (s < 60) return `${s}s`;
    const m2 = Math.floor(s / 60);
    const rem = s % 60;
    if (m2 < 60) return rem ? `${m2}m ${rem}s` : `${m2}m`;
    const h = Math.floor(m2 / 60);
    return `${h}h ${m2 % 60}m`;
  };

  const anchor = started || ended;
  if (!anchor) return "";
  const parts: string[] = [];
  const when = [day(anchor), clock(anchor)].filter(Boolean).join(", ");
  // Only show the "→ ended" half when the call actually has a length; a
  // zero-length record would render "5:29pm → 5:29pm" and read as broken.
  const closed = started && ended && durMs ? ` → ${clock(ended)}` : "";
  if (when) parts.push(when + closed);
  if (durMs) parts.push(spell(durMs));
  return parts.join(" · ");
}

function metaStr(m: Record<string, unknown> | undefined, ...keys: string[]): string {
  if (!m) return "";
  for (const k of keys) {
    const v = m[k];
    if (typeof v === "string" && v.trim()) return v.trim();
  }
  return "";
}

function useSurfaceActionActivity(itemId: string): {
  running: boolean;
  // The action label of the run currently in flight, with the " · <title>"
  // suffix stripped — so a footer button can spin ONLY for its own action
  // instead of every button sharing one item-level `running` flag.
  runningLabel: string;
  text: string;
  error: string;
} {
  const { latest } = useRuns({ kind: "surface.action", targetId: itemId, limit: 3 });
  const ws = useWebSocket();
  const [text, setText] = React.useState("");
  const [error, setError] = React.useState("");
  const sessionId = itemId ? `surface-action-${itemId}` : "";

  React.useEffect(() => {
    if (!sessionId) return;
    return ws.subscribe((ev) => {
      if ("session_id" in ev && ev.session_id !== sessionId) return;
      if (ev.type === "delta") {
        setText((t) => t + ev.text);
      } else if (ev.type === "error") {
        setError(ev.message);
      } else if (ev.type === "complete") {
        setError("");
      }
    });
  }, [sessionId, ws]);

  const running = latest?.status === "running";
  // Run labels are built server-side as `${action.label} · ${item.title}`
  // (surface_action_api.go). Strip the title suffix back to the action label so
  // FollowupFooterActions can match a specific button to the in-flight run.
  const runningLabel =
    running && latest?.label ? latest.label.split(" · ")[0].trim() : "";
  return {
    running,
    runningLabel,
    text: text.trim(),
    error: error || (latest?.status === "error" ? latest.error || "Action failed." : ""),
  };
}

// SentReplySection - the read-only "Response" the draft box becomes once a reply
// is sent. Styled like the "Message" section (ModalSection + ModalPre) but
// success-tinted so it reads as "this is what you sent back", sitting above the
// incoming Message exactly where the draft box was.
function SentReplySection({ text, source }: { text: string; source: string }) {
  return (
    <ModalSection
      label="Response"
      tone="success"
      meta={`you replied${source ? " · " + source : ""}`}
    >
      <ModalPre>{text}</ModalPre>
    </ModalSection>
  );
}

function DraftReplyPanel({
  itemId,
  draft,
  streamedText,
  running,
  error,
  onSent,
}: {
  itemId: string;
  draft?: string;
  streamedText: string;
  running: boolean;
  error: string;
  // Fired with the sent text on a successful send POST so the parent can flip
  // to the read-only "Response" view immediately (server state confirms it).
  onSent?: (text: string) => void;
}) {
  const incoming = (draft || streamedText || "").trim();
  const [value, setValue] = React.useState(incoming);
  const [dirty, setDirty] = React.useState(false);
  const [sending, setSending] = React.useState(false);

  React.useEffect(() => {
    if (incoming && !dirty) setValue(incoming);
  }, [dirty, incoming]);

  if (!running && !incoming && !error) return null;

  return (
    <ModalSection
      label="Draft reply"
      meta={
        running ? (
          <span className="inline-flex items-center gap-1.5 text-brand">
            <Spinner className="size-3" aria-hidden />
            Jarvis is writing
          </span>
        ) : draft ? (
          "saved draft"
        ) : (
          "latest action"
        )
      }
    >
      <div className="space-y-2">
        {error ? (
          <p className="inline-flex items-center gap-1.5 text-[12px] text-danger">
            <AlertTriangle className="size-3.5 shrink-0" aria-hidden />
            {error}
          </p>
        ) : null}
        <Textarea
          value={value}
          onChange={(e) => {
            setDirty(true);
            setValue(e.target.value);
          }}
          placeholder={running ? "Jarvis is drafting..." : "No draft text captured yet."}
          className="min-h-[180px] resize-y bg-background text-[14px] leading-relaxed"
        />
        {running ? (
          <p className="inline-flex items-center gap-1.5 text-[11px] text-muted-foreground">
            <Spinner className="size-3" aria-hidden />
            Keep this open to watch the draft stream in. The run is also tracked durably if you navigate away.
          </p>
        ) : null}
        <div className="flex flex-wrap items-center justify-end gap-2">
          <Button
            type="button"
            size="sm"
            disabled={running || sending || value.trim().length === 0}
            className="h-10 gap-1.5"
            onClick={async () => {
              const draftText = value.trim();
              if (!draftText) return;
              setSending(true);
              try {
                await postSurfaceAction(itemId, "send_reply", { draftText });
                onSent?.(draftText);
              } finally {
                setSending(false);
              }
            }}
          >
            {sending ? (
              <Spinner className="size-3.5" aria-hidden />
            ) : (
              <Send className="size-3.5" aria-hidden />
            )}
            {sending ? "Sending..." : "Send reply"}
          </Button>
        </div>
      </div>
    </ModalSection>
  );
}

// Lazily fetch the full email body for a follow-up when the viewer opens.
// Nothing is loaded at poll time, so this is the only place the (possibly
// large) HTML body is retrieved - and only for the item actually opened.
type Attachment = { name: string; mimeType?: string; id: string };

type FetchedMessage = {
  html: string;
  text: string;
  attachments: Attachment[];
  loading: boolean;
  // True when the lazy fetch genuinely failed (transport / upstream 4xx, e.g.
  // a revoked connector account). Distinct from "fetched fine but the email
  // has no body" so the UI can show an honest error instead of silently
  // falling back to the list-chip subtext.
  error: boolean;
};

function useFollowupMessage(f: FollowUp): FetchedMessage {
  const [state, setState] = React.useState<FetchedMessage>({
    html: "",
    text: "",
    attachments: [],
    loading: true,
    error: false,
  });
  React.useEffect(() => {
    let alive = true;
    setState({ html: "", text: "", attachments: [], loading: true, error: false });
    const origin = f.origin ?? "followup";
    authedFetch(
      `/api/followups/message?id=${encodeURIComponent(f.id)}&origin=${encodeURIComponent(origin)}`,
    )
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error("message fetch failed"))))
      .then((d: { html?: string; text?: string; attachments?: Attachment[]; error?: string }) => {
        if (!alive) return;
        const html = d.html ?? "";
        const text = d.text ?? "";
        setState({
          html,
          text,
          attachments: Array.isArray(d.attachments) ? d.attachments : [],
          loading: false,
          // The endpoint returns 200 with an `error` marker when it had no
          // stored copy and the live fetch failed (e.g. revoked account). Only
          // treat it as an error when we genuinely got no body to show.
          error: Boolean(d.error) && !html.trim() && !text.trim(),
        });
      })
      .catch(() => {
        if (alive) setState({ html: "", text: "", attachments: [], loading: false, error: true });
      });
    return () => {
      alive = false;
    };
  }, [f.id, f.origin]);
  return state;
}

// Attachment chips - paperclip + filename, clickable to open the file. On
// click we fetch a short-lived download URL (GMAIL_GET_ATTACHMENT → presigned
// URL) and open it in a new tab. The blank tab is opened synchronously so the
// popup blocker doesn't eat it after the await.
// Attachment chips - paperclip + filename, showing what the email carries.
// Not clickable: we can't serve the bytes (the connector's download URL is
// broken upstream, and direct provider download needs an OAuth token we don't
// hold). Real in-app view/download is gated on that token; for now this is an
// honest "here's what's attached" label.
function AttachmentChips({ attachments }: { attachments: Attachment[] }) {
  if (attachments.length === 0) return null;
  // Text, not bordered pills (§7). One paperclip leads the line; the filenames
  // read as a list. Still display-only for the same reason as before.
  return (
    <p className="mt-3 flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1 text-[12px] text-quiet">
      <Paperclip className="size-3.5 shrink-0" aria-hidden />
      {attachments.map((att, i) => (
        <span key={`${att.id || att.name}-${i}`} className="min-w-0 break-all" title={att.name}>
          {att.name}
          {i < attachments.length - 1 ? "," : ""}
        </span>
      ))}
    </p>
  );
}

// A calm three-bar shimmer while the email body loads.
function MessageSkeleton() {
  return (
    <div className="space-y-2" aria-hidden>
      <div className="h-3 w-2/3 animate-pulse rounded bg-muted" />
      <div className="h-3 w-full animate-pulse rounded bg-muted" />
      <div className="h-3 w-5/6 animate-pulse rounded bg-muted" />
      <div className="h-3 w-1/2 animate-pulse rounded bg-muted" />
    </div>
  );
}

// ── Surface item (generic surface contract) ───────────────────────────────
//
// Untitled UI labelled-rows shape. The old version was noisy:
//   - chip row (kind · via X · time · importance) that duplicated the
//     header's own meta
//   - pipe-separated subtitle ("mr khaya | support@... | likely no
//     reply") rendered as raw text
//   - italic importanceReason floating between the subtitle and a
//     faux-card holding the multi-line "Label: value" body as a single
//     <pre> block
//   - a second metadata <dl> at the bottom showing keys most users
//     never need to see
//
// New version drops the chip row entirely (the header subtitle owns
// kind/source/time/importance), drops the pipe subtitle, and parses
// the labelled body into <ModalField> rows so each field name appears
// in muted tracking-wide caps with its value on the right. The "why
// it matters" sentence becomes a clean italic intro - no card around
// it, just a paragraph. The url renders as a thin link at the bottom
// only when present.
// dedupeSurfaceBody strips the parts of a body that merely repeat the
// importanceReason rendered (italic) directly above it. Writers used to put
// the reason's text inside the body too — the first line of the narrative,
// and/or a trailing "What this means: <floor>" — so the modal showed the same
// sentence twice back to back (the duplicated nightly-cognition card the boss
// caught). Defends against every writer AND historical DB rows.
function dedupeSurfaceBody(
  body: string | null | undefined,
  reason: string | null | undefined,
): string {
  const b = (body ?? "").trim();
  const r = (reason ?? "").trim();
  if (!b || !r) return b;
  let out = b;
  if (out === r) return "";
  if (out.startsWith(r)) out = out.slice(r.length).trim();
  const legacySuffix = "What this means: " + r;
  if (out.endsWith(legacySuffix)) {
    out = out.slice(0, out.length - legacySuffix.length).trim();
  }
  return out;
}

// FieldValue renders a labelled field's value. Values whose lines are "- "
// bullets (e.g. the nightly digest's "What I learned" lessons) render as a
// real list instead of a wall of <br>-joined text.
function FieldValue({ value }: { value: string }) {
  const lines = value.split("\n");
  const bullets = lines.filter((l) => l.trim().startsWith("- "));
  if (bullets.length > 0 && bullets.length === lines.filter((l) => l.trim() !== "").length) {
    return (
      <ul className="list-disc space-y-1.5 pl-4">
        {bullets.map((l, i) => (
          <li key={i} className="break-words">
            {l.trim().slice(2)}
          </li>
        ))}
      </ul>
    );
  }
  return (
    <>
      {lines.map((line, i, arr) => (
        <React.Fragment key={i}>
          {line}
          {i < arr.length - 1 ? <br /> : null}
        </React.Fragment>
      ))}
    </>
  );
}

/* MessageBody - a message somebody left for the boss on the phone.
 *
 * This is his MAIL, and it should read like it: who it is from, what his
 * assistant made of the call, and then the message itself, given room to
 * breathe. Not a labelled key/value dump, and not a transcript to excavate.
 *
 * Structure mirrors how a real assistant hands you a note: the read first
 * ("she rang about the voicemail, she sounded moved"), then the words.
 */
function MessageBody({ item }: { item: SurfaceItem }) {
  const meta = item.metadata ?? {};
  const from = typeof meta.from === "string" ? meta.from : "";
  const number = typeof meta.number === "string" ? meta.number : "";
  const message = typeof meta.message === "string" ? meta.message : item.body ?? "";
  const read = item.importanceReason?.trim() || (typeof meta.read === "string" ? meta.read : "");
  const urgency = typeof meta.urgency === "string" ? meta.urgency.toLowerCase() : "";
  const urgent = urgency === "high" || urgency === "urgent" || urgency === "emergency";
  const initial = (from || "?").trim().charAt(0).toUpperCase();

  return (
    <div className="space-y-4 pt-4">
      {/* Who it is from. An avatar and a name, the way every message you have
          ever read is headed. */}
      <div className="flex min-w-0 items-center gap-3">
        <span
          aria-hidden
          className={cn(
            "flex size-10 shrink-0 items-center justify-center rounded-full text-sm font-semibold",
            urgent ? "bg-danger/15 text-danger" : "bg-brand/15 text-brand",
          )}
        >
          {initial}
        </span>
        <span className="flex min-w-0 flex-col">
          <span className="truncate text-sm font-semibold">{from || "Unknown caller"}</span>
          {number ? (
            <span className="truncate text-[11px] text-muted-foreground">{number}</span>
          ) : null}
        </span>
        {urgent ? (
          // One alive signal (§1.4): red ink says urgent. It does not need a
          // bordered pill to say it, and a pill here is a box inside the row.
          <span className="ml-auto shrink-0 font-mono text-[10.5px] uppercase tracking-[0.14em] text-danger">
            Urgent
          </span>
        ) : null}
      </div>

      {/* Jarvis's own account: what the call was about and how they seemed. */}
      {read ? (
        <p className="text-[13.5px] italic leading-relaxed text-muted-foreground">{read}</p>
      ) : null}

      {/* The message. Given the room it deserves — on an Inset (the one
          container allowed inside a row) rather than the bordered card that
          used to sit inside the modal's own border. */}
      {message ? (
        <ModalSection label="Message">
          <Inset variant="quote" text={message} />
        </ModalSection>
      ) : null}
    </div>
  );
}

function SurfaceBody({ item }: { item: SurfaceItem }) {
  // A message is mail, not a surfaced FYI. It gets its own shape.
  if (item.surface === "messages") return <MessageBody item={item} />;

  // When an item was PRODUCED by a session (a phone errand Jarvis ran after the
  // boss hung up, a cron's nightly run), he should be able to see the work, not
  // just the summary of it. One tap, straight into the conversation.
  const sessionId =
    typeof item.metadata?.session_id === "string" ? item.metadata.session_id : "";

  const body = dedupeSurfaceBody(item.body, item.importanceReason);
  const fields = parseLabeledBody(body);
  // When the body parses cleanly into fields, drop the "Why it matters"
  // duplicate from inline rendering since we render importanceReason
  // above as a hero italic. Same for "Account" if it's already shown
  // in the calm header subtitle - actually keep Account, the boss
  // wants every contract field visible.
  const visibleFields = fields.filter(
    (f) => f.label.toLowerCase() !== "why it matters" || !item.importanceReason,
  );

  // When it rang, when it hung up, how long it ran — right-aligned above the
  // transcript, same shape and typography as the received-date on an opened
  // email. Empty string for any non-call surface, and for the mid-call refusal
  // alerts, which have no times to state.
  const timeline = item.surface === "calls" ? callTimeline(item.metadata) : "";

  const empty = visibleFields.length === 0 && !body && !item.importanceReason;

  return (
    <ViewerSections>
      {timeline ? (
        <ModalChips className="mb-3 justify-end">
          <span suppressHydrationWarning>{timeline}</span>
        </ModalChips>
      ) : null}

      {/* Jarvis's read on it, in his own voice, above the labelled rows. */}
      {item.importanceReason ? (
        <p className="mb-3 min-w-0 whitespace-pre-wrap break-words font-voice text-[15.5px] leading-[1.55] text-foreground">
          {item.importanceReason}
        </p>
      ) : null}

      {/* Labelled rows on hairlines — the bordered card that used to wrap them
          was a box inside the modal's box. The rows already carry the shape. */}
      {visibleFields.length > 0 ? (
        <div className="min-w-0 divide-y divide-hairline">
          {visibleFields.map((f) => (
            <ModalField key={f.label} label={f.label}>
              <FieldValue value={f.value} />
            </ModalField>
          ))}
        </div>
      ) : body ? (
        // Body didn't parse into labelled fields - render as prose so we
        // still show what Jarvis wrote, without the faux-card chrome.
        <p className="min-w-0 whitespace-pre-wrap break-words text-[13.5px] leading-relaxed text-foreground/90">
          {body}
        </p>
      ) : empty ? (
        <QuietNote>Nothing further on this one.</QuietNote>
      ) : null}

      {sessionId ? (
        <div className="pt-4">
          <a
            href={`/live?session=${encodeURIComponent(sessionId)}`}
            className="inline-flex h-11 items-center gap-2 rounded-[10px] border border-input px-3 text-[13.5px] font-medium transition-colors hover:bg-accent"
          >
            <MessagesSquare className="size-3.5" aria-hidden />
            See what he did
          </a>
        </div>
      ) : null}

      {item.url ? (
        <div className="pt-3">
          <ModalUrl href={item.url} icon={<ExternalLink className="size-3.5" aria-hidden />}>
            {item.url}
          </ModalUrl>
        </div>
      ) : null}
    </ViewerSections>
  );
}

// ── Saved ─────────────────────────────────────────────────────────────────
function SavedBody({ s }: { s: Saved }) {
  // kind / reading time / saved-when are in the header context line.
  if (!s.source && !s.body && !s.url) {
    return <QuietNote>Just the title on this one.</QuietNote>;
  }
  return (
    <ViewerSections>
      {s.source ? (
        <ModalSection label="Source">
          <span className="break-words">{s.source}</span>
        </ModalSection>
      ) : null}
      {s.body ? (
        <ModalSection label="Saved">
          <ModalPre>{s.body}</ModalPre>
        </ModalSection>
      ) : null}
      {s.url ? (
        <ModalSection label="Link">
          <ModalUrl href={s.url} icon={<ExternalLink className="size-3.5" aria-hidden />}>
            {s.url}
          </ModalUrl>
        </ModalSection>
      ) : null}
    </ViewerSections>
  );
}

// ── Artifact (Made by Jarvis) ──────────────────────────────────────────────
function ArtifactBody({ a }: { a: Artifact }) {
  const meta: { k: string; v: React.ReactNode }[] = [];
  if (a.virtualPath) meta.push({ k: "path", v: <span className="break-all font-mono text-[11px]">{a.virtualPath}</span> });
  if (a.sourceTool) meta.push({ k: "built with", v: a.sourceTool });
  if (a.bridge) meta.push({ k: "where", v: a.bridge === "mac" ? "your Mac" : "cloud workspace" });
  // kind / "made by jarvis" / built-when are in the header context line.
  return (
    <ViewerSections>
      <ModalSection label="What it is">
        <p>
          {a.description?.trim() ? a.description : "Jarvis built this for you."}{" "}
          Tap <span className="font-medium text-foreground">Open</span> to launch it.
        </p>
      </ModalSection>
      {meta.length > 0 ? (
        <ModalSection label="Details">
          <ModalDl entries={meta} />
        </ModalSection>
      ) : null}
      {a.githubUrl ? (
        <ModalSection label="Repo">
          <ModalUrl href={a.githubUrl} icon={<ExternalLink className="size-3.5" aria-hidden />}>
            {a.githubUrl}
          </ModalUrl>
        </ModalSection>
      ) : null}
    </ViewerSections>
  );
}

// ── Work ──────────────────────────────────────────────────────────────────
function WorkBody({ w }: { w: WorkItem }) {
  // The subtitle is "agent-narrated outcome" - when it leads with
  // "error" it's the failure message and deserves the danger-toned
  // section. Otherwise it's neutral context above the metadata grid.
  const isError = (w.subtitle ?? "").toLowerCase().startsWith("error");
  const scheduleEntries: { k: string; v: React.ReactNode }[] = [];
  if (w.scheduledFor) scheduleEntries.push({ k: "scheduled", v: clockTime(w.scheduledFor) });
  if (w.startedAt) scheduleEntries.push({ k: "started", v: clockTime(w.startedAt) });
  if (w.finishedAt) scheduleEntries.push({ k: "finished", v: clockTime(w.finishedAt) });

  // Details grid carries what's deliberately kept OFF the readable card title:
  // which subsystem is behind this (Voyager / GEPA / Schedule / …) and the raw
  // technical handle (auto-generated skill name, cron schedule, watch type).
  const detailEntries: { k: string; v: React.ReactNode }[] = [];
  if (w.engine) detailEntries.push({ k: "engine", v: w.engine });
  if (w.ref) detailEntries.push({ k: "reference", v: <span className="break-all font-mono">{w.ref}</span> });

  // Column / engine / duration are in the header context line now — that chip
  // row was the loudest of the eight boxes this modal used to open with.
  return (
    <ViewerSections>
      {/* What it does — the job's actual instruction (cron) or goal (plan), so a
          queued/running item explains itself inline instead of sending the boss
          to /cron to find out what it is. */}
      {w.instruction?.trim() ? (
        <ModalSection label="What it does">
          <ModalPre>{w.instruction.trim()}</ModalPre>
        </ModalSection>
      ) : null}

      {/* Skills it runs — the ingredients under the job headline, as mono text
          rather than a row of bordered pills. */}
      {w.skills && w.skills.length > 0 ? (
        <ModalSection label={w.skills.length === 1 ? "Skill" : "Skills"}>
          <span className="min-w-0 break-words font-mono text-[12px] text-foreground/85">
            {w.skills.join(", ")}
          </span>
        </ModalSection>
      ) : null}

      {/* The narrative the run wrote ("what it did / how it went / outcome").
          ModalPre preserves the header + body line break the executor built. */}
      {w.summary ? (
        <ModalSection label="Report">
          <ModalPre>{w.summary}</ModalPre>
        </ModalSection>
      ) : null}

      {/* Subtitle is the short status line. When it leads with "error" it's a
          failure message and gets the danger-toned card; otherwise it's neutral
          context. Suppressed when it would just echo the summary's header. */}
      {w.subtitle && !(w.summary && !isError) ? (
        <ModalSection label={isError ? "Error" : "Status"} tone={isError ? "error" : "default"}>
          <ModalPre mono={isError}>{w.subtitle}</ModalPre>
        </ModalSection>
      ) : null}

      {detailEntries.length > 0 ? (
        <ModalSection label="Details">
          <ModalDl entries={detailEntries} />
        </ModalSection>
      ) : null}

      {scheduleEntries.length > 0 ? (
        <ModalSection label="Schedule">
          <ModalDl entries={scheduleEntries} />
        </ModalSection>
      ) : null}

      {/* Workflow runs carry their step state-machine inline - the Kanban
          card IS the workflow view. Tap any column, see the steps. */}
      {w.kind === "workflow" && w.workflowSteps && w.workflowSteps.length > 0 ? (
        <ModalSection
          label="Steps"
          meta={`${w.workflowSteps.length} step${w.workflowSteps.length === 1 ? "" : "s"}`}
        >
          <ol className="space-y-2">
            {w.workflowSteps.map((s) => (
              <li key={s.index} className="flex gap-2 text-[12px]">
                <span
                  className={cn(
                    "mt-1 size-1.5 shrink-0 rounded-full",
                    workflowStepDot(s.status),
                  )}
                  aria-hidden
                />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
                      {s.kind}
                    </span>
                    <span className="truncate text-foreground/90">
                      {s.name || `step ${s.index}`}
                    </span>
                    <span
                      className={cn(
                        "ml-auto shrink-0 font-mono text-[10px] uppercase tracking-wider",
                        workflowStepText(s.status),
                      )}
                    >
                      {s.status}
                    </span>
                  </div>
                  {s.error ? (
                    <p className="mt-0.5 break-words text-[11px] text-danger">{s.error}</p>
                  ) : s.output ? (
                    <p className="mt-0.5 line-clamp-2 break-words text-[11px] text-muted-foreground">
                      {s.output}
                    </p>
                  ) : null}
                </div>
              </li>
            ))}
          </ol>
        </ModalSection>
      ) : null}

      {/* Plans carry their durable step timeline inline - the Kanban card IS
          the plan view. Same pattern as workflow runs, richer per-step (status,
          verification evidence, live spinner). */}
      {w.kind === "plan" && w.planSteps && w.planSteps.length > 0 ? (
        <ModalSection
          label="Steps"
          meta={`${w.doneCount ?? 0}/${w.totalCount ?? w.planSteps.length}`}
        >
          <PlanTimeline steps={w.planSteps} />
        </ModalSection>
      ) : null}

      {/* Mandate — the definition of done (binary criteria) + the verification
          verdict, inline so the Kanban card carries the full contract. */}
      {w.kind === "mandate" && w.mandateCriteria && w.mandateCriteria.length > 0 ? (
        <ModalSection
          label="Definition of done"
          meta={`${w.doneCount ?? 0}/${w.totalCount ?? w.mandateCriteria.length}`}
        >
          <ul className="space-y-2">
            {w.mandateCriteria.map((c) => (
              <li key={c.id} className="flex items-start gap-2 text-sm min-w-0">
                <span
                  className={cn(
                    "mt-0.5 shrink-0 font-mono text-xs",
                    c.status === "pass"
                      ? "text-emerald-500"
                      : c.status === "fail"
                        ? "text-rose-500"
                        : "text-muted-foreground",
                  )}
                  aria-hidden
                >
                  {c.status === "pass" ? "[x]" : c.status === "fail" ? "[✗]" : "[ ]"}
                </span>
                <div className="min-w-0">
                  <span className="break-words">{c.text}</span>
                  {c.evidence ? (
                    <span className="mt-0.5 block break-words text-xs text-muted-foreground">
                      {c.evidence}
                    </span>
                  ) : null}
                </div>
              </li>
            ))}
          </ul>
        </ModalSection>
      ) : null}

      {w.kind === "mandate" && w.crosscheck?.overall ? (
        <ModalSection
          label="Verification"
          tone={
            w.crosscheck.overall === "pass"
              ? "success"
              : w.crosscheck.overall === "fail"
                ? "error"
                : "default"
          }
        >
          <ModalDl
            entries={[
              { k: "Verdict", v: w.crosscheck.overall === "pass" ? "Passed" : "Failed" },
       { k: "Verified by", v: w.crosscheck.auditor ?? "n/a" },
              ...(typeof w.crosscheck.confidence === "number"
                ? [{ k: "Confidence", v: `${Math.round(w.crosscheck.confidence * 100)}%` }]
                : []),
              ...(w.crosscheck.notes ? [{ k: "Notes", v: w.crosscheck.notes }] : []),
            ]}
          />
        </ModalSection>
      ) : null}

      {w.detailHref ? (
        <ModalSection label="Elsewhere">
          <ModalUrl
            href={w.detailHref}
            external={false}
            icon={<ExternalLink className="size-3.5" aria-hidden />}
          >
            open in {w.detailHref}
          </ModalUrl>
        </ModalSection>
      ) : null}
    </ViewerSections>
  );
}

// Workflow step status → dot colour / text colour for the run drawer.
function workflowStepDot(status: string): string {
  switch (status) {
    case "done":
      return "bg-success";
    case "failed":
      return "bg-danger";
    case "running":
      return "bg-info animate-pulse";
    case "awaiting":
      return "bg-rose-400";
    case "skipped":
      return "bg-muted-foreground/40";
    default:
      return "bg-muted-foreground/30";
  }
}

function workflowStepText(status: string): string {
  switch (status) {
    case "done":
      return "text-success";
    case "failed":
      return "text-danger";
    case "running":
      return "text-info";
    case "awaiting":
      return "text-rose-400";
    default:
      return "text-muted-foreground";
  }
}


// ── Activity ──────────────────────────────────────────────────────────────
// One universal layout for EVERY activity kind. A detail is a set of newline
// segments; a "label: value" segment renders as a labeled block (the
// action/why/result/safety shape heartbeat findings emit), "- x" lines render
// as bullets, and everything else is clean prose. Backticked spans render as
// inline code. Reflections, runs, system notes — all read the same way.

type ActivityBlock = { label?: string; value?: string; bullets?: string[] };

const ACTIVITY_LABEL_RE = /^([a-zA-Z][a-zA-Z /]{0,22}):\s+(.+)$/;

function parseActivityDetail(detail: string): ActivityBlock[] {
  const blocks: ActivityBlock[] = [];
  let prose: string[] = [];
  let bullets: string[] = [];
  const flushProse = () => {
    if (prose.length) blocks.push({ value: prose.join(" ") });
    prose = [];
  };
  const flushBullets = () => {
    if (bullets.length) blocks.push({ bullets });
    bullets = [];
  };
  for (const raw of detail.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line) {
      flushProse();
      flushBullets();
      continue;
    }
    const bm = line.match(/^[-•]\s+(.*)$/);
    if (bm) {
      flushProse();
      bullets.push(bm[1]);
      continue;
    }
    flushBullets();
    const lm = line.match(ACTIVITY_LABEL_RE);
    if (lm) {
      flushProse();
      blocks.push({ label: lm[1], value: lm[2] });
      continue;
    }
    prose.push(line);
  }
  flushProse();
  flushBullets();
  return blocks;
}

// renderInline turns `backticked` spans into inline code. React escapes the
// rest, so literal angle-bracket tokens like <connected_accounts> survive.
function renderActivityInline(text: string): React.ReactNode {
  return text.split(/(`[^`]+`)/g).map((p, i) =>
    p.length > 1 && p.startsWith("`") && p.endsWith("`") ? (
      <code
        key={i}
        className="rounded bg-muted px-1 py-0.5 font-mono text-[11.5px] text-foreground/90"
      >
        {p.slice(1, -1)}
      </code>
    ) : (
      <span key={i}>{p}</span>
    ),
  );
}

function ActivityBody({ e }: { e: ActivityEv }) {
  const blocks = parseActivityDetail(e.detail || "");
  // Kind + relative time are in the header context line; the full timestamp
  // stays here because the header carries the relative one ("2h ago") and the
  // absolute one is the thing you open a detail view to read.
  return (
    <ViewerSections>
      <ModalChips className="mb-3 justify-end">
        <span suppressHydrationWarning>{fullDateTime(e.at)}</span>
      </ModalChips>

      {/* Body — labelled rows for "label: value" segments (the action/why/
          result/safety shape heartbeat findings emit), bullets and prose for
          the rest. A labelled segment IS a ModalSection now, so the whole body
          reads as one column of labels instead of two competing label styles. */}
      {blocks.length > 0 ? (
        <>
          {blocks.map((b, i) =>
            b.label ? (
              <ModalSection key={i} label={b.label}>
                {renderActivityInline(b.value || "")}
              </ModalSection>
            ) : b.bullets ? (
              <ul
                key={i}
                className="list-disc space-y-1 py-2 pl-5 text-[13px] leading-relaxed text-foreground/90"
              >
                {b.bullets.map((x, j) => (
                  <li key={j} className="break-words">
                    {renderActivityInline(x)}
                  </li>
                ))}
              </ul>
            ) : (
              <p
                key={i}
                className="min-w-0 break-words py-1 text-[13px] leading-relaxed text-foreground/85"
              >
                {renderActivityInline(b.value || "")}
              </p>
            ),
          )}
        </>
      ) : (
        <QuietNote>No further detail.</QuietNote>
      )}
    </ViewerSections>
  );
}

// ── header meta dispatch ─────────────────────────────────────────────────

/* headerKindLabel - the kind label that leads every modal's context line.
 *
 * It used to also hand back an `Icon` and a `tone` className for the header's
 * bordered icon chip. Majordomo §7 removed the chip (one title per surface, no
 * chrome above it), so the icon and the tint have no surface left to paint and
 * are gone rather than left dangling as data nothing reads. The LABEL is the
 * part that carried meaning and it still does, as the first word of the
 * context line. */
function headerMeta(item: DashboardItem): { label: string } {
  switch (item.kind) {
    case "pursuit":
      return { label: "Pursuit" };
    case "todo":
      return { label: "Todo" };
    case "event":
      return { label: "Calendar event" };
    case "reflection":
      return { label: "Reflection" };
    case "approval":
      if (item.data.kind === "code_proposal") return { label: "Code proposal" };
      if (item.data.kind === "curiosity") return { label: "Curiosity" };
      return { label: "Approval" };
    case "followup":
      return { label: "Follow-up" };
    case "surface":
      return { label: surfaceKindLabel(item.data.surface) };
    case "work":
      return { label: "Agent work" };
    case "saved":
      return { label: "Saved" };
    case "artifact":
      return { label: "Made by Jarvis" };
    case "activity":
      return { label: "Activity" };
    case "record":
      return { label: item.data.subtitle || item.data.kind };
  }
}

