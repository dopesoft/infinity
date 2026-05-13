/* Date/time formatters shared across Dashboard surfaces.
 *
 * All client-rendered date strings on the Dashboard rely on these helpers
 * + `suppressHydrationWarning` on the wrapping <span> so UTC server vs
 * locale client never trigger React's hydration mismatch warning.
 */

export function relTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "—";
  const delta = Date.now() - t;
  const future = delta < 0;
  const abs = Math.abs(delta);
  const s = Math.max(1, Math.floor(abs / 1000));
  let out: string;
  if (s < 60) out = `${s}s`;
  else if (s < 3600) out = `${Math.floor(s / 60)}m`;
  else if (s < 86_400) out = `${Math.floor(s / 3600)}h`;
  else out = `${Math.floor(s / 86_400)}d`;
  return future ? `in ${out}` : `${out} ago`;
}

export function clockTime(iso: string | null | undefined, opts: { seconds?: boolean } = {}): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleTimeString([], {
    hour: "numeric",
    minute: "2-digit",
    ...(opts.seconds ? { second: "2-digit" } : {}),
  });
}

export function dayLabel(iso: string): string {
  const d = new Date(iso);
  const today = new Date();
  const startOfDay = (x: Date) => {
    const c = new Date(x);
    c.setHours(0, 0, 0, 0);
    return c;
  };
  const diffDays = Math.round(
    (startOfDay(d).getTime() - startOfDay(today).getTime()) / (24 * 60 * 60 * 1000),
  );
  if (diffDays === 0) return "Today";
  if (diffDays === 1) return "Tomorrow";
  if (diffDays === -1) return "Yesterday";
  if (diffDays > 0 && diffDays < 7) {
    return d.toLocaleDateString([], { weekday: "long" });
  }
  return d.toLocaleDateString([], { weekday: "short", month: "short", day: "numeric" });
}

export function todayHeader(): { title: string; sub: string } {
  const d = new Date();
  return {
    title: d.toLocaleDateString([], { weekday: "long" }),
    sub: d.toLocaleDateString([], { month: "long", day: "numeric", year: "numeric" }),
  };
}

export function formatDuration(ms: number | undefined): string {
  if (!ms) return "—";
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  if (ms < 3_600_000) return `${Math.round(ms / 60_000)}m`;
  return `${(ms / 3_600_000).toFixed(1)}h`;
}

export function startOfDay(d: Date | string): number {
  const date = typeof d === "string" ? new Date(d) : new Date(d);
  date.setHours(0, 0, 0, 0);
  return date.getTime();
}
