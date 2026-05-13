"use client";

import * as React from "react";
import Link from "next/link";
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
  Clock4,
  ExternalLink,
  Eye,
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
  Trash2,
  X,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import {
  Drawer,
  DrawerContent,
  DrawerTitle,
  DrawerDescription,
} from "@/components/ui/drawer";
import { cn } from "@/lib/utils";
import { clockTime, dayLabel, formatDuration, relTime } from "@/lib/dashboard/format";
import { useIsDesktop } from "@/lib/dashboard/use-is-desktop";
import { seedSession } from "@/lib/dashboard/seed";
import type {
  Approval,
  CalendarEvent,
  DashboardItem,
  FollowUp,
  Pursuit,
  Reflection,
  Saved,
  Todo,
  ActivityEvent as ActivityEv,
  WorkItem,
} from "@/lib/dashboard/types";

/* ObjectViewer — the responsive preview surface for every dashboard item.
 *
 * Per the ObjectViewer pattern:
 *  - Dialog on lg+, Drawer on <lg.
 *  - Renders the artifact in its native form (full email body, full diff,
 *    full event card, etc.) — *not* a summary.
 *  - "Discuss with Jarvis" is the only path to a seeded /live session.
 *  - Many items also surface kind-specific quick actions (approve, snooze,
 *    mark done, dismiss) so the user can act without ever opening a chat.
 */
export function ObjectViewer({
  item,
  onClose,
}: {
  item: DashboardItem | null;
  onClose: () => void;
}) {
  // Only one primitive mounts at a time. Rendering both means both
  // portals stack their overlays at <body> level (each with backdrop-blur)
  // and the modal looks washed out behind the second overlay. matchMedia
  // gives us a single source of truth for which primitive to mount.
  const isDesktop = useIsDesktop();
  const open = item !== null;
  if (isDesktop) {
    return (
      <Dialog open={open} onOpenChange={(o) => (!o ? onClose() : null)}>
        <DialogContent className="flex max-h-[90dvh] w-[min(96vw,640px)] max-w-none flex-col overflow-hidden p-0">
          <DialogTitle className="sr-only">{item ? getViewerTitle(item) : "Item"}</DialogTitle>
          <DialogDescription className="sr-only">
            {item ? getViewerKindLabel(item) : ""}
          </DialogDescription>
          <AnimatePresence mode="wait">
            {item ? <ViewerBody key={getViewerKey(item)} item={item} /> : null}
          </AnimatePresence>
        </DialogContent>
      </Dialog>
    );
  }
  return (
    <Drawer open={open} onOpenChange={(o) => (!o ? onClose() : null)}>
      <DrawerContent>
        <DrawerTitle className="sr-only">{item ? getViewerTitle(item) : "Item"}</DrawerTitle>
        <DrawerDescription className="sr-only">
          {item ? getViewerKindLabel(item) : ""}
        </DrawerDescription>
        <AnimatePresence mode="wait">
          {item ? (
            <ViewerBody key={getViewerKey(item)} item={item} layout="drawer" />
          ) : null}
        </AnimatePresence>
      </DrawerContent>
    </Drawer>
  );
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
    case "work": return "Agent work item";
    case "saved": return "Saved item";
    case "activity": return "Activity event";
  }
}

function ViewerBody({
  item,
  layout = "dialog",
}: {
  item: DashboardItem;
  layout?: "dialog" | "drawer";
}) {
  // No `onClose` here — Dialog/Drawer wrappers in ObjectViewer drive the
  // close action via their own onOpenChange. The X icon that used to
  // sit in the header moved out when the seeded-session CTA rewrite
  // shifted the close affordance to the modal chrome.
  return (
    <motion.div
      initial={{ opacity: 0, y: layout === "drawer" ? 0 : 4 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.18, ease: [0.2, 0.7, 0.2, 1] }}
      className="flex h-full max-h-full min-h-0 flex-col"
    >
      <ViewerHeader item={item} layout={layout} />
      <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4 scroll-touch sm:px-5">
        <ViewerContent item={item} />
      </div>
      <ViewerActions item={item} />
    </motion.div>
  );
}

function ViewerHeader({
  item,
  layout,
}: {
  item: DashboardItem;
  layout: "dialog" | "drawer";
}) {
  const { Icon, label, tone } = headerMeta(item);
  return (
    <header
      className={cn(
        "flex items-center gap-2 border-b px-4 sm:px-5",
        layout === "drawer" ? "pb-3 pt-3" : "pb-3 pt-4",
      )}
    >
      <span
        className={cn(
          "flex size-8 shrink-0 items-center justify-center rounded-md border",
          tone,
        )}
      >
        <Icon className="size-4" aria-hidden />
      </span>
      <div className="min-w-0 flex-1">
        <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
          {label}
        </p>
        <h2 className="truncate text-base font-semibold tracking-tight text-foreground">
          {getViewerTitle(item)}
        </h2>
      </div>
    </header>
  );
}

function ViewerActions({ item }: { item: DashboardItem }) {
  // Every item gets a "Discuss with Jarvis" primary CTA. Kind-specific
  // secondary actions (approve, reject, snooze, mark done, dismiss) live
  // to the left of it.
  const router = useRouter();
  const [seeding, setSeeding] = React.useState(false);

  async function discuss() {
    const id = (item.data as { id?: string }).id ?? "";
    setSeeding(true);
    try {
      const sessionId = await seedSession(item.kind, id);
      if (sessionId) {
        router.push(`/live?session=${encodeURIComponent(sessionId)}`);
      } else {
        // Seed failed — degrade to unseeded /live so the boss can still
        // start a chat. ObjectViewer will close as the route changes.
        router.push("/live");
      }
    } finally {
      setSeeding(false);
    }
  }

  function renderSecondary(): React.ReactNode {
    switch (item.kind) {
      case "approval":
        if (item.data.kind.startsWith("trust_")) {
          return (
            <>
              <SecondaryButton tone="success" Icon={CheckCircle2}>
                Approve
              </SecondaryButton>
              <SecondaryButton tone="danger" Icon={X}>
                Reject
              </SecondaryButton>
            </>
          );
        }
        if (item.data.kind === "code_proposal") {
          return (
            <>
              <SecondaryButton tone="success" Icon={CheckCircle2}>
                Approve
              </SecondaryButton>
              <SecondaryButton tone="muted" Icon={Clock4}>
                Snooze
              </SecondaryButton>
            </>
          );
        }
        return null;
      case "todo":
        return (
          <SecondaryButton tone="success" Icon={CheckCircle2}>
            Mark done
          </SecondaryButton>
        );
      case "followup":
        return (
          <>
            <SecondaryButton tone="muted" Icon={Clock4}>
              Snooze
            </SecondaryButton>
            <SecondaryButton tone="muted" Icon={Trash2}>
              Dismiss
            </SecondaryButton>
          </>
        );
      case "saved":
        return <SecondaryButton tone="muted" Icon={Trash2}>Remove</SecondaryButton>;
      default:
        return null;
    }
  }

  return (
    <footer className="flex shrink-0 flex-wrap items-center justify-end gap-2 border-t bg-muted/20 px-4 py-4 sm:px-5 sm:py-4 pb-safe">
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
    </footer>
  );
}

function SecondaryButton({
  Icon,
  tone,
  children,
}: {
  Icon: React.ComponentType<{ className?: string }>;
  tone: "success" | "danger" | "muted";
  children: React.ReactNode;
}) {
  const cls =
    tone === "success"
      ? "border-success/40 text-success hover:bg-success/10"
      : tone === "danger"
        ? "border-danger/40 text-danger hover:bg-danger/10"
        : "border-border text-muted-foreground hover:bg-accent hover:text-foreground";
  return (
    <button
      type="button"
      className={cn(
        "inline-flex h-10 items-center gap-1.5 rounded-md border bg-background px-3 text-[13px] font-medium transition-colors",
        cls,
      )}
    >
      <Icon className="size-3.5" aria-hidden />
      {children}
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
    case "work": return <WorkBody w={item.data} />;
    case "saved": return <SavedBody s={item.data} />;
    case "activity": return <ActivityBody e={item.data} />;
  }
}

function ContextBlock({
  children,
  meta,
}: {
  children: React.ReactNode;
  meta?: React.ReactNode;
}) {
  return (
    <div className="mt-4 rounded-lg border bg-muted/30">
      <header className="flex items-center gap-2 border-b bg-muted/40 px-3 py-2">
        <Eye className="size-3.5 text-muted-foreground" aria-hidden />
        <span className="font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
          Context
        </span>
        {meta ? <span className="ml-auto text-[11px] text-muted-foreground">{meta}</span> : null}
      </header>
      <div className="p-3 text-[13px] leading-relaxed">{children}</div>
    </div>
  );
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
        <ContextBlock meta={`${p.progress.current}/${p.progress.target} ${p.progress.unit ?? ""}`}>
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
        </ContextBlock>
      ) : (
        <ContextBlock meta={p.doneToday ? "today: done" : "today: open"}>
          <p className="text-muted-foreground">
            {p.doneToday
              ? `Checked in today${p.doneAt ? ` at ${clockTime(p.doneAt)}` : ""}.`
              : "Not yet completed today."}
            {p.streakDays
              ? ` Current streak is ${p.streakDays} day${p.streakDays === 1 ? "" : "s"}.`
              : ""}
          </p>
        </ContextBlock>
      )}
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
      <ContextBlock>
        <p className="text-foreground/85">{t.title}</p>
        {t.source === "agent" ? (
          <p className="mt-2 text-[12px] text-muted-foreground">
            Jarvis created this todo based on your recent activity. Discuss to ask why.
          </p>
        ) : null}
      </ContextBlock>
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
        <ContextBlock meta={`${openPrep.length}/${e.prep.length} prep open`}>
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
        </ContextBlock>
      ) : (
        <p className="text-[12px] text-muted-foreground">
          No prep items for this event — Jarvis didn&apos;t flag anything you need to do beforehand.
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
      <ContextBlock>
        <p className="leading-relaxed text-foreground/90">{r.body}</p>
      </ContextBlock>
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
        <ContextBlock meta={a.toolCall.name}>
          <pre className="overflow-x-auto whitespace-pre-wrap break-words font-mono text-[12px] leading-relaxed text-foreground">
            {JSON.stringify(a.toolCall.args, null, 2)}
          </pre>
        </ContextBlock>
      ) : null}

      {a.diff ? (
        <ContextBlock meta={a.filePath ?? "patch"}>
          <pre className="overflow-x-auto whitespace-pre font-mono text-[11px] leading-relaxed">
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
          </pre>
        </ContextBlock>
      ) : null}

      {a.question ? (
        <ContextBlock meta="Jarvis asks">
          <p className="text-[13px] leading-relaxed text-foreground/90">{a.question}</p>
          {a.context ? (
            <p className="mt-2 text-[11px] italic text-muted-foreground">{a.context}</p>
          ) : null}
        </ContextBlock>
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
      <ContextBlock meta={f.threadUrl ? <a href={f.threadUrl} className="hover:underline" target="_blank" rel="noreferrer">open in {f.source} <ExternalLink className="inline size-3" /></a> : null}>
        <pre className="whitespace-pre-wrap break-words font-sans text-[13px] leading-relaxed text-foreground/90">
          {f.body ?? f.preview}
        </pre>
      </ContextBlock>
      {f.draft ? (
        <ContextBlock meta="Jarvis drafted">
          <pre className="whitespace-pre-wrap break-words font-sans text-[13px] leading-relaxed text-foreground/90">
            {f.draft}
          </pre>
        </ContextBlock>
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
      {s.source ? <p className="text-[12px] text-muted-foreground">{s.source}</p> : null}
      {s.body ? (
        <ContextBlock>
          <p className="whitespace-pre-wrap break-words text-foreground/90">{s.body}</p>
        </ContextBlock>
      ) : null}
      {s.url ? (
        <a
          href={s.url}
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1 text-[12px] text-info hover:underline"
        >
          <ExternalLink className="size-3.5" aria-hidden />
          {s.url}
        </a>
      ) : null}
    </div>
  );
}

// ── Work ──────────────────────────────────────────────────────────────────
function WorkBody({ w }: { w: WorkItem }) {
  return (
    <div className="space-y-3 pt-3">
      <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
        <span className="rounded-full bg-muted px-2 py-0.5 font-mono uppercase tracking-wider">
          {w.column}
        </span>
        <span className="rounded-full bg-muted px-2 py-0.5 font-mono uppercase tracking-wider">
          {w.kind.replace("_", " ")}
        </span>
        {w.durationMs ? (
          <span className="font-mono">· {formatDuration(w.durationMs)}</span>
        ) : null}
      </div>
      {w.subtitle ? (
        <p className="text-[13px] leading-relaxed text-foreground/85">{w.subtitle}</p>
      ) : null}
      <ContextBlock>
        <dl className="grid grid-cols-2 gap-y-1 text-[12px]">
          {w.scheduledFor ? (
            <Row label="scheduled" value={clockTime(w.scheduledFor)} />
          ) : null}
          {w.startedAt ? <Row label="started" value={clockTime(w.startedAt)} /> : null}
          {w.finishedAt ? <Row label="finished" value={clockTime(w.finishedAt)} /> : null}
        </dl>
      </ContextBlock>
      {w.detailHref ? (
        <Link
          href={w.detailHref}
          className="inline-flex items-center gap-1 text-[12px] text-info hover:underline"
        >
          <ExternalLink className="size-3.5" aria-hidden />
          open in {w.detailHref}
        </Link>
      ) : null}
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className="font-mono uppercase tracking-wider text-muted-foreground">{label}</dt>
      <dd className="text-right font-mono text-foreground" suppressHydrationWarning>
        {value}
      </dd>
    </>
  );
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
