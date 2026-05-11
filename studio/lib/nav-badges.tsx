"use client";

import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { fetchTrustContracts, fetchSkillProposals } from "@/lib/api";

/**
 * Single source of truth for "something needs the boss's attention in this
 * tab" counts. Polls cheap endpoints on a fixed interval; consumers
 * (`TabNav`, `MobileNav`) read via `useNavBadge(href)` and render a chip
 * when the count is > 0.
 *
 * The polling cadence is intentionally slow (20s) — these are background
 * signals, not the conversation. Studio's `<RealtimeProvider>` provides
 * the instant updates inside each tab; this hook only exists so the user
 * sees something pop up in the nav without having to open every tab.
 */

const POLL_MS = 20_000;

export type NavBadges = Record<string, number>;

const BadgeContext = createContext<NavBadges>({});

export function NavBadgesProvider({ children }: { children: ReactNode }) {
  const [badges, setBadges] = useState<NavBadges>({});
  const inflightRef = useRef(false);

  useEffect(() => {
    let cancelled = false;

    async function tick() {
      if (inflightRef.current) return;
      inflightRef.current = true;
      try {
        const [trust, candidates] = await Promise.allSettled([
          fetchTrustContracts("pending"),
          fetchSkillProposals("candidate"),
        ]);

        if (cancelled) return;

        const next: NavBadges = {};
        if (trust.status === "fulfilled") {
          next["/trust"] = (trust.value ?? []).length;
        }
        if (candidates.status === "fulfilled") {
          next["/skills"] = (candidates.value ?? []).length;
        }
        setBadges((prev) => {
          // Avoid re-rendering all nav links when nothing changed.
          const sameKeys = Object.keys(next).length === Object.keys(prev).length;
          const sameVals = sameKeys && Object.keys(next).every((k) => prev[k] === next[k]);
          return sameVals ? prev : next;
        });
      } finally {
        inflightRef.current = false;
      }
    }

    tick();
    const id = setInterval(tick, POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  const value = useMemo(() => badges, [badges]);
  return <BadgeContext.Provider value={value}>{children}</BadgeContext.Provider>;
}

export function useNavBadge(href: string): number {
  const badges = useContext(BadgeContext);
  return badges[href] ?? 0;
}
