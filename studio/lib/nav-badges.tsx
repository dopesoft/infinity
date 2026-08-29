"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { fetchNavCounts } from "@/lib/api";
import { useAuth } from "@/lib/auth/session";
import { useRealtime } from "@/lib/realtime/provider";

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

// Tables that drive any nav-counts cell. When any of these tables
// changes (insert / update / delete) we kick a counts refetch so the
// badge updates immediately instead of waiting up to 20s for the next
// poll. Source: core/internal/server/nav_counts_api.go - if you add a
// new SELECT COUNT(*) FROM mem_xxx there, add the table here too.
const COUNT_TABLES = [
  "mem_skill_proposals",
  "mem_trust_contracts",
  "mem_curiosity_questions",
  "mem_heartbeat_findings",
  "mem_code_proposals",
  "mem_followups",
  "mem_surface_items",
];

export type NavBadges = Record<string, number>;

const BadgeContext = createContext<NavBadges>({});

export function NavBadgesProvider({ children }: { children: ReactNode }) {
  const [badges, setBadges] = useState<NavBadges>({});
  const inflightRef = useRef(false);
  // Only poll Core while signed in. On /login the JWT doesn't exist yet,
  // so the polls would 401 every 20s and litter the console with red.
  const { user } = useAuth();
  const userId = user?.id ?? null;

  // tick is the one fetch-and-merge path used by both the interval and
  // the realtime listener. Stable identity via useCallback so the
  // useRealtime subscription doesn't re-register on every render.
  const tick = useCallback(async () => {
    if (!userId) return;
    if (inflightRef.current) return;
    inflightRef.current = true;
    try {
      const counts = await fetchNavCounts();
      if (!counts) return;
      // Keyed by the SEVEN routes in `NAV`. The folds mean two of these are
      // sums: Skills absorbed Lab, so a skill candidate and a code proposal
      // both light the same badge; Activity absorbed Heartbeat and Logs, so
      // a finding and a failed job do too. Summing here rather than in Core
      // keeps /api/nav/counts a plain per-table count that nothing else has
      // to agree with.
      const lab = counts.overflow?.lab ?? 0;
      const heartbeat = counts.overflow?.heartbeat ?? 0;
      const logs = counts.overflow?.logs ?? 0;
      const next: NavBadges = {
        "/": counts.dashboard,
        "/live": counts.chat,
        "/memory": counts.memory,
        "/skills": counts.skills + lab,
        "/automations": 0,
        "/activity": heartbeat + logs,
        "/settings": counts.chat,
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
  }, [userId]);

  useEffect(() => {
    if (!userId) {
      setBadges((prev) => (Object.keys(prev).length === 0 ? prev : {}));
      return;
    }
    void tick();
    const id = setInterval(tick, POLL_MS);
    return () => clearInterval(id);
  }, [userId, tick]);

  // Realtime: any change to a count-driving table triggers an immediate
  // refetch. So when the boss approves a skill candidate (UPDATE on
  // mem_skill_proposals → status='approved'), the "2" badge drops to
  // "1" within ms - no refresh, no 20s poll wait.
  useRealtime(COUNT_TABLES, () => {
    void tick();
  });

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
