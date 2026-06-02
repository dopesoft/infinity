import type { CronJobDTO, SentinelDTO } from "@/lib/api";

/* Plain-English labels for the cron + sentinel engine enums.
 *
 * The raw enum values (`isolated_agent_turn`, `system_task`, `external_api_poll`)
 * are engine internals — they belong in the on-click detail, never as the thing
 * the boss reads first (see CLAUDE.md → "Readable names, engine on click").
 * This is the single source of truth so the list row, the create form, and the
 * detail modal all describe a kind the same way. */

export type CronKind = CronJobDTO["job_kind"];
export type WatchType = SentinelDTO["watch_type"];

export const CRON_KIND_META: Record<
  CronKind,
  { label: string; blurb: string }
> = {
  isolated_agent_turn: {
    label: "Scheduled task",
    blurb:
      "Runs as a fresh, self-contained agent session each time it fires. It has no memory of other chats — it reads your instructions, does the work, saves anything worth keeping, then ends. Best for recurring jobs you don't need to watch live.",
  },
  system_event: {
    label: "Live-session task",
    blurb:
      "Runs inside your main chat session, so its work shows up in Live as if you'd asked it yourself. Use when you want the result to land in the ongoing conversation.",
  },
  connector_poll: {
    label: "App sync",
    blurb:
      "Checks a connected app (Gmail, Calendar, …) on a schedule and files what it finds onto your dashboards. No AI thinking — it's a direct, deterministic fetch.",
  },
  system_task: {
    label: "Maintenance",
    blurb:
      "Built-in housekeeping that runs automatically (memory consolidation, nightly cognition, …). No AI prompt — it's fixed internal upkeep.",
  },
};

export function cronKindMeta(kind: CronKind) {
  return (
    CRON_KIND_META[kind] ?? {
      label: kind,
      blurb: "Scheduled job.",
    }
  );
}

export const WATCH_TYPE_META: Record<
  WatchType,
  { label: string; blurb: string }
> = {
  webhook: {
    label: "Webhook",
    blurb:
      "Fires when an outside system sends it an HTTP request. Used to react to events from other services.",
  },
  file_change: {
    label: "File change",
    blurb:
      "Watches a file and fires when it's modified (size or timestamp changes).",
  },
  memory_event: {
    label: "Memory event",
    blurb:
      "Fires when a new observation matching a filter lands in memory — Jarvis reacting to something he just noticed.",
  },
  external_api_poll: {
    label: "API poll",
    blurb:
      "Polls an external endpoint on an interval and fires when the response changes or matches a condition.",
  },
  threshold: {
    label: "Threshold",
    blurb:
      "Runs a query and fires when the result crosses a number you set (e.g. a count goes above N).",
  },
};

export function watchTypeMeta(t: WatchType) {
  return (
    WATCH_TYPE_META[t] ?? {
      label: t,
      blurb: "Event watcher.",
    }
  );
}

/* Format a timestamp in the boss's local frame, casual + short, matching the
 * LocalTimeProvider voice ("Thu 5 Jun, 3:00am"). Returns "" for nullish input
 * so callers can fall back cleanly. Locale-dependent → render under
 * suppressHydrationWarning. */
export function casualTime(iso?: string | null): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const date = d.toLocaleDateString(undefined, {
    weekday: "short",
    day: "numeric",
    month: "short",
  });
  const time = d
    .toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })
    .toLowerCase()
    .replace(/\s/g, "");
  return `${date}, ${time}`;
}

/* The browser's local timezone abbreviation (e.g. "CST"), used to label times
 * so the single convention is explicit. Falls back to "local time". */
export function localTzAbbrev(): string {
  try {
    const parts = new Intl.DateTimeFormat(undefined, {
      timeZoneName: "short",
    }).formatToParts(new Date());
    return parts.find((p) => p.type === "timeZoneName")?.value ?? "local time";
  } catch {
    return "local time";
  }
}
