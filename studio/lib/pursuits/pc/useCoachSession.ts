"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { seedSession } from "@/lib/dashboard/seed";
import { useWebSocket } from "@/lib/ws/provider";
import type { WSEvent } from "@/lib/ws/client";

/* Live Jarvis inside the coaching session.
 *
 * The guided beats are deterministic and instant (lib/pursuits/pc/coaching.ts).
 * This hook is the OTHER half: the moment the boss says something the script
 * did not anticipate, that sentence goes to the real agent.
 *
 * It deliberately does NOT use `useChat`. That hook owns the global chat
 * session: it reads and writes `infinity:sessionId` in localStorage, rotates
 * stale sessions, and takes its id from the `?session=` search param. Mounting
 * it inside the cockpit would silently repoint the boss's main /live
 * conversation at the coaching session. What we reuse instead is the layer
 * underneath it, which is where the reuse actually belongs:
 *
 *   • the app-wide WebSocketProvider socket (multi-subscriber, already mounted
 *     in app/layout.tsx), filtered to this session id
 *   • `seedSession("pursuit_pc", …)`, the SAME seed the dashboard's
 *     Discuss-with-Jarvis uses, which hydrates turn one with the full cockpit
 *     via pc.FormatChatContext in Go
 *
 * So there is one transport, one agent loop, one memory-capture path, and the
 * session id we produce is the same one `/live?session=` opens. No parallel
 * chat stack, no duplicate API.
 *
 * The session is minted LAZILY, on the first thing the boss actually says.
 * Opening the cockpit to tap through a rehearsal should not leave an empty
 * conversation behind in his session list.
 */

export type CoachLiveRole = "boss" | "coach";

export type CoachLiveMessage = {
  id: string;
  role: CoachLiveRole;
  text: string;
  /** Still streaming, or still waiting for the first token. */
  pending?: boolean;
  /** Set when the turn failed. Never rendered as a normal reply. */
  error?: string;
};

export type CoachSession = {
  /** The seeded session id, once one exists. Null until the boss speaks. */
  sessionId: string | null;
  messages: CoachLiveMessage[];
  /** A turn is in flight. */
  busy: boolean;
  /** Transport state, so the surface can say plainly that it cannot reach him. */
  connected: boolean;
  ask: (text: string) => Promise<void>;
  /** Mint the session without sending anything, for the workspace handoff. */
  open: () => Promise<string | null>;
};

let seq = 0;
function nextId(prefix: string): string {
  seq += 1;
  return `${prefix}-${seq}`;
}

export function useCoachSession(pursuitId: string): CoachSession {
  const ws = useWebSocket();
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [messages, setMessages] = useState<CoachLiveMessage[]>([]);
  const [busy, setBusy] = useState(false);

  // The subscriber is registered once and reads the live id from a ref, so a
  // newly minted session never misses the deltas of the turn that created it.
  const sessionRef = useRef<string | null>(null);
  const seedingRef = useRef<Promise<string | null> | null>(null);
  const replyRef = useRef<string | null>(null);

  useEffect(() => {
    sessionRef.current = sessionId;
  }, [sessionId]);

  // Reset when the cockpit is pointed at a different pursuit.
  useEffect(() => {
    sessionRef.current = null;
    seedingRef.current = null;
    replyRef.current = null;
    setSessionId(null);
    setMessages([]);
    setBusy(false);
  }, [pursuitId]);

  const finish = useCallback((id: string, patch: Partial<CoachLiveMessage>) => {
    setMessages((prev) => prev.map((m) => (m.id === id ? { ...m, ...patch } : m)));
  }, []);

  useEffect(() => {
    return ws.subscribe((ev: WSEvent) => {
      const current = sessionRef.current;
      if (!current || !("session_id" in ev) || ev.session_id !== current) return;

      switch (ev.type) {
        case "delta": {
          const id = replyRef.current;
          if (!id) return;
          setMessages((prev) =>
            prev.map((m) => (m.id === id ? { ...m, text: m.text + ev.text } : m)),
          );
          return;
        }
        case "complete": {
          const id = replyRef.current;
          replyRef.current = null;
          setBusy(false);
          if (!id) return;
          // A turn that completed without a single token is not a reply. Say
          // so rather than leaving an empty bubble that reads as silence.
          setMessages((prev) =>
            prev.flatMap((m) => {
              if (m.id !== id) return [m];
              if (m.text.trim()) return [{ ...m, pending: false }];
              return [
                {
                  ...m,
                  pending: false,
                  error: "That turn came back empty. Nothing was lost, try asking again.",
                },
              ];
            }),
          );
          return;
        }
        case "error": {
          const id = replyRef.current;
          replyRef.current = null;
          setBusy(false);
          if (id) finish(id, { pending: false, error: ev.message });
          return;
        }
        default:
          return;
      }
    });
  }, [ws, finish]);

  /** Mint (once) the seeded pursuit_pc session. Concurrent callers share the
   *  same in-flight promise so a fast double tap cannot create two sessions. */
  const open = useCallback(async (): Promise<string | null> => {
    if (sessionRef.current) return sessionRef.current;
    if (seedingRef.current) return seedingRef.current;

    const p = (async () => {
      const id = await seedSession("pursuit_pc", pursuitId);
      if (id) {
        sessionRef.current = id;
        setSessionId(id);
      }
      seedingRef.current = null;
      return id;
    })();
    seedingRef.current = p;
    return p;
  }, [pursuitId]);

  const ask = useCallback(
    async (raw: string) => {
      const text = raw.trim();
      if (!text || busy) return;

      const bossId = nextId("boss");
      const replyId = nextId("coach");
      setMessages((prev) => [
        ...prev,
        { id: bossId, role: "boss", text },
        { id: replyId, role: "coach", text: "", pending: true },
      ]);
      setBusy(true);

      const id = await open();
      if (!id) {
        replyRef.current = null;
        setBusy(false);
        finish(replyId, {
          pending: false,
          error:
            "I could not open a conversation just then. Your programme is saved either way.",
        });
        return;
      }

      replyRef.current = replyId;
      const sent = ws.send({ type: "message", session_id: id, content: text });
      if (!sent) {
        replyRef.current = null;
        setBusy(false);
        finish(replyId, {
          pending: false,
          error: "I am not connected right now, so that did not reach me.",
        });
        ws.reconnect();
      }
    },
    [busy, finish, open, ws],
  );

  return {
    sessionId,
    messages,
    busy,
    connected: ws.status === "connected",
    ask,
    open,
  };
}
