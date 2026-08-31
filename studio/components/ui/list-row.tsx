"use client";

import * as React from "react";
import Link from "next/link";
import { ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * ListRow / GroupLabel / WorkRow — the row vocabulary (Majordomo §5).
 *
 * The contract these three share, and the reason they exist as primitives:
 *
 *  - **Rows are separated by a hairline and space, never by a border box.**
 *    Rows no longer draw a hairline each; they separate by rhythm. The LAST row in a
 *    group drops it, so a list needs no wrapper chrome at all. A bordered or
 *    rounded list row is a bug (§1.2).
 *  - **44px minimum touch target** (`min-h-11`) on every row, tappable or
 *    not, so a list never becomes a cramped strip on a phone.
 *  - **The row owns its own overflow.** `min-w-0` on the row and on the text
 *    column, `truncate` on title and meta. A long id or URL can never widen
 *    the page — the 375px no-horizontal-scroll rule is enforced here once
 *    rather than in every consumer.
 *  - **Tone is a 7px dot, not a background.** Colour marks one thing: brand
 *    = alive, warning = waiting on the boss, danger = broken (§1.4). Rows do
 *    not get tinted grounds.
 *  - **Voice vs chrome.** `voice` sets the title in the voice face — use it
 *    when the row's title is something Jarvis said or wrote. Everything else
 *    (labels, filenames, settings) stays chrome.
 *
 * Element choice is automatic: `href` renders a `<Link>`, `onClick` renders a
 * `<button>`, neither renders a `<div>`. Consumers never hand-roll that.
 */

export type RowTone =
  | "default"
  | "quiet"
  | "brand"
  | "accent"
  | "success"
  | "info"
  | "warning"
  | "danger";

/** Dot colour per tone. `default`/`accent` map to the same neutral resting glyph. */
const DOT_TONE: Record<RowTone, string> = {
  default: "bg-quiet",
  quiet: "bg-quiet",
  brand: "bg-brand",
  accent: "bg-brand",
  success: "bg-success",
  info: "bg-info",
  warning: "bg-warning",
  danger: "bg-danger",
};

/**
 * StatusDot — the 7px tone dot ListRow renders when no explicit `leading` is
 * given. Exported so WorkRow, the activity ledger, and modals use the exact
 * same glyph instead of each inventing a dot size.
 */
export function StatusDot({
  tone = "default",
  pulse,
  className,
}: {
  tone?: RowTone;
  /** Only for the one thing happening right now. */
  pulse?: boolean;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "size-[7px] shrink-0 rounded-full",
        DOT_TONE[tone],
        pulse && "animate-pulse-soft",
        className,
      )}
      aria-hidden
    />
  );
}

export interface ListRowProps {
  /** Glyph or custom node in the leading slot. Omit to get the tone dot. */
  leading?: React.ReactNode;
  /**
   * An INTERACTIVE control in the leading slot: a todo checkbox, a habit tick.
   *
   * It renders OUTSIDE the row's own `<button>`/`<Link>`, as a sibling, because
   * a button inside a button is invalid HTML and iOS Safari resolves the tap to
   * the wrong target. Consumers used to hand-roll the whole row just to get a
   * checkbox next to a tappable title; this is that shape, once. When set,
   * `leading` and the tone dot are not rendered — the control IS the glyph.
   */
  leadingAction?: React.ReactNode;
  title: React.ReactNode;
  /** Quiet second line (or inline suffix when short): timestamp, count, path. */
  meta?: React.ReactNode;
  /** Right-aligned node: a chip, a number, a control. */
  trailing?: React.ReactNode;
  onClick?: () => void;
  href?: string;
  tone?: RowTone;
  /** Set the title in the voice face — true when Jarvis authored the title. */
  voice?: boolean;
  /** Pulse the tone dot. Reserved for the one live thing. */
  live?: boolean;
  /** Show the affordance chevron. Defaults to true for tappable rows. */
  chevron?: boolean;
  /** Drop the bottom hairline (e.g. the row is last and the group has none). */
  /** Accepted for compatibility. Rows no longer draw a rule at all. */
  noRule?: boolean;
  disabled?: boolean;
  className?: string;
  /** Opened detail (an `<Inset>`), rendered under the row inside the same rule. */
  children?: React.ReactNode;
}

export function ListRow({
  leading,
  leadingAction,
  title,
  meta,
  trailing,
  onClick,
  href,
  tone = "default",
  voice,
  live,
  chevron,
  disabled,
  className,
  children,
}: ListRowProps) {
  const tappable = Boolean(onClick || href) && !disabled;
  const showChevron = chevron ?? tappable;

  const inner = (
    <>
      {leadingAction ? null : (
        <span className="flex size-[18px] shrink-0 items-center justify-center text-quiet">
          {leading ?? <StatusDot tone={tone} pulse={live} />}
        </span>
      )}
      <span className="flex min-w-0 flex-1 flex-col gap-0.5 text-left">
        <span
          className={cn(
            "min-w-0 truncate",
            voice
              ? "font-voice text-[15.5px] leading-[1.55] text-foreground"
              : "font-sans text-[13.5px] font-medium text-foreground",
          )}
        >
          {title}
        </span>
        {meta ? (
          <span className="min-w-0 truncate text-[12px] tabular-nums text-quiet">{meta}</span>
        ) : null}
      </span>
      {trailing ? <span className="flex shrink-0 items-center gap-2">{trailing}</span> : null}
      {showChevron ? (
        <ChevronRight className="size-4 shrink-0 text-quiet" aria-hidden />
      ) : null}
    </>
  );

  const rowClass = cn(
    "flex min-h-11 w-full min-w-0 max-w-full items-center gap-3 py-2.5 text-left",
    tappable && "transition-colors hover:bg-accent/60 active:bg-accent",
    tappable &&
      "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 focus-visible:ring-offset-0",
    disabled && "opacity-60",
  );

  const body =
    href && !disabled ? (
      <Link href={href} className={rowClass}>
        {inner}
      </Link>
    ) : onClick ? (
      <button type="button" onClick={onClick} disabled={disabled} className={rowClass}>
        {inner}
      </button>
    ) : (
      <div className={rowClass}>{inner}</div>
    );

  return (
    <div
      className={cn(
        "min-w-0 max-w-full",
        /* No rule under each row. A hairline between every setting turns a
           short list into a ledger and competes with the section breaks that
           actually mean something — the boss: "that only makes sense between
           larger sections not each fucking row". Rows separate by rhythm; a
           SECTION separates with a rule or a band. `noRule` stays accepted so
           no consumer breaks, and is now the default behaviour. */
        // (rule intentionally absent)
        className,
      )}
    >
      {leadingAction ? (
        <div className="flex min-w-0 max-w-full items-center gap-3">
          <span className="flex shrink-0 items-center justify-center">{leadingAction}</span>
          <span className="min-w-0 flex-1">{body}</span>
        </div>
      ) : (
        body
      )}
      {children ? <div className="min-w-0 max-w-full pb-3">{children}</div> : null}
    </div>
  );
}

/**
 * GroupLabel — the mono uppercase label that names a run of rows.
 *
 * Replaces Kanban column headers and rail section headings: a group of rows
 * gets a label, not a container. Never rendered above a page or section
 * title (§1.6 — eyebrows label groups, never titles).
 */
export function GroupLabel({
  label,
  count,
  trailing,
  className,
}: {
  label: string;
  count?: number;
  trailing?: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex min-h-8 min-w-0 max-w-full items-center justify-between gap-3 pb-1 pt-4 first:pt-1",
        className,
      )}
    >
      <span className="flex min-w-0 items-baseline gap-2">
        <span className="truncate font-mono text-[11px] uppercase tracking-[0.08em] text-quiet">
          {label}
        </span>
        {/* Zero is the empty state's job, not a badge's. See board.tsx. */}
        {count !== undefined && count !== 0 ? (
          <span className="shrink-0 font-mono text-[11px] tabular-nums text-quiet">{count}</span>
        ) : null}
      </span>
      {trailing ? <span className="shrink-0">{trailing}</span> : null}
    </div>
  );
}

export interface WorkRowProps {
  /** Mono uppercase eyebrow naming the kind of work ("PLAN", "CRON", "SKILL"). */
  kind?: string;
  /** Plain-English title. Never a raw skill name or cron id — those go in the detail. */
  title: React.ReactNode;
  /** Short status word rendered as a chip, tinted by `tone`. */
  status?: string;
  /** Quiet line under the title: elapsed, owner, step count. */
  meta?: React.ReactNode;
  /**
   * The narrative: what the run actually did, in Jarvis's words. Wraps to at
   * most two lines (§ "runs carry a narrative" — a bare "ok" is not an
   * outcome). Distinct from `meta`, which is a single truncating fact line.
   */
  summary?: React.ReactNode;
  /**
   * 0..1 for a known fraction, "indeterminate" when the work is genuinely
   * moving but cannot quote one, omitted when there is no evidence it is
   * doing anything. Coloured by `tone`. Never pass a made-up number: a bar
   * that creeps while nothing happens is the lie this prop exists to avoid.
   */
  progress?: number | "indeterminate";
  tone?: RowTone;
  /**
   * Pulse the tone dot and animate nothing else — the one alive signal.
   *
   * Pass this ONLY on evidence the row moved recently, never because its
   * column says "running". A status column is set once at the start and
   * nothing guarantees anything sets it back, so a dot driven by one goes on
   * animating "I am working on it" long after the process died.
   */
  live?: boolean;
  onClick?: () => void;
  href?: string;
  trailing?: React.ReactNode;
  /** Accepted for compatibility. Rows no longer draw a rule at all. */
  noRule?: boolean;
  className?: string;
}

/**
 * Progress-bar fill per tone. Brand is the default because a bar means work in
 * flight; a run that BROKE or is WAITING must not paint its progress emerald
 * (§1.4 — colour carries meaning). This is the one place that mapping lives, so
 * the Agent Work board, the goal rows, and the work-item modal agree.
 */
const BAR_TONE: Record<RowTone, string> = {
  default: "bg-brand",
  quiet: "bg-quiet",
  brand: "bg-brand",
  accent: "bg-brand",
  success: "bg-success",
  info: "bg-info",
  warning: "bg-warning",
  danger: "bg-danger",
};

const CHIP_TONE: Record<RowTone, string> = {
  default: "text-quiet",
  quiet: "text-quiet",
  brand: "text-brand",
  accent: "text-brand",
  success: "text-success",
  info: "text-info",
  warning: "text-warning",
  danger: "text-danger",
};

/**
 * WorkRow — a unit of work with a status (Agent Work board, work-item modal).
 *
 * Same hairline/touch-target/overflow contract as ListRow, plus the three
 * things a work item always has: what kind of work it is (mono eyebrow), how
 * it is going (status chip in the tone colour), and, when it is measurable,
 * how far along (a 2px brand bar). No card, no border, no icon tile — the
 * board is a ledger, not a deck.
 */
export function WorkRow({
  kind,
  title,
  status,
  meta,
  summary,
  progress,
  tone = "default",
  live,
  onClick,
  href,
  trailing,
  className,
}: WorkRowProps) {
  const tappable = Boolean(onClick || href);
  // Three honest states, and no fourth:
  //
  //   a number        we know how far along it is. A real fraction of real work.
  //   "indeterminate" it is genuinely moving and cannot quote a fraction. The
  //                   bar sweeps and never fills, so it says "working" without
  //                   ever claiming a percentage.
  //   undefined       we have no evidence it is doing anything. Nothing draws.
  //
  // The middle state exists because the alternative was worse: showing no bar
  // and letting a PULSING DOT carry the claim instead. A dot driven by a
  // status column asserts "this is alive right now" with no evidence behind
  // it, and an animation is the strongest assertion an interface can make. A
  // sweep that quotes no number is the honest way to say "moving, extent
  // unknown".
  const indeterminate = progress === "indeterminate";
  const pct =
    typeof progress === "number"
      ? Math.max(0, Math.min(100, Math.round(progress * 100)))
      : null;

  const inner = (
    <>
      <span className="flex size-[18px] shrink-0 items-center justify-center pt-[3px]">
        <StatusDot tone={tone} pulse={live} />
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-1 text-left">
        {kind ? (
          <span className="truncate font-mono text-[10.5px] uppercase tracking-[0.08em] text-quiet">
            {kind}
          </span>
        ) : null}
        <span className="min-w-0 truncate font-voice text-[15.5px] leading-[1.4] text-foreground">
          {title}
        </span>
        {meta ? (
          <span className="min-w-0 truncate text-[12px] tabular-nums text-quiet">{meta}</span>
        ) : null}
        {summary ? (
          <span className="line-clamp-2 min-w-0 text-[12px] leading-snug text-muted-foreground [overflow-wrap:anywhere]">
            {summary}
          </span>
        ) : null}
        {indeterminate ? (
          <span
            className="mt-0.5 flex h-[3px] w-full min-w-0 overflow-hidden rounded-full bg-hairline"
            role="progressbar"
            aria-label="Working, no percentage available"
          >
            <span className={cn("h-full w-1/3 rounded-full bar-sweep", BAR_TONE[tone])} />
          </span>
        ) : pct !== null ? (
          <span
            className="mt-0.5 flex h-[3px] w-full min-w-0 overflow-hidden rounded-full bg-hairline"
            role="progressbar"
            aria-valuenow={pct}
            aria-valuemin={0}
            aria-valuemax={100}
          >
            {/* The only inline value in this file. Width IS the datum here —
                a runtime percentage cannot be a static utility class, and
                Tailwind cannot generate a class per value. Matches how every
                other progress bar in Studio does it (ContextBudget,
                AgentWorkBoard, /heartbeat). Colour and shape stay in classes. */}
            <span
              className={cn("h-full rounded-full transition-[width] duration-300", BAR_TONE[tone])}
              style={{ width: `${pct}%` }}
            />
          </span>
        ) : null}
      </span>
      <span className="flex shrink-0 items-center gap-2 self-start pt-[1px]">
        {status ? (
          <span
            className={cn(
              "shrink-0 font-mono text-[11px] uppercase tracking-[0.06em]",
              CHIP_TONE[tone],
            )}
          >
            {status}
          </span>
        ) : null}
        {trailing}
        {tappable ? <ChevronRight className="size-4 text-quiet" aria-hidden /> : null}
      </span>
    </>
  );

  const rowClass = cn(
    "flex min-h-11 w-full min-w-0 max-w-full items-start gap-3 py-3 text-left",
    tappable && "transition-colors hover:bg-accent/60 active:bg-accent",
    tappable &&
      "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 focus-visible:ring-offset-0",
  );

  return (
    <div
      className={cn(
        "min-w-0 max-w-full",
        /* No rule under each row. A hairline between every setting turns a
           short list into a ledger and competes with the section breaks that
           actually mean something — the boss: "that only makes sense between
           larger sections not each fucking row". Rows separate by rhythm; a
           SECTION separates with a rule or a band. `noRule` stays accepted so
           no consumer breaks, and is now the default behaviour. */
        // (rule intentionally absent)
        className,
      )}
    >
      {href ? (
        <Link href={href} className={rowClass}>
          {inner}
        </Link>
      ) : onClick ? (
        <button type="button" onClick={onClick} className={rowClass}>
          {inner}
        </button>
      ) : (
        <div className={rowClass}>{inner}</div>
      )}
    </div>
  );
}
