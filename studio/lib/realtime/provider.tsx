"use client";

import { createContext, useContext, useEffect, useMemo, useRef } from "react";
import type {
  RealtimeChannel,
  RealtimePostgresChangesPayload,
} from "@supabase/supabase-js";
import { useAuth } from "@/lib/auth/session";
import { getSupabaseBrowserClient } from "@/lib/supabase/client";

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
  const { user } = useAuth();
  const userId = user?.id ?? null;
  const listenersRef = useRef<Set<Listener>>(new Set());
  const channelRef = useRef<RealtimeChannel | null>(null);

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

    // Tables to subscribe to. Adding a table here without also bumping a
    // realtime publication migration silently no-ops on the wire.
    const TABLES = [
      "mem_sessions",
      "mem_observations",
      "mem_memories",
      "mem_summaries",
      "mem_audit",
      "mem_heartbeats",
      "mem_heartbeat_findings",
      "mem_intent_decisions",
      "mem_outcomes",
      "mem_trust_contracts",
      "mem_patterns",
      "mem_crons",
      "mem_sentinels",
      "mem_skills",
      "mem_skill_runs",
      "mem_skill_proposals",
      "mem_profiles",
      "mem_lessons",
      "mem_session_state",
      "mem_working_buffer",
      "mem_curiosity_questions",
      "mem_reflections",
      "mem_predictions",
      "mem_code_proposals",
      "mem_surface_items",
      "mem_tasks",
      "mem_pursuits",
      "mem_pursuit_checkins",
      "mem_followups",
      "mem_saved",
      "mem_training_examples",
      "mem_distillation_datasets",
      "mem_distillation_runs",
      "mem_model_adapters",
      "mem_adapter_evals",
      "mem_policy_routes",
      "mem_turns",
    ] as const;

    let channel = supabase.channel("infinity-realtime");
    for (const table of TABLES) {
      channel = channel.on(
        "postgres_changes",
        { event: "*", schema: "public", table },
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        (payload: RealtimePostgresChangesPayload<any>) => {
          for (const l of listenersRef.current) {
            if (l.table !== table) continue;
            if (
              !l.events.includes("*") &&
              !l.events.includes(payload.eventType as Event)
            ) {
              continue;
            }
            try {
              l.handler(payload);
            } catch (err) {
              console.error("realtime handler error", err);
            }
          }
        },
      );
    }

    channel.subscribe();
    channelRef.current = channel;

    return () => {
      supabase.removeChannel(channel);
      channelRef.current = null;
    };
  }, [userId]);

  return (
    <RealtimeContext.Provider value={{ register }}>{children}</RealtimeContext.Provider>
  );
}

// useRealtime subscribes the calling component to changes on the given
// table(s) and runs `handler` for every event. Re-registers when handler
// or events change. Pass a single string or an array.
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
}
