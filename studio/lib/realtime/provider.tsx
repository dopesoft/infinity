"use client";

import { createContext, useContext, useEffect, useMemo, useRef } from "react";
import type {
  RealtimeChannel,
  RealtimePostgresChangesPayload,
} from "@supabase/supabase-js";
import { useAuth } from "@/lib/auth/session";
import { getSupabaseBrowserClient } from "@/lib/supabase/client";
import { trailingDebounce } from "@/lib/realtime/debounce";

type Event = "INSERT" | "UPDATE" | "DELETE" | "*";

type Listener = {
  table: string;
  events: Event[];
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  handler: (payload: RealtimePostgresChangesPayload<any>) => void;
};

type RealtimeContextValue = {
  register: (l: Listener) => () => void;
};

const RealtimeContext = createContext<RealtimeContextValue | null>(null);

// One Supabase channel per provider mount. All useRealtime() consumers
// register listeners against this channel. The channel itself is rebuilt
// when the user / table set changes; individual listeners just attach +
// detach handlers without churning the websocket.
export function RealtimeProvider({ children }: { children: React.ReactNode }) {
  const { user, accessToken } = useAuth();
  const userId = user?.id ?? null;
  const listenersRef = useRef<Set<Listener>>(new Set());
  const channelRef = useRef<RealtimeChannel | null>(null);
  // Latest JWT in a ref so the channel-build effect (keyed on userId) can
  // authenticate the socket without re-subscribing on every token refresh.
  const tokenRef = useRef<string | null>(accessToken);
  tokenRef.current = accessToken;

  // Stable register function - listeners de-register via the returned
  // teardown. The handler is dispatched inside the channel subscription.
  const register = useMemo<RealtimeContextValue["register"]>(
    () => (l: Listener) => {
      listenersRef.current.add(l);
      return () => {
        listenersRef.current.delete(l);
      };
    },
    [],
  );

  useEffect(() => {
    if (typeof window === "undefined") return;
    if (!userId) {
      // Tear down on sign-out.
      if (channelRef.current) {
        getSupabaseBrowserClient().removeChannel(channelRef.current);
        channelRef.current = null;
      }
      return;
    }

    const supabase = getSupabaseBrowserClient();

    // Build (or rebuild) the schema-wide realtime channel. Extracted into a
    // closure so the wake handler below can re-subscribe after a backgrounded
    // browser (iOS Safari especially) kills the socket.
    //
    // Authenticate the realtime SOCKET with the user's JWT before subscribing.
    // Every mem_* table's realtime read policy is `TO authenticated` (migration
    // 083), so an anon socket — which is what @supabase/ssr's cookie-recovered
    // client connects as on first load — has every change event silently
    // filtered out by RLS. Without this, nothing updates live without a manual
    // refresh (session name, background dock, dashboard, runs, …). The separate
    // effect below keeps the token fresh across refreshes.
    //
    // ONE schema-wide subscription instead of a hand-maintained per-table
    // list. Subscribing to the whole `public` schema means the channel hears
    // EVERY published table automatically — add a table to the realtime
    // publication and it's covered with zero frontend edits. Per-table RLS
    // (`TO authenticated`, migration 083) still scopes what the socket
    // receives, and consumers still filter by their own table list.
    const buildChannel = () => {
      if (tokenRef.current) supabase.realtime.setAuth(tokenRef.current);
      const channel = supabase
        .channel("infinity-realtime")
        .on(
          "postgres_changes",
          { event: "*", schema: "public" },
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          (payload: RealtimePostgresChangesPayload<any>) => {
            // payload.table is the changed table. Dispatch to every listener
            // that targets it — or that subscribed with "*" (react to anything).
            const tbl = (payload as { table?: string }).table ?? "";
            const evt = payload.eventType as Event;
            for (const l of listenersRef.current) {
              if (l.table !== "*" && l.table !== tbl) continue;
              if (!l.events.includes("*") && !l.events.includes(evt)) continue;
              try {
                l.handler(payload);
              } catch (err) {
                console.error("realtime handler error", err);
              }
            }
          },
        );
      channel.subscribe();
      channelRef.current = channel;
    };

    buildChannel();

    // KEEP LIVE ACROSS FOCUS — the boss's rule is that data updates CONTINUOUSLY
    // while the window is visible (even if it's not the focused app), never a
    // batch "catch-up" that all moves at once when he clicks back in. This is
    // event-driven (postgres_changes push straight to the handlers above), so a
    // visible-but-unfocused desktop window already updates live and needs NOTHING
    // here. The ONLY thing this wake handler does is REPAIR a socket that a
    // browser actually KILLED while suspended (iOS Safari backgrounding) — and
    // even then it does NOT fire a blanket refetch on every focus (that blanket
    // refetch is exactly the jarring "a thousand things move when I click it"
    // the boss hates). We rebuild the socket only when it's genuinely dropped,
    // and only then catch up the consumers that missed events while it was dead.
    let wakeTimer: ReturnType<typeof setTimeout> | null = null;
    const onWake = () => {
      if (document.visibilityState !== "visible") return;
      const ch = channelRef.current;
      // Socket still joined → updates never stopped → do NOTHING (no flush).
      if (ch && ch.state === "joined") return;
      // Debounce: pageshow + focus + visibilitychange can all fire on one wake.
      if (wakeTimer) return;
      wakeTimer = setTimeout(() => {
        wakeTimer = null;
        const cur = channelRef.current;
        if (cur && cur.state === "joined") return; // recovered on its own
        if (tokenRef.current) supabase.realtime.setAuth(tokenRef.current);
        if (cur) supabase.removeChannel(cur);
        buildChannel();
        // The socket WAS dead, so consumers missed events — catch them up once.
        window.dispatchEvent(new Event("infinity:pull-to-refresh"));
      }, 250);
    };
    window.addEventListener("pageshow", onWake);
    window.addEventListener("focus", onWake);
    document.addEventListener("visibilitychange", onWake);

    return () => {
      window.removeEventListener("pageshow", onWake);
      window.removeEventListener("focus", onWake);
      document.removeEventListener("visibilitychange", onWake);
      if (wakeTimer) clearTimeout(wakeTimer);
      if (channelRef.current) supabase.removeChannel(channelRef.current);
      channelRef.current = null;
    };
  }, [userId]);

  // Keep the socket token fresh across refreshes without tearing down the
  // channel. setAuth pushes the new JWT to every joined channel, so an
  // hourly token refresh doesn't drop us back to anon (which would silently
  // re-break realtime until the next full reload).
  useEffect(() => {
    if (typeof window === "undefined" || !accessToken) return;
    getSupabaseBrowserClient().realtime.setAuth(accessToken);
  }, [accessToken]);

  return (
    <RealtimeContext.Provider value={{ register }}>{children}</RealtimeContext.Provider>
  );
}

// useRealtime subscribes the calling component to changes on the given
// table(s) and runs `handler` for every event. Re-registers when handler
// or events change. Pass a single string or an array.
//
// In addition to Postgres pub/sub, the handler ALSO fires on the global
// "infinity:pull-to-refresh" custom event (dispatched by PullToRefresh
// when the user pulls down at scrollY=0). That makes pull-to-refresh
// universal: every page using useRealtime — which is every page with
// live data — re-runs its load function when the gesture fires.
export function useRealtime(
  table: string | string[],
  handler: () => void,
  events: Event[] = ["*"],
) {
  const ctx = useContext(RealtimeContext);
  // Latest-handler ref so the registration doesn't churn on every render.
  const handlerRef = useRef(handler);
  handlerRef.current = handler;

  useEffect(() => {
    if (!ctx) return;
    const tables = Array.isArray(table) ? table : [table];
    const teardowns = tables.map((t) =>
      ctx.register({
        table: t,
        events,
        handler: () => handlerRef.current(),
      }),
    );
    return () => {
      for (const fn of teardowns) fn();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ctx, Array.isArray(table) ? table.join(",") : table, events.join(",")]);

  // Pull-to-refresh: re-run the page's load handler when the gesture
  // fires, regardless of whether realtime is connected.
  useEffect(() => {
    const onPull = () => handlerRef.current();
    window.addEventListener("infinity:pull-to-refresh", onPull);
    return () => window.removeEventListener("infinity:pull-to-refresh", onPull);
  }, []);
}

// useRealtimeDebounced is useRealtime for a table that changes in bursts
// (mem_sessions moves on every assistant segment): a burst of events becomes
// ONE call to the handler after `waitMs` of quiet. Same subscription path,
// same pull-to-refresh (which stays immediate: the boss asked for it).
export function useRealtimeDebounced(
  table: string | string[],
  handler: () => void,
  waitMs = 500,
  events: Event[] = ["*"],
) {
  const handlerRef = useRef(handler);
  handlerRef.current = handler;
  const debouncedRef = useRef<ReturnType<typeof trailingDebounce<[]>> | null>(null);
  if (!debouncedRef.current) {
    debouncedRef.current = trailingDebounce(() => handlerRef.current(), waitMs);
  }
  useEffect(() => {
    const d = debouncedRef.current;
    return () => d?.cancel();
  }, []);
  useRealtime(table, () => debouncedRef.current?.(), events);
}
