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
import { fetchNavCounts } from "@/lib/api";
import { useAuth } from "@/lib/auth/session";

/**
 * Single source of truth for "something needs the boss's attention in this
 * tab" counts. Pulls from `/api/nav/counts` (one cheap round-trip) on a
 * fixed interval so the user sees a chip light up in the nav without
 * having to open every tab.
 *
 * Consumers (`TabNav`, `MobileNav`) read via `useNavBadge(href)` and
 * render a chip when the count is > 0. The keying is by route path so
 * adding a new tab is just adding a row to the map below.
 *
 * Polling cadence is intentionally slow (20s) - these are background
 * signals, not the conversation. Studio's `<RealtimeProvider>` provides
 * the instant updates inside each tab.
 */

const POLL_MS = 20_000;

export type NavBadges = Record<string, number>;

const BadgeContext = createContext<NavBadges>({});

export function NavBadgesProvider({ children }: { children: ReactNode }) {
  const [badges, setBadges] = useState<NavBadges>({});
  const inflightRef = useRef(false);
  // Only poll Core while signed in. On /login the JWT doesn't exist yet,
  // so the polls would 401 every 20s and litter the console with red.
  const { user } = useAuth();
  const userId = user?.id ?? null;

  useEffect(() => {
    if (!userId) {
      setBadges((prev) => (Object.keys(prev).length === 0 ? prev : {}));
      return;
    }

    let cancelled = false;

    async function tick() {
      if (inflightRef.current) return;
      inflightRef.current = true;
      try {
        const counts = await fetchNavCounts();
        if (cancelled || !counts) return;

        const next: NavBadges = {
          // Primary tabs (4)
          "/": counts.dashboard,
          "/dashboard": counts.dashboard,
          "/live": counts.chat,
          "/memory": counts.memory,
          "/skills": counts.skills,
          // Overflow tabs
          "/lab": counts.overflow?.lab ?? 0,
          "/heartbeat": counts.overflow?.heartbeat ?? 0,
          "/logs": counts.overflow?.logs ?? 0,
          // Settings carries the trust pending count (Trust folded in
          // per the IA defrag plan). We surface it on Settings AND on
          // Chat (where the realtime approval lives).
          "/settings": counts.chat,
          "/trust": counts.chat,
        };

        setBadges((prev) => {
          const sameKeys = Object.keys(next).length === Object.keys(prev).length;
          const sameVals =
            sameKeys && Object.keys(next).every((k) => prev[k] === next[k]);
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
  }, [userId]);

  const value = useMemo(() => badges, [badges]);
  return <BadgeContext.Provider value={value}>{children}</BadgeContext.Provider>;
}

export function useNavBadge(href: string): number {
  const badges = useContext(BadgeContext);
  return badges[href] ?? 0;
}

/**
 * useNavBadgesAll exposes the whole map (rare consumer; the dashboard
 * "Need you" pill uses it). Memoized by NavBadgesProvider.
 */
export function useNavBadgesAll(): NavBadges {
  return useContext(BadgeContext);
}
