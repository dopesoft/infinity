"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { motion, AnimatePresence } from "framer-motion";
import {
  Activity,
  AlertTriangle,
  ArrowRight,
  AtSign,
  Brain,
  Calendar,
  CheckCircle2,
  Circle,
  ExternalLink,
  FileCode,
  Flame,
  Hash,
  HelpCircle,
  Inbox,
  AlignLeft,
  Layers,
  ListTodo,
  Loader2,
  Mail,
  MapPin,
  MessageCircle,
  Paperclip,
  Quote,
  Repeat,
  Sparkles,
  Target,
  Terminal,
  Users,
  Video,
  X,
} from "lucide-react";
import { authedFetch } from "@/lib/api";
import {
  ResponsiveModal,
  ResponsiveModalHeader,
} from "@/components/ui/responsive-modal";
import {
  ModalChips,
  ModalCode,
  ModalDl,
  ModalField,
  ModalHtml,
  ModalPre,
  ModalSection,
  ModalUrl,
} from "@/components/ui/modal-content";
import { Chip, classificationTone, intentTone, modeTone, type ChipTone } from "./Chip";
import { PlanTimeline } from "./PlanTimeline";
import { SurfaceActionRow } from "./SurfaceActions";
import { cn } from "@/lib/utils";
import { clockTime, dayLabel, formatDuration, fullDateTime, relTime } from "@/lib/dashboard/format";
import { seedSession } from "@/lib/dashboard/seed";
import { parseLabeledBody } from "@/lib/dashboard/parseBody";
import type {
  Approval,
  CalendarEvent,
  DashboardItem,
  FollowUp,
  Pursuit,
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
  const footerOverride = isEvent
    ? "bg-violet-100/70 dark:bg-violet-950/40 justify-between"
    : undefined;
  // The email viewer gets a wider, fixed-height frame on desktop so it reads
  // comfortably AND doesn't grow/shrink as the lazy email body loads. Other
  // kinds keep the snug, content-sized `lg` modal.
  const isFollowup = item?.kind === "followup";
  return (
    <ResponsiveModal
      open={open}
      onOpenChange={(o) => (!o ? onClose() : null)}
      size={isFollowup ? "xl" : "lg"}
      desktopHeight={isFollowup ? "tall" : "auto"}
      title={item ? getViewerTitle(item) : "Item"}
      description={item ? getViewerKindLabel(item) : undefined}
      header={item ? <ItemHeader item={item} /> : undefined}
      footer={item && hasFooter ? <ViewerActions item={item} onResolved={onResolved} /> : undefined}
      footerClassName={footerOverride}
    >
      <AnimatePresence mode="wait">
        {item ? <ViewerBody key={getViewerKey(item)} item={item} /> : null}
      </AnimatePresence>
    </ResponsiveModal>
  );
}

function ItemHeader({ item }: { item: DashboardItem }) {
  const { Icon, label, tone } = headerMeta(item);
  // Event kind gets a bespoke header per the design reference: large
  // bold title, classification pill (tone-coded), kebab placeholder,
  // date/duration subtitle row under the title. Hand-built instead of
  // ResponsiveModalHeader because the design hierarchy is distinct
  // enough that fitting it into the generic header was producing the
  // wrong typography.
  if (item.kind === "event") {
    return <EventHeader event={item.data} />;
  }
  // Surface items, follow-ups, saved, and reflections render the "calm
  // preview" header shape: no loud uppercase eyebrow above the title,
  // title grows to 2 lines for long subjects, and the kind / source /
  // time appear as a muted subtitle UNDER the title. Other kinds
  // (approval, work, todo, pursuit) keep the original compact header.
  const useCalmHeader =
    item.kind === "surface" ||
    item.kind === "followup" ||
    item.kind === "saved" ||
    item.kind === "reflection";

  if (useCalmHeader) {
    return (
      <ResponsiveModalHeader
        icon={<Icon className="size-4" aria-hidden />}
        title={getViewerTitle(item)}
        subtitle={calmSubtitle(item, label)}
        tone={tone}
        titleSize="lg"
        titleClamp={2}
      />
    );
  }
  return (
    <ResponsiveModalHeader
      icon={<Icon className="size-4" aria-hidden />}
      eyebrow={label}
      title={getViewerTitle(item)}
      tone={tone}
    />
  );
}

// ── EventHeader ───────────────────────────────────────────────────────────
//
// Bespoke event modal header. Matches the design reference: large bold
// title, classification pill (tone-coded) + kebab on the right, plain
// sans-serif date/duration row under the title, separator at the bottom
// to match other modal headers. NO icon + eyebrow combo; the shape is
// distinct from every other kind's header.
function EventHeader({ event }: { event: CalendarEvent }) {
  const cls = (event.classification || "meeting").toLowerCase();
  const pillTone = eventClassificationTone(cls);
  return (
    <header className="flex shrink-0 flex-col gap-2 border-b px-4 pt-4 pb-3 pr-12 sm:px-5 sm:pr-14">
      <div className="flex items-center gap-2">
        <h2 className="min-w-0 flex-1 text-[20px] font-semibold leading-tight tracking-tight text-foreground sm:text-[22px]">
          {event.title}
        </h2>
        {/* Both right-side affordances are h-6 so they vertically center
            cleanly against the title baseline (items-center on the row). */}
        {event.htmlLink ? (
          <a
            href={event.htmlLink}
            target="_blank"
            rel="noreferrer noopener"
            aria-label="Open in Google Calendar"
            className="inline-flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <ExternalLink className="size-4" aria-hidden />
          </a>
        ) : null}
        <span
          className={cn(
            "inline-flex h-6 shrink-0 items-center rounded-full px-2.5 text-[12px] font-medium capitalize leading-none",
            pillTone,
          )}
        >
          {cls}
        </span>
      </div>
      <p
        className="flex flex-wrap items-baseline gap-x-3 text-[13px] text-foreground/80"
        suppressHydrationWarning
      >
        <span>{eventDateLine(event.startsAt)}</span>
        {!event.allDay ? (
          <>
            <span>
              {clockTime(event.startsAt)}
              {event.endsAt ? ` – ${clockTime(event.endsAt)}` : ""}
            </span>
            {event.endsAt ? (
              <span className="text-muted-foreground">
                ({eventDuration(event.startsAt, event.endsAt)})
              </span>
            ) : null}
          </>
        ) : (
          <span>All day</span>
        )}
      </p>
    </header>
  );
}

// eventClassificationTone: orange "Meeting" pill in the design ref is the
// default; other event kinds get distinct tints so the boss can spot
// "flight" / "dinner" / "appointment" at a glance. (Distinct from the
// email-chip classificationTone imported from ./Chip - this one returns a
// full className string for the bespoke event pill.)
function eventClassificationTone(cls: string): string {
  switch (cls) {
    case "meeting":
      return "bg-orange-100 text-orange-700 dark:bg-orange-500/20 dark:text-orange-300";
    case "flight":
    case "travel":
      return "bg-sky-100 text-sky-700 dark:bg-sky-500/20 dark:text-sky-300";
    case "dinner":
    case "lunch":
      return "bg-amber-100 text-amber-700 dark:bg-amber-500/20 dark:text-amber-300";
    case "concert":
    case "social":
      return "bg-violet-100 text-violet-700 dark:bg-violet-500/20 dark:text-violet-300";
    case "appointment":
    case "interview":
      return "bg-emerald-100 text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-300";
    case "wedding":
      return "bg-pink-100 text-pink-700 dark:bg-pink-500/20 dark:text-pink-300";
    default:
      return "bg-muted text-muted-foreground";
  }
}

// eventDateLine: "Thu, 1st April, 2026" format from the design ref.
// Uses ordinal suffix on the day. en-GB locale ordering (day-month-year).
function eventDateLine(iso: string): string {
  const d = new Date(iso);
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

// calmSubtitle: builds the muted meta strip rendered under the hero
// title for the calm header shape. Combines the kind label with the
// most useful trailing meta (source + time + optional importance) in
// interpunct form. Each segment is only included when it carries
// signal; an unranked item never shows "imp 0", and a source-less
// item just omits the "via" segment.
function calmSubtitle(item: DashboardItem, kindLabel: string): React.ReactNode {
  const parts: string[] = [kindLabel];
  if (item.kind === "surface") {
    const s = item.data;
    if (s.source) parts.push(s.source);
    parts.push(relTime(s.createdAt));
    if (typeof s.importance === "number" && s.importance >= 50) {
      parts.push(`imp ${s.importance}`);
    }
  } else if (item.kind === "followup") {
    if (item.data.source) parts.push(item.data.source);
    parts.push(relTime(item.data.receivedAt));
  } else if (item.kind === "saved") {
    if (item.data.kind) parts.push(item.data.kind);
    parts.push(`saved ${relTime(item.data.savedAt)}`);
  } else if (item.kind === "reflection") {
    parts.push(`${item.data.evidenceCount} sources`);
    parts.push(relTime(item.data.capturedAt));
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
    case "activity": return item.data.title;
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
    case "activity": return "Activity event";
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

function ViewerActions({
  item,
  onResolved,
}: {
  item: DashboardItem;
  onResolved?: (item: DashboardItem) => void;
}) {
  // Every item gets a "Discuss with Jarvis" primary CTA. Kind-specific
  // secondary actions are preview-only "Open in <surface>" deep-links
  // (per the IA defrag), no inline approve/reject here anymore. The
  // one exception: follow-ups also get a "Dismiss" button so the boss
  // can drop a row without opening a chat for it — and dismissals are
  // durable (status='dismissed' is preserved by the connector poller's
  // ON CONFLICT DO NOTHING, so re-polled threads don't resurface).
  const router = useRouter();
  const [seeding, setSeeding] = React.useState(false);
  const [dismissing, setDismissing] = React.useState(false);

  async function dismissFollowup() {
    if (item.kind !== "followup") return;
    const id = item.data.id;
    const origin = item.data.origin ?? "followup";
    setDismissing(true);
    try {
      const res = await authedFetch("/api/followups/dismiss", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ id, origin }),
      });
      if (res.ok && onResolved) onResolved(item);
    } finally {
      setDismissing(false);
    }
  }

  async function dismissCuriosity() {
    if (item.kind !== "approval" || item.data.kind !== "curiosity") return;
    setDismissing(true);
    try {
      const res = await authedFetch(`/api/curiosity/questions/${item.data.id}/decide`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ decision: "dismissed" }),
      });
      if (res.ok && onResolved) onResolved(item);
    } finally {
      setDismissing(false);
    }
  }

  async function dismissSurface() {
    if (item.kind !== "surface") return;
    setDismissing(true);
    try {
      const res = await authedFetch("/api/followups/dismiss", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ id: item.data.id, origin: "surface" }),
      });
      if (res.ok && onResolved) onResolved(item);
    } finally {
      setDismissing(false);
    }
  }

  // Folded "system" notes in the Activity feed carry a dismiss handle
  // (origin + id) mapping back to mem_surface_items, so they stay clearable
  // through the same endpoint without inventing a per-kind route.
  async function dismissActivity() {
    if (item.kind !== "activity" || !item.data.dismiss) return;
    const { id, origin } = item.data.dismiss;
    setDismissing(true);
    try {
      const res = await authedFetch("/api/followups/dismiss", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ id, origin }),
      });
      if (res.ok && onResolved) onResolved(item);
    } finally {
      setDismissing(false);
    }
  }

  async function discuss() {
    const id = (item.data as { id?: string }).id ?? "";
    setSeeding(true);
    try {
      const sessionId = await seedSession(item.kind, id, item.data);
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
          label="Open in Trust"
        />
      );
    }
    if (item.kind === "approval" && item.data.kind === "code_proposal") {
      return <OpenInButton href="/lab?tab=open" label="Open in Lab" />;
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
      // them in one tap. Heartbeat findings have none → Open in Lab.
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
      return <OpenInButton href="/lab?tab=open" label="Open in Lab" />;
    }
    if (item.kind === "followup") {
      // Dismiss sits next to Discuss for follow-ups so the boss can
      // drop a row in one tap. Persistence is server-side (status =
      // 'dismissed' on mem_followups or mem_surface_items); the poller
      // re-poll path won't resurface it.
      return (
        <button
          type="button"
          onClick={dismissFollowup}
          disabled={dismissing}
          className="inline-flex h-10 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-[13px] font-medium text-foreground transition-colors hover:bg-accent disabled:opacity-60"
        >
          <X className={cn("size-3.5", dismissing && "animate-pulse")} aria-hidden />
          {dismissing ? "Dismissing..." : "Dismiss"}
        </button>
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
    return null;
  }

  // Event modals own their entire footer (Are you attending? + RSVP
  // cluster) per the design ref - no Discuss CTA. Every other kind
  // keeps Discuss as the universal trailing primary.
  if (item.kind === "event") {
    return <>{renderSecondary()}</>;
  }
  return (
    <>
      {renderSecondary()}
      <button
        type="button"
        onClick={discuss}
        disabled={seeding}
        className="ml-auto inline-flex h-10 items-center gap-1.5 rounded-md bg-foreground px-4 text-sm font-medium text-background transition-all hover:opacity-90 disabled:opacity-60"
      >
        <Sparkles className={cn("size-3.5", seeding && "animate-pulse")} aria-hidden />
        Discuss with Jarvis
        <ArrowRight className="size-3.5" aria-hidden />
      </button>
    </>
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
  const router = useRouter();
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
    case "activity": return <ActivityBody e={item.data} />;
  }
}

// ── Pursuit ───────────────────────────────────────────────────────────────
function PursuitBody({ p }: { p: Pursuit }) {
  return (
    <div className="pt-3">
      <div className="flex flex-wrap items-center gap-2 text-[11px]">
        <span className="rounded-full bg-muted px-2 py-0.5 font-mono uppercase tracking-wider text-muted-foreground">
          {p.cadence}
        </span>
        {p.streakDays ? (
          <span className="inline-flex items-center gap-1 rounded-full bg-rose-400/10 px-2 py-0.5 font-mono text-rose-400">
            <Flame className="size-3" aria-hidden /> {p.streakDays}d streak
          </span>
        ) : null}
        {p.status ? (
          <span className="font-mono uppercase tracking-wider text-muted-foreground">
            · {p.status.replace("_", " ")}
          </span>
        ) : null}
      </div>
      {p.progress ? (
        <ModalSection meta={`${p.progress.current}/${p.progress.target} ${p.progress.unit ?? ""}`}>
          <div className="space-y-2">
            <div className="h-2 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-foreground"
                style={{ width: `${Math.min(100, Math.round((p.progress.current / p.progress.target) * 100))}%` }}
              />
            </div>
            <p className="text-muted-foreground">
              Progress so far: {p.progress.current} of {p.progress.target}{" "}
              {p.progress.unit ?? "units"}.
            </p>
          </div>
        </ModalSection>
      ) : (
        <ModalSection meta={p.doneToday ? "today: done" : "today: open"}>
          <p className="text-muted-foreground">
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
        <p
          className="pt-2 font-mono text-[11px] uppercase tracking-wider text-muted-foreground"
          suppressHydrationWarning
        >
          created {relTime(p.createdAt)}
        </p>
      ) : null}
    </div>
  );
}

// ── Todo ──────────────────────────────────────────────────────────────────
function TodoBody({ t }: { t: Todo }) {
  return (
    <div className="space-y-3 pt-3">
      <div className="flex flex-wrap items-center gap-2 text-[11px]">
        {t.priority ? (
          <span
            className={cn(
              "rounded-full px-2 py-0.5 font-mono uppercase tracking-wider",
              t.priority === "high"
                ? "bg-danger/10 text-danger"
                : t.priority === "med"
                  ? "bg-rose-400/10 text-rose-400"
                  : "bg-muted text-muted-foreground",
            )}
          >
            {t.priority}
          </span>
        ) : null}
        <span className="rounded-full bg-muted px-2 py-0.5 font-mono uppercase tracking-wider text-muted-foreground">
          {t.source}
        </span>
        {t.dueAt ? (
          <span
            className="font-mono uppercase tracking-wider text-muted-foreground"
            suppressHydrationWarning
          >
            due {dayLabel(t.dueAt).toLowerCase()} · {clockTime(t.dueAt)}
          </span>
        ) : null}
      </div>
      <ModalSection>
        <p className="text-foreground/85">{t.title}</p>
        {t.source === "agent" ? (
          <p className="mt-2 text-[12px] text-muted-foreground">
            Jarvis created this todo based on your recent activity. Discuss to ask why.
          </p>
        ) : null}
      </ModalSection>
    </div>
  );
}

// ── CalendarEvent ─────────────────────────────────────────────────────────
function EventBody({ e }: { e: CalendarEvent }) {
  const openPrep = e.prep.filter((p) => !p.done);
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

      {e.prep.length > 0 ? (
        <ModalSection meta={`${openPrep.length}/${e.prep.length} prep open`}>
          <ul className="space-y-2">
            {e.prep.map((p) => (
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
  return (
    <div className="space-y-3 pt-3">
      <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
        <span className="rounded-full bg-tier-procedural/10 px-2 py-0.5 font-mono uppercase tracking-wider text-tier-procedural">
          metacognition
        </span>
        <span className="font-mono" suppressHydrationWarning>
          {relTime(r.capturedAt)}
        </span>
        <span className="font-mono">· {r.evidenceCount} sources</span>
      </div>
      <ModalSection>
        <p className="leading-relaxed text-foreground/90">{r.body}</p>
      </ModalSection>
    </div>
  );
}

// ── Approval ──────────────────────────────────────────────────────────────
function ApprovalBody({ a }: { a: Approval }) {
  return (
    <div className="space-y-3 pt-3">
      <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
        <span className="rounded-full bg-rose-400/10 px-2 py-0.5 font-mono uppercase tracking-wider text-rose-400">
          {a.kind.replace("_", " ")}
        </span>
        <span className="font-mono" suppressHydrationWarning>
          {relTime(a.createdAt)}
        </span>
        {a.riskLevel ? (
          <span
            className={cn(
              "font-mono uppercase tracking-wider",
              a.riskLevel === "high" || a.riskLevel === "critical"
                ? "text-danger"
                : a.riskLevel === "medium"
                  ? "text-rose-400"
                  : "text-muted-foreground",
            )}
          >
            · risk {a.riskLevel}
          </span>
        ) : null}
      </div>

      {a.rationale ? (
        <p className="text-[13px] leading-relaxed text-foreground/85">{a.rationale}</p>
      ) : null}

      {a.toolCall ? (
        <ModalSection meta={a.toolCall.name}>
          <ModalPre mono>{JSON.stringify(a.toolCall.args, null, 2)}</ModalPre>
        </ModalSection>
      ) : null}

      {a.diff ? (
        <ModalSection meta={a.filePath ?? "patch"}>
          <ModalCode>
            {a.diff.split("\n").map((line, i) => {
              const cls = line.startsWith("+++") || line.startsWith("---")
                ? "text-muted-foreground"
                : line.startsWith("+")
                  ? "text-success"
                  : line.startsWith("-")
                    ? "text-danger"
                    : line.startsWith("@")
                      ? "text-info"
                      : "text-foreground/80";
              return (
                <span key={i} className={cn("block", cls)}>
                  {line}
                </span>
              );
            })}
          </ModalCode>
        </ModalSection>
      ) : null}

      {a.question ? (
        <ModalSection meta="Jarvis asks">
          <p className="text-[13px] leading-relaxed text-foreground/90">{a.question}</p>
          {a.context ? (
            // whitespace-pre-line so the multi-line diagnosis (what it does /
            // what it was doing / the real error / suggested fix) keeps its
            // line breaks instead of collapsing into a run-on.
            <p className="mt-2 whitespace-pre-line text-[12px] leading-relaxed text-muted-foreground">
              {a.context}
            </p>
          ) : null}
        </ModalSection>
      ) : null}
    </div>
  );
}

// ── FollowUp ──────────────────────────────────────────────────────────────
function FollowUpBody({ f }: { f: FollowUp }) {
  const SourceIcon =
    f.source === "gmail"
      ? AtSign
      : f.source === "slack"
        ? Hash
        : f.source === "imessage"
          ? MessageCircle
          : Inbox;

  // Triage chips. classification / intent / mode are separate metadata axes
  // that often overlap — intent "needs reply" + mode "reply" are the SAME
  // thing shown twice. dedupeChips collapses them: it drops any value
  // contained in a longer sibling (so "reply" disappears next to "needs
  // reply") and any exact duplicate.
  const triageChips = dedupeChips([
    { v: metaStr(f.metadata, "classification", "category"), tone: classificationTone },
    { v: metaStr(f.metadata, "intent"), tone: intentTone },
    { v: metaStr(f.metadata, "mode", "action"), tone: modeTone },
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

  return (
    <div className="space-y-1 pt-3">
      {/* Source · account · time + triage chips */}
      <ModalChips>
        <Chip tone="muted" icon={<SourceIcon className="size-3" aria-hidden />}>
          {f.source}
        </Chip>
        {f.account ? <Chip tone="muted">{f.account}</Chip> : null}
        {triageChips.map((c) => (
          <Chip key={c.v} tone={c.tone}>
            {c.v}
          </Chip>
        ))}
        <span className="ml-auto font-mono text-[11px] text-muted-foreground" suppressHydrationWarning>
          {/* Modal shows the OFFICIAL received date in full (received_at from
              the real inbox), falling back to the full found-date — never the
              listing's relative "Nd ago". */}
          {receivedReal || fullDateTime(f.receivedAt)}
        </span>
      </ModalChips>

      {/* From / Subject - each a flex row with symmetric padding +
          items-center so the label and value sit dead-center in their row.
          mt-10 (40px) gives a clearly visible gap below the chip row so the
          meta chips don't crowd the FROM line. */}
      <div className="mt-10 overflow-hidden rounded-lg border border-border bg-muted/20">
        <div className="flex items-center gap-4 px-3 py-3">
          <span className="w-20 shrink-0 font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
            From
          </span>
          <span className="min-w-0 flex-1 break-words text-[13px] font-medium text-foreground">
            {f.from}
          </span>
        </div>
        {f.subject ? (
          <div className="flex items-center gap-4 border-t border-border px-3 py-3">
            <span className="w-20 shrink-0 font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
              Subject
            </span>
            <span className="min-w-0 flex-1 break-words text-[13px] text-foreground/90">
              {f.subject}
            </span>
          </div>
        ) : null}
      </div>

      {/* One-tap actions (Draft reply / Archive / Snooze …). Tapping seeds an
          agent turn from the action's intent — "Draft reply" drafts only, never
          sends. Renders nothing when the item carries no actions. */}
      <SurfaceActionRow itemId={f.id} actions={f.actions} className="flex flex-wrap gap-2 pt-1" />


      {/* Context (summary) - ABOVE the email, under From/Subject. Stays
          silent when there's no triage summary (raw poll rows). */}
      {f.summary?.trim() ? (
        <ModalSection
          label="Context"
          icon={<Sparkles className="size-3.5 shrink-0 text-brand" aria-hidden />}
          className="border-brand/20 bg-brand/[0.04]"
        >
          <ModalPre>{f.summary.trim()}</ModalPre>
        </ModalSection>
      ) : null}

      {/* Message - the real email, rendered as HTML when available. */}
      <ModalSection
        label="Message"
        icon={<Mail className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />}
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
            <Loader2 className="size-3 animate-spin" aria-hidden /> loading full message…
          </p>
        ) : null}
      </ModalSection>

      {f.draft ? (
        <ModalSection
          label="Draft"
          icon={<Sparkles className="size-3.5 shrink-0 text-brand" aria-hidden />}
          meta="Jarvis drafted"
        >
          <ModalPre>{f.draft}</ModalPre>
        </ModalSection>
      ) : null}
    </div>
  );
}

// dedupeChips collapses the overlapping triage chips. A value is dropped when
// a longer sibling already contains it (so "reply" vanishes next to "needs
// reply") or when it's an exact duplicate; each surviving chip keeps its tone.
function dedupeChips(
  defs: { v: string; tone: (v: string) => ChipTone }[],
): { v: string; tone: ChipTone }[] {
  const present = defs.filter((d) => d.v.trim() !== "");
  const out: { v: string; tone: ChipTone }[] = [];
  for (const d of present) {
    const lv = d.v.toLowerCase();
    const dominated = present.some(
      (o) => o.v !== d.v && o.v.toLowerCase().includes(lv) && o.v.length > d.v.length,
    );
    if (dominated) continue;
    if (out.some((s) => s.v.toLowerCase() === lv)) continue;
    out.push({ v: d.v, tone: d.tone(d.v) });
  }
  return out;
}

// Pull a string-valued field from a follow-up's metadata bag.
function metaStr(m: Record<string, unknown> | undefined, ...keys: string[]): string {
  if (!m) return "";
  for (const k of keys) {
    const v = m[k];
    if (typeof v === "string" && v.trim()) return v.trim();
  }
  return "";
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
  return (
    <div className="mt-3 flex flex-wrap gap-2">
      {attachments.map((att, i) => (
        <span
          key={`${att.id || att.name}-${i}`}
          className="inline-flex h-8 max-w-full items-center gap-1.5 rounded-md border border-border bg-muted/40 px-2.5 text-[12px] text-foreground/85"
          title={att.name}
        >
          <Paperclip className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
          <span className="min-w-0 truncate">{att.name}</span>
        </span>
      ))}
    </div>
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
function SurfaceBody({ item }: { item: SurfaceItem }) {
  const fields = parseLabeledBody(item.body);
  // When the body parses cleanly into fields, drop the "Why it matters"
  // duplicate from inline rendering since we render importanceReason
  // above as a hero italic. Same for "Account" if it's already shown
  // in the calm header subtitle - actually keep Account, the boss
  // wants every contract field visible.
  const visibleFields = fields.filter(
    (f) => f.label.toLowerCase() !== "why it matters" || !item.importanceReason,
  );

  return (
    <div className="space-y-4 pt-4">
      {item.importanceReason ? (
        <p className="text-[13.5px] italic leading-relaxed text-muted-foreground">
          {item.importanceReason}
        </p>
      ) : null}

      {visibleFields.length > 0 ? (
        <div className="divide-y divide-border rounded-lg border bg-card">
          <div className="px-4 sm:px-5">
            {visibleFields.map((f) => (
              <ModalField key={f.label} label={f.label}>
                {f.value.split("\n").map((line, i, arr) => (
                  <React.Fragment key={i}>
                    {line}
                    {i < arr.length - 1 ? <br /> : null}
                  </React.Fragment>
                ))}
              </ModalField>
            ))}
          </div>
        </div>
      ) : item.body ? (
        // Body didn't parse into labelled fields - render as prose so we
        // still show what Jarvis wrote, without the faux-card chrome.
        <p className="whitespace-pre-wrap break-words text-[13.5px] leading-relaxed text-foreground/90">
          {item.body}
        </p>
      ) : null}

      {item.url ? (
        <ModalUrl href={item.url} icon={<ExternalLink className="size-3.5" aria-hidden />}>
          {item.url}
        </ModalUrl>
      ) : null}
    </div>
  );
}

// ── Saved ─────────────────────────────────────────────────────────────────
function SavedBody({ s }: { s: Saved }) {
  return (
    <div className="space-y-3 pt-3">
      <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
        <span className="rounded-full bg-muted px-2 py-0.5 font-mono uppercase tracking-wider">
          {s.kind}
        </span>
        {s.readingMinutes ? (
          <span className="font-mono">· {s.readingMinutes} min read</span>
        ) : null}
        <span className="font-mono" suppressHydrationWarning>
          · saved {relTime(s.savedAt)}
        </span>
      </div>
      {s.source ? (
        <p className="break-words text-[12px] text-muted-foreground">{s.source}</p>
      ) : null}
      {s.body ? (
        <ModalSection>
          <ModalPre>{s.body}</ModalPre>
        </ModalSection>
      ) : null}
      {s.url ? (
        <ModalUrl href={s.url} icon={<ExternalLink className="size-3.5" aria-hidden />}>
          {s.url}
        </ModalUrl>
      ) : null}
    </div>
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

  return (
    <div className="space-y-3 pt-3">
      <ModalChips>
        <span className="rounded-full bg-muted px-2 py-0.5 font-mono uppercase tracking-wider">
          {w.column}
        </span>
        {w.engine ? (
          <span className="rounded-full bg-muted px-2 py-0.5 font-mono uppercase tracking-wider">
            {w.engine}
          </span>
        ) : null}
        {w.durationMs ? (
          <span className="font-mono">· {formatDuration(w.durationMs)}</span>
        ) : null}
      </ModalChips>

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

      {w.detailHref ? (
        <ModalUrl
          href={w.detailHref}
          external={false}
          icon={<ExternalLink className="size-3.5" aria-hidden />}
        >
          open in {w.detailHref}
        </ModalUrl>
      ) : null}
    </div>
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
function ActivityBody({ e }: { e: ActivityEv }) {
  return (
    <div className="space-y-3 pt-3">
      <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
        <span className="rounded-full bg-muted px-2 py-0.5 font-mono uppercase tracking-wider">
          {e.kind}
        </span>
        <span className="font-mono" suppressHydrationWarning>
          · {e.future ? `in ${dayLabel(e.at).toLowerCase()}` : relTime(e.at)}
        </span>
      </div>
      {e.detail ? <p className="text-[13px] leading-relaxed text-foreground/85">{e.detail}</p> : null}
    </div>
  );
}

// ── header meta dispatch ─────────────────────────────────────────────────

function headerMeta(item: DashboardItem): {
  Icon: React.ComponentType<{ className?: string }>;
  label: string;
  tone: string;
} {
  switch (item.kind) {
    case "pursuit":
      return { Icon: Target, label: "Pursuit", tone: "border-border bg-muted text-foreground" };
    case "todo":
      return { Icon: ListTodo, label: "Todo", tone: "border-border bg-muted text-foreground" };
    case "event":
      return { Icon: Calendar, label: "Calendar event", tone: "border-border bg-muted text-foreground" };
    case "reflection":
      return { Icon: Brain, label: "Reflection", tone: "border-tier-procedural/40 bg-tier-procedural/10 text-tier-procedural" };
    case "approval":
      if (item.data.kind === "code_proposal")
        return { Icon: FileCode, label: "Code proposal", tone: "border-info/40 bg-info/10 text-info" };
      if (item.data.kind === "curiosity")
        return { Icon: HelpCircle, label: "Curiosity", tone: "border-info/40 bg-info/10 text-info" };
      return { Icon: Terminal, label: "Approval", tone: "border-rose-400/40 bg-rose-400/10 text-rose-400" };
    case "followup":
      return { Icon: Inbox, label: "Follow-up", tone: "border-border bg-muted text-foreground" };
    case "surface":
      return {
        Icon: Sparkles,
        label: surfaceKindLabel(item.data.surface),
        tone:
          (item.data.importance ?? 0) >= 80
            ? "border-danger/40 bg-danger/10 text-danger"
            : "border-border bg-muted text-foreground",
      };
    case "work":
      return { Icon: Layers, label: "Agent work", tone: "border-border bg-muted text-foreground" };
    case "saved":
      return { Icon: item.data.kind === "quote" ? Quote : Sparkles, label: "Saved", tone: "border-border bg-muted text-foreground" };
    case "activity":
      return { Icon: Activity, label: "Activity", tone: "border-border bg-muted text-foreground" };
  }
}

// Keep this import referenced so tree-shaking doesn't complain about an
// unused declaration in the union-meta switch above.
void Loader2;
