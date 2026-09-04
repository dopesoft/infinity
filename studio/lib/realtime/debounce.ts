/**
 * debounce.ts - one trailing-edge debounce, pure, so the rule can be tested.
 *
 * WHY. Every mem_sessions UPDATE (token usage after each assistant segment,
 * the title, last_run_at) reached three separate `useRealtime("mem_sessions")`
 * subscribers, each of which refetched the whole session list. During a turn
 * that was up to fifteen GET /api/sessions a second (Railway HTTP logs,
 * 2026-09-04) for a name that changes once. A burst becomes one refetch.
 */
export function trailingDebounce<A extends unknown[]>(
  fn: (...args: A) => void,
  waitMs: number,
): ((...args: A) => void) & { cancel: () => void } {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let last: A | null = null;
  const run = (...args: A) => {
    last = args;
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => {
      timer = null;
      const a = last;
      last = null;
      if (a) fn(...a);
    }, waitMs);
  };
  run.cancel = () => {
    if (timer) clearTimeout(timer);
    timer = null;
    last = null;
  };
  return run;
}
