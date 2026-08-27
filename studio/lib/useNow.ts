"use client";

import { useEffect, useState } from "react";

/**
 * useNow - a live clock for elapsed-time readouts.
 *
 * Returns the current time and re-renders the caller every `intervalMs`
 * while `active` is true; frozen (no timer) once inactive. This is what makes
 * a "12.4s" readout on a running card actually tick instead of updating only
 * when some other interaction happens to re-render it (2026-08-26: tool-card
 * seconds frozen until the boss tapped the card).
 *
 * Hydration discipline: the initializer is 0 (never Date.now() in a
 * useState initializer); the first real reading lands in the effect. Callers
 * treat 0 as "no reading yet" and fall back to render-time Date.now().
 */
export function useNow(active: boolean, intervalMs = 1000): number {
  const [now, setNow] = useState<number>(0);
  useEffect(() => {
    if (!active) return;
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [active, intervalMs]);
  return now;
}
