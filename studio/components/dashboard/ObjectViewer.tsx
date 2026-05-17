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
  Layers,
  ListTodo,
  Loader2,
  MapPin,
  MessageCircle,
  Quote,
  Sparkles,
  Target,
  Terminal,
} from "lucide-react";
import {
  ResponsiveModal,
  ResponsiveModalHeader,
} from "@/components/ui/responsive-modal";
import {
  ModalChips,
  ModalCode,
  ModalDl,
  ModalField,
  ModalPre,
  ModalSection,
  ModalUrl,
} from "@/components/ui/modal-content";
import { cn } from "@/lib/utils";
import { clockTime, dayLabel, formatDuration, relTime } from "@/lib/dashboard/format";
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
  // All overflow / Dialog-vs-Drawer / a11y handling is owned by
  // ResponsiveModal. ObjectViewer just composes the header, body, and
  // footer slots - same shape it would have in either form.
  return (
    <ResponsiveModal
      open={open}
      onOpenChange={(o) => (!o ? onClose() : null)}
      size="lg"
      title={item ? getViewerTitle(item) : "Item"}
      description={item ? getViewerKindLabel(item) : undefined}
      header={item ? <ItemHeader item={item} /> : undefined}
      footer={item ? <ViewerActions item={item} onResolved={onResolved} /> : undefined}
    >
      <AnimatePresence mode="wait">
        {item ? <ViewerBody key={getViewerKey(item)} item={item} /> : null}
      </AnimatePresence>
    </ResponsiveModal>
  );
}

function ItemHeader({ item }: { item: DashboardItem }) {
  const { Icon, label, tone } = headerMeta(item);
  // Surface items, follow-ups, saved, and reflections render the "calm
  // preview" header shape: no loud uppercase eyebrow above the title,
  // title grows to 2 lines for long subjects, and the kind / source /
  // time appear as a muted subtitle UNDER the title. Other kinds
  // (approval, work, todo, event, pursuit) keep the original compact
  // header where the eyebrow tells you what's about to happen.
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
}: {
  item: DashboardItem;
  // onResolved was the old inline-action hook; preview-only mode (per
  // the IA defrag) doesn't need it since every action navigates away.
  onResolved?: (item: DashboardItem) => void;
}) {
  // Every item gets a "Discuss with Jarvis" primary CTA. Kind-specific
  // secondary actions are preview-only "Open in <surface>" deep-links
  // (per the IA defrag), no inline approve/reject here anymore.
  const router = useRouter();
  const [seeding, setSeeding] = React.useState(false);

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
    if (item.kind === "activity") {
      // Heartbeat findings + curiosity questions surface in Activity.
      // All actionable in Lab Fix-this.
      return <OpenInButton href="/lab?tab=open" label="Open in Lab" />;
    }
    return null;
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
  return (
    <div className="space-y-3 pt-3">
      <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
        <span className="rounded-full bg-muted px-2 py-0.5 font-mono uppercase tracking-wider">
          {e.classification}
        </span>
        <span className="font-mono" suppressHydrationWarning>
          {dayLabel(e.startsAt)} · {clockTime(e.startsAt)}
          {e.endsAt ? ` – ${clockTime(e.endsAt)}` : ""}
        </span>
      </div>
      {e.location ? (
        <p className="flex items-center gap-1.5 text-[12px] text-muted-foreground">
          <MapPin className="size-3.5" aria-hidden />
          {e.location}
        </p>
      ) : null}
      {e.prep.length > 0 ? (
        <ModalSection meta={`${openPrep.length}/${e.prep.length} prep open`}>
          <ul className="space-y-2">
            {e.prep.map((p) => (
              <li key={p.id} className="flex items-start gap-2">
                {p.done ? (
                  <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-success" aria-hidden />
                ) : (
                  <Circle className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
                )}
                <div className="min-w-0 flex-1">
                  <p
                    className={cn(
                      "text-[13px]",
                      p.done ? "text-muted-foreground line-through" : "text-foreground",
                    )}
                  >
                    {p.label}
                  </p>
                  {p.rationale ? (
                    <p className="mt-0.5 text-[11px] italic text-muted-foreground">{p.rationale}</p>
                  ) : null}
                </div>
              </li>
            ))}
          </ul>
        </ModalSection>
      ) : (
        <p className="text-[12px] text-muted-foreground">
          No prep items for this event - Jarvis didn&apos;t flag anything you need to do beforehand.
        </p>
      )}
    </div>
  );
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
            <p className="mt-2 text-[11px] italic text-muted-foreground">{a.context}</p>
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
  return (
    <div className="space-y-3 pt-3">
      <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
        <span className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 font-mono uppercase tracking-wider">
          <SourceIcon className="size-3" aria-hidden /> {f.source}
        </span>
        {f.account ? <span className="font-mono">· {f.account}</span> : null}
        <span className="font-mono" suppressHydrationWarning>
          · {relTime(f.receivedAt)}
        </span>
      </div>
      <div className="space-y-1">
        <p className="text-[13px] font-medium text-foreground">From: {f.from}</p>
        {f.subject ? (
          <p className="text-[13px] text-foreground/85">Subject: {f.subject}</p>
        ) : null}
      </div>
      <ModalSection
        meta={
          f.threadUrl ? (
            <ModalUrl href={f.threadUrl} icon={<ExternalLink className="size-3" aria-hidden />}>
              open in {f.source}
            </ModalUrl>
          ) : null
        }
      >
        <ModalPre>{f.body ?? f.preview}</ModalPre>
      </ModalSection>
      {f.draft ? (
        <ModalSection meta="Jarvis drafted">
          <ModalPre>{f.draft}</ModalPre>
        </ModalSection>
      ) : null}
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

  return (
    <div className="space-y-3 pt-3">
      <ModalChips>
        <span className="rounded-full bg-muted px-2 py-0.5 font-mono uppercase tracking-wider">
          {w.column}
        </span>
        <span className="rounded-full bg-muted px-2 py-0.5 font-mono uppercase tracking-wider">
          {w.kind.replace("_", " ")}
        </span>
        {w.durationMs ? (
          <span className="font-mono">· {formatDuration(w.durationMs)}</span>
        ) : null}
      </ModalChips>

      {/* Subtitle either renders as the danger-toned Error card (cron
          failure / workflow step error) or as a neutral Output card.
          Either way it goes inside a ModalSection so the padding,
          overflow, and break-word discipline come from the primitive
          rather than a hand-rolled <p>. */}
      {w.subtitle ? (
        <ModalSection label={isError ? "Error" : "Output"} tone={isError ? "error" : "default"}>
          <ModalPre mono={isError}>{w.subtitle}</ModalPre>
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

// Keep these imports referenced so tree-shaking doesn't complain about
// unused declarations in the union-meta switch above.
void Loader2;
void AlertTriangle;
