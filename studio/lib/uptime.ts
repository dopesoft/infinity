/**
 * Tab-scoped boot timestamp.
 *
 * Persisted to sessionStorage so it survives client-side navigation
 * (TabFrame remounts on every Next.js route change, which would otherwise
 * reset any in-component `Date.now()` counter). The value clears when the
 * tab is closed - that's the right semantic for "time since you opened
 * Infinity in this tab".
 */
const KEY = "infinity:studio:bootedAt";

export function getBootedAt(): number {
  if (typeof window === "undefined") return Date.now();
  const existing = window.sessionStorage.getItem(KEY);
  if (existing) {
    const n = Number(existing);
    if (Number.isFinite(n)) return n;
  }
  const now = Date.now();
  try {
    window.sessionStorage.setItem(KEY, String(now));
  } catch {
    // sessionStorage can throw in private mode / disabled storage;
    // fall back to in-memory now() - the counter still ticks, it just
    // resets on navigation in that environment. Not worth a runtime error.
  }
  return now;
}

export function formatUptime(ms: number): string {
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}
