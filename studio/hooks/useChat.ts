"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";
import type { WSEvent, WSToolEvent } from "@/lib/ws/client";
import { useWebSocket } from "@/lib/ws/provider";
import { fetchSessionMessages } from "@/lib/api";

export type ChatRole = "user" | "assistant" | "tool" | "thinking";

export type ChatMessage = {
  id: string;
  role: ChatRole;
  text: string;
  toolCall?: WSToolEvent;
  toolResult?: WSToolEvent;
  pending?: boolean;
  inputTokens?: number;
  outputTokens?: number;
  latencyMs?: number;
  error?: string;
  createdAt: number;
  // Only set on `thinking` messages once the agent moves on to text/tool/complete.
  endedAt?: number;
  // steered=true on a user message means it was typed and sent mid-turn —
  // it routes through the WS `steer` channel and gets drained into the
  // running agent loop on the next iteration boundary. ChatBubble surfaces
  // a small "↳ steered" affordance so the transcript reads correctly.
  steered?: boolean;
  // interrupted=true on the final assistant message of a turn means the
  // user pressed Stop mid-stream. The partial text streamed is preserved;
  // the UI surfaces a "↩ interrupted" hint rather than an error state.
  interrupted?: boolean;
  // proactive=true marks an assistant bubble that originated from the
  // heartbeat broadcaster (an unprompted turn), not from a user message.
  // Studio renders a subtle origin badge so the boss can tell the agent
  // spoke first.
  proactive?: boolean;
  proactiveKind?: string;
  // seeded=true marks the dashboard context block injected by
  // Discuss-with-Jarvis. It's a user-role turn to the model, but Studio
  // renders it as a distinct "from dashboard" card (DashboardContextCard)
  // instead of a plain user message. seedKind is the originating
  // dashboard item kind (e.g. "activity") used as the card header.
  seeded?: boolean;
  seedKind?: string;
  // curiosityId links a heartbeat/seeded finding to an open curiosity
  // question. When set, the card renders an "Approve & fix" action that
  // marks the question approved and tells the agent to apply the fix.
  curiosityId?: string;
};

type Usage = { input: number; output: number };

const SESSION_KEY = "infinity:sessionId";
// Optimistic per-session message cache. Core (Postgres-backed) remains the
// source of truth — this cache only exists so a refresh while Core is
// offline doesn't blank out the visible conversation. Whenever Core
// returns rows for the session, those overwrite the cache entirely.
const MESSAGES_KEY_PREFIX = "infinity:messages:";
const MESSAGES_CACHE_LIMIT = 200;
// If the agent goes silent for this long after a send, surface a timeout
// so the UI can never get stuck on "thinking" forever.
const TURN_WATCHDOG_MS = 90_000;

function makeId() {
  return Math.random().toString(36).slice(2) + Date.now().toString(36);
}

function newSessionId() {
  if (typeof crypto !== "undefined" && crypto.randomUUID) return crypto.randomUUID();
  return `sess_${Date.now()}_${Math.random().toString(16).slice(2)}`;
}

// A server transcript row as returned by GET /api/sessions/{id}/messages.
type ServerRow = {
  role: ChatRole;
  text: string;
  created_at: string;
  kind?: string;
  seed_kind?: string;
  curiosity_id?: string;
};

// rowToMessage converts a canonical server transcript row into the local
// ChatMessage shape. Single source of truth for the mapping so the
// session-load, switch-session, and reconnect-merge paths stay in sync —
// notably the `seeded` flag that routes the Discuss-with-Jarvis context
// block to DashboardContextCard instead of a plain user bubble.
function rowToMessage(r: ServerRow): ChatMessage {
  return {
    id: makeId(),
    role: r.role,
    text: r.text,
    createdAt: new Date(r.created_at).getTime() || Date.now(),
    seeded: r.kind === "dashboard_seed" || undefined,
    seedKind: r.seed_kind || undefined,
    curiosityId: r.curiosity_id || undefined,
  };
}

function readStoredSessionId(): string {
  if (typeof window === "undefined") return "";
  try {
    return window.localStorage.getItem(SESSION_KEY) || "";
  } catch {
    return "";
  }
}

function writeStoredSessionId(id: string) {
  if (typeof window === "undefined") return;
  try {
    if (id) window.localStorage.setItem(SESSION_KEY, id);
    else window.localStorage.removeItem(SESSION_KEY);
  } catch {
    /* private mode / quota */
  }
}

// Pending / in-flight messages aren't worth caching — they'd hydrate as
// orphaned spinners. The "thinking" placeholder is always dropped (it's
// purely an in-flight affordance). Error messages, however, ARE cached:
// if the boss gets an error and navigates away to fix it, the error
// MUST still be visible when they come back. Errors are durable client
// state; the server never persisted them.
function isCacheable(m: ChatMessage): boolean {
  if (m.role === "thinking") return false;
  if (m.pending && !m.error) return false;
  return true;
}

function readCachedMessages(sessionId: string): ChatMessage[] {
  if (typeof window === "undefined" || !sessionId) return [];
  try {
    const raw = window.localStorage.getItem(MESSAGES_KEY_PREFIX + sessionId);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as ChatMessage[];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function writeCachedMessages(sessionId: string, messages: ChatMessage[]) {
  if (typeof window === "undefined" || !sessionId) return;
  try {
    const trimmed = messages.filter(isCacheable).slice(-MESSAGES_CACHE_LIMIT);
    if (trimmed.length === 0) {
      window.localStorage.removeItem(MESSAGES_KEY_PREFIX + sessionId);
      return;
    }
    window.localStorage.setItem(
      MESSAGES_KEY_PREFIX + sessionId,
      JSON.stringify(trimmed),
    );
  } catch {
    /* private mode / quota — caller can ignore */
  }
}

// mergeServerRows reconciles the canonical transcript from Core with the
// optimistic local state. We keep any *pending* local messages (thinking
// indicators, drafts in flight) and replace finalized turns with whatever
// Core reports — Core is the source of truth for completed turns.
//
// This is the rehydration path used on WS reconnect: a turn the agent
// completed while we were disconnected is in `rows` but missing locally,
// so we append it.
function mergeServerRows(
  local: ChatMessage[],
  rows: ServerRow[],
): ChatMessage[] {
  // Convert server rows into ChatMessage shape with stable created_at
  // timestamps for ordering.
  const fromServer: ChatMessage[] = rows.map(rowToMessage);
  // Detect pending tail items to preserve (in-flight stream, watchdog
  // error bubble we don't want to silently erase).
  const pendingTail: ChatMessage[] = [];
  for (let i = local.length - 1; i >= 0; i--) {
    const m = local[i];
    if (m.pending || m.role === "thinking") {
      pendingTail.unshift(m);
    } else {
      break;
    }
  }
  // De-dupe: drop any pending bubble whose text matches OR is a prefix of
  // a same-role server row. The server's finalized turn always wins.
  //
  // The prefix check matters for voice mode: a streaming assistant bubble
  // may sit at a partial transcript ("Good afternoon, boss. What's on
  // your") while the server has already persisted the completed text
  // ("…What's on your mind today?"). Without the prefix-dedupe we'd
  // render BOTH — the orphaned streaming partial AND the canonical
  // completed turn. The bug looked like the agent "duplicating" itself.
  const sameRoleServer: Map<ChatRole, string[]> = new Map();
  for (const m of fromServer) {
    const list = sameRoleServer.get(m.role) ?? [];
    list.push(m.text.trim());
    sameRoleServer.set(m.role, list);
  }
  const filteredPending = pendingTail.filter((m) => {
    const candidates = sameRoleServer.get(m.role);
    if (!candidates) return true;
    const local = m.text.trim();
    if (!local) return true;
    for (const s of candidates) {
      if (s === local) return false;
      if (s.startsWith(local)) return false;
    }
    return true;
  });
  return [...fromServer, ...filteredPending];
}

// Mark the most recent pending `thinking` message as complete. Called whenever
// the agent transitions out of "thinking" — first text delta, first tool call,
// or stream complete. Returns a new array (never mutates).
function closePendingThinking(messages: ChatMessage[]): ChatMessage[] {
  const next = [...messages];
  for (let i = next.length - 1; i >= 0; i--) {
    if (next[i].role === "thinking" && next[i].pending) {
      next[i] = { ...next[i], pending: false, endedAt: Date.now() };
      break;
    }
  }
  return next;
}

function normalizedVoiceText(text: string): string {
  return text.trim().replace(/\s+/g, " ").toLowerCase();
}

function isDuplicateVoiceAssistantText(a: string, b: string): boolean {
  const left = normalizedVoiceText(a);
  const right = normalizedVoiceText(b);
  if (!left || !right) return false;
  return left === right || left.startsWith(right) || right.startsWith(left);
}

function findLatestPendingAssistant(messages: ChatMessage[]): number {
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].role === "assistant" && messages[i].pending) return i;
  }
  return -1;
}

export function useChat() {
  const ws = useWebSocket();
  const searchParams = useSearchParams();
  const requestedSessionId = searchParams.get("session")?.trim() ?? "";
  // Empty on first server render; assigned client-side in useEffect to avoid
  // hydration mismatches from non-deterministic UUID generation.
  const [sessionId, setSessionId] = useState<string>("");
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [usage, setUsage] = useState<Usage>({ input: 0, output: 0 });
  const [isStreaming, setIsStreaming] = useState(false);
  const turnStartRef = useRef<number | null>(null);
  const watchdogRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Discuss-with-Jarvis opens a session whose only row is a DashboardSeed
  // context block — no agent reply yet. seededKickRef parks such a session
  // id until the socket is connected enough to fire one `resume` turn so
  // the agent actually responds. kickedSessionsRef dedupes so a remount or
  // reconnect never re-fires the opening turn.
  const seededKickRef = useRef<string>("");
  const kickedSessionsRef = useRef<Set<string>>(new Set());

  const clearWatchdog = useCallback(() => {
    if (watchdogRef.current) {
      clearTimeout(watchdogRef.current);
      watchdogRef.current = null;
    }
  }, []);

  const armWatchdog = useCallback(() => {
    clearWatchdog();
    watchdogRef.current = setTimeout(() => {
      watchdogRef.current = null;
      setMessages((prev) => {
        const next = closePendingThinking(prev);
        for (let i = next.length - 1; i >= 0; i--) {
          if (next[i].role === "assistant" && next[i].pending) {
            next[i] = {
              ...next[i],
              pending: false,
              error: "Agent went silent. Tap reconnect or try again.",
            };
            return next;
          }
        }
        next.push({
          id: makeId(),
          role: "assistant",
          text: "",
          error: "Agent went silent. Tap reconnect or try again.",
          createdAt: Date.now(),
        });
        return next;
      });
      turnStartRef.current = null;
      setIsStreaming(false);
    }, TURN_WATCHDOG_MS);
  }, [clearWatchdog]);

  // Fire one `resume` turn for a freshly-seeded session so the agent
  // replies to the dashboard context Discuss-with-Jarvis injected (the
  // context block alone never triggers a turn). No-op once a session has
  // been kicked. If the socket isn't OPEN yet, the id is parked on
  // seededKickRef and the ws.status effect below retries on connect.
  const kickSeeded = useCallback(
    (target: string) => {
      if (!target || kickedSessionsRef.current.has(target)) return;
      const ok = ws.send({ type: "resume", session_id: target });
      if (!ok) {
        seededKickRef.current = target; // retry once connected
        return;
      }
      seededKickRef.current = "";
      kickedSessionsRef.current.add(target);
      turnStartRef.current = Date.now();
      setIsStreaming(true);
      armWatchdog();
      // Show the "Jarvis is thinking" indicator while the resume turn
      // spins up — same optimistic affordance a normal send() gives.
      setMessages((prev) =>
        prev.some((m) => m.role === "thinking" && m.pending)
          ? prev
          : [
              ...prev,
              { id: makeId(), role: "thinking", text: "", pending: true, createdAt: Date.now() },
            ],
      );
    },
    [ws, armWatchdog],
  );
  // Latest-callback ref so the session-load effect can kick a seeded
  // session without taking kickSeeded as a dependency (which would make
  // it re-fetch the transcript on every ws status change).
  const kickSeededRef = useRef(kickSeeded);
  useEffect(() => {
    kickSeededRef.current = kickSeeded;
  }, [kickSeeded]);

  // Retry a parked seeded-session kick once the socket reconnects.
  useEffect(() => {
    if (ws.status === "connected" && seededKickRef.current) {
      kickSeeded(seededKickRef.current);
    }
  }, [ws.status, kickSeeded]);

  // Restore session id from localStorage on mount; mint a fresh one if none.
  // Hydrate from the optimistic local cache *immediately* so a refresh —
  // even one while Core is offline — keeps the visible conversation. Then
  // ask Core for the canonical transcript and overwrite the local cache
  // with whatever Core returns. Core wins; local cache only fills the
  // gap when Core is unreachable. Will be replaced by Supabase Realtime
  // once auth is wired so multiple tabs / devices stay in sync.
  useEffect(() => {
    const stored = readStoredSessionId();
    const id = requestedSessionId || stored || newSessionId();
    setSessionId(id);
    writeStoredSessionId(id);

    const cached = readCachedMessages(id);
    if (cached.length > 0) {
      setMessages(cached);
    } else {
      setMessages([]);
    }

    const ac = new AbortController();
    fetchSessionMessages(id, ac.signal).then((rows) => {
      if (!rows) return;
      const restored: ChatMessage[] = rows.map(rowToMessage);
      // Core is authoritative — overwrite both state and cache.
      setMessages(restored);
      writeCachedMessages(id, restored);
      // A session whose entire transcript is a single seeded context
      // block was just opened from Discuss-with-Jarvis and has no reply
      // yet — fire the opening turn so the agent actually responds.
      if (restored.length === 1 && restored[0].seeded) {
        kickSeededRef.current(id);
      }
    });
    return () => ac.abort();
  }, [requestedSessionId]);

  // Mirror the visible transcript into localStorage whenever it changes.
  // Pending / thinking messages are filtered out by writeCachedMessages
  // so the cache only contains finalized turns.
  useEffect(() => {
    if (!sessionId) return;
    writeCachedMessages(sessionId, messages);
  }, [sessionId, messages]);

  useEffect(() => () => clearWatchdog(), [clearWatchdog]);

  // WS-reconnect rehydration. iOS Safari (and any flaky network) can kill
  // the WebSocket mid-turn. The agent finishes the turn and writes to
  // mem_messages, but the `complete` frame never reaches us — the UI ends
  // up stuck on "thinking forever" until the user nudges the agent.
  //
  // On every connect transition we re-fetch the session's messages from
  // Core and merge in any assistant turns that landed while we were
  // disconnected. Streaming-in-progress is left alone — we only patch
  // when the local state has gaps Core knows about.
  const lastStatusRef = useRef<typeof ws.status>(ws.status);
  useEffect(() => {
    const prevStatus = lastStatusRef.current;
    lastStatusRef.current = ws.status;
    if (ws.status !== "connected") return;
    if (prevStatus === "connected") return; // ignore initial mount; covered by the session-load effect
    if (!sessionId) return;

    // ALWAYS refetch on reconnect — even mid-stream. Turns now run
    // server-side independent of the WS lifecycle (see ws.go startTurn),
    // so a turn the boss kicked off before backgrounding the app on iOS
    // Safari may have completed while disconnected. The server's
    // mem_messages row is the source of truth; reconcile against it.
    const ac = new AbortController();
    void fetchSessionMessages(sessionId, ac.signal).then((rows) => {
      if (!rows || rows.length === 0) return;
      const serverFinishedTurn =
        rows[rows.length - 1]?.role === "assistant";
      setMessages((prev) => {
        const next = mergeServerRows(prev, rows);
        if (!serverFinishedTurn) return next;
        // Server already has a completed assistant turn at the tail.
        // Drop trailing pending bubbles mergeServerRows preserved —
        // the canonical reply replaced them and the `complete` frame
        // landed on a now-dead WS.
        while (next.length > 0) {
          const last = next[next.length - 1];
          if (last.role === "thinking" && last.pending) {
            next.pop();
            continue;
          }
          if (last.role === "assistant" && last.pending && !last.error) {
            next.pop();
            continue;
          }
          break;
        }
        return next;
      });
      if (serverFinishedTurn) {
        turnStartRef.current = null;
        setIsStreaming(false);
        clearWatchdog();
      }
    });
    return () => ac.abort();
  }, [ws.status, sessionId, clearWatchdog]);

  useEffect(() => {
    return ws.subscribe((ev: WSEvent) => {
      if ("session_id" in ev && ev.session_id && ev.session_id !== sessionId) return;

      switch (ev.type) {
        case "thinking": {
          armWatchdog();
          setMessages((prev) => {
            const next = [...prev];
            const last = next[next.length - 1];
            if (last && last.role === "thinking" && last.pending) {
              next[next.length - 1] = { ...last, text: last.text + ev.text };
            } else {
              next.push({
                id: makeId(),
                role: "thinking",
                text: ev.text,
                pending: true,
                createdAt: Date.now(),
              });
            }
            return next;
          });
          break;
        }
        case "delta": {
          armWatchdog();
          setMessages((prev) => {
            const next = closePendingThinking(prev);
            const last = next[next.length - 1];
            if (!last || last.role !== "assistant" || !last.pending) {
              next.push({
                id: makeId(),
                role: "assistant",
                text: ev.text,
                pending: true,
                createdAt: Date.now(),
              });
            } else {
              next[next.length - 1] = { ...last, text: last.text + ev.text };
            }
            return next;
          });
          break;
        }
        case "tool_call": {
          // When the gate parks a call on a Trust contract, the agent
          // loop blocks inside WaitForDecision for up to 15 min. The
          // 90s "agent went silent" watchdog would fire long before
          // the boss could tap Approve — disarm it here so the card
          // can sit waiting indefinitely. tool_result re-arms it.
          if (ev.tool_call.awaiting_approval) {
            clearWatchdog();
          } else {
            armWatchdog();
          }
          setMessages((prev) => [
            ...closePendingThinking(prev),
            {
              id: makeId(),
              role: "tool",
              text: "",
              toolCall: ev.tool_call,
              pending: true,
              createdAt: Date.now(),
            },
          ]);
          break;
        }
        case "tool_result": {
          armWatchdog();
          setMessages((prev) =>
            prev.map((m) =>
              m.role === "tool" && m.toolCall?.id === ev.tool_result.id
                ? { ...m, toolResult: ev.tool_result, pending: false }
                : m,
            ),
          );
          break;
        }
        case "complete": {
          clearWatchdog();
          const inputT = ev.usage?.input ?? 0;
          const outputT = ev.usage?.output ?? 0;
          setUsage((u) => ({ input: u.input + inputT, output: u.output + outputT }));
          const latency = turnStartRef.current ? Date.now() - turnStartRef.current : undefined;
          const interrupted = ev.stop_reason === "interrupted";
          setMessages((prev) => {
            const next = closePendingThinking(prev);
            // Find the most recent pending assistant message and finalize
            // it. When `interrupted`, the partial text already streamed
            // becomes the canonical reply and we mark the bubble so the
            // UI can render a subtle "↩ interrupted" hint. No error state.
            let finalized = false;
            for (let i = next.length - 1; i >= 0; i--) {
              if (next[i].role === "assistant" && next[i].pending) {
                next[i] = {
                  ...next[i],
                  pending: false,
                  inputTokens: inputT,
                  outputTokens: outputT,
                  latencyMs: latency,
                  interrupted: interrupted || undefined,
                };
                finalized = true;
                break;
              }
            }
            // If the user interrupted before any assistant text streamed
            // (stop pressed during pure thinking) there's no pending
            // assistant bubble to finalize. Insert a small marker so the
            // transcript still reflects that the turn ended.
            if (!finalized && interrupted) {
              next.push({
                id: makeId(),
                role: "assistant",
                text: "",
                interrupted: true,
                createdAt: Date.now(),
              });
            }
            // Silent-turn rescue: when a turn completes with no
            // visible assistant text, surface a clear marker so the
            // chat never just stops mid-air. Phrase the marker
            // honestly based on what we know:
            //  - had tool messages in this turn → tools ran, model
            //    just didn't follow up with prose. Asking "what did
            //    you find?" continues naturally.
            //  - output_tokens === 0 → model emitted literally
            //    nothing. Refusal, transient API issue, or upstream
            //    interrupt. Rephrasing usually unsticks it.
            //  - otherwise → generic, with the usage numbers so the
            //    pattern is debuggable if it recurs.
            if (!interrupted) {
              const last = next[next.length - 1];
              const visibleText =
                last && last.role === "assistant"
                  ? last.text.trim()
                  : "";
              const hasAssistantText = !!visibleText;
              if (!hasAssistantText) {
                // Did this turn produce any tool activity? Walk back
                // from the end until we hit the user message that
                // started the turn; any tool entries we cross mean
                // tools did run.
                let toolsRanThisTurn = false;
                for (let i = next.length - 1; i >= 0; i--) {
                  const m = next[i];
                  if (m.role === "user") break;
                  if (m.role === "tool") {
                    toolsRanThisTurn = true;
                    break;
                  }
                }
                let errorText: string;
                if (toolsRanThisTurn) {
                  errorText =
                    "Tools ran above but Jarvis didn't follow up with a reply. Ask \"what did you find?\" to continue.";
                } else if (outputT === 0) {
                  errorText =
                    "Jarvis didn't emit anything this turn (0 output tokens) — likely a transient API hiccup or a soft refusal. Try rephrasing.";
                } else {
                  errorText = `Turn ended without a visible reply (${outputT} output tokens). Ask a follow-up to continue.`;
                }
                const placeholder: ChatMessage = {
                  id: makeId(),
                  role: "assistant",
                  text: "",
                  error: errorText,
                  inputTokens: inputT,
                  outputTokens: outputT,
                  latencyMs: latency,
                  createdAt: Date.now(),
                };
                if (
                  last &&
                  last.role === "assistant" &&
                  !last.text.trim() &&
                  !last.error
                ) {
                  // Reuse the empty finalized bubble in place rather
                  // than stacking a sibling marker.
                  next[next.length - 1] = { ...last, ...placeholder, id: last.id };
                } else {
                  next.push(placeholder);
                }
              }
            }
            return next;
          });
          turnStartRef.current = null;
          setIsStreaming(false);
          break;
        }
        case "steer_received": {
          // Echo for steered input. The originating tab already rendered
          // the bubble optimistically; this exists for multi-tab parity
          // and reconnect cases. Dedup is best-effort: if the most recent
          // user message has the same text and was marked steered, drop.
          setMessages((prev) => {
            for (let i = prev.length - 1; i >= 0; i--) {
              const m = prev[i];
              if (m.role !== "user") continue;
              if (m.steered && m.text === ev.text) return prev;
              break;
            }
            return [
              ...prev,
              {
                id: makeId(),
                role: "user",
                text: ev.text,
                steered: true,
                createdAt: Date.now(),
              },
            ];
          });
          break;
        }
        case "error": {
          clearWatchdog();
          setMessages((prev) => [
            ...closePendingThinking(prev),
            {
              id: makeId(),
              role: "assistant",
              text: "",
              error: ev.message,
              createdAt: Date.now(),
            },
          ]);
          turnStartRef.current = null;
          setIsStreaming(false);
          break;
        }
        case "cleared":
          break;
        case "pong":
          break;
        case "intent": {
          /* IntentFlow classification — consumed by the IntentStream panel
           * via its own /api/intent fetch path, not the chat transcript.
           * Acknowledged here so the WSEvent switch is exhaustive and
           * future TS strictness doesn't blow up. */
          break;
        }
        case "proactive_message": {
          /* Unprompted assistant turn pushed by the heartbeat. Render it
           * as a regular assistant bubble so the transcript reads
           * naturally — the `proactive` flag lets the bubble surface a
           * subtle origin badge ("heartbeat: surprise", etc.) without
           * altering the conversation flow.
           *
           * Actionable system findings should not pollute the live chat
           * transcript. They belong in dashboard / heartbeat surfaces
           * unless they are genuinely conversational. */
          if (ev.finding_kind === "surprise" && !ev.curiosity_id) {
            break;
          }
          clearWatchdog();
          setMessages((prev) => [
            ...closePendingThinking(prev),
            {
              id: makeId(),
              role: "assistant",
              text: ev.text,
              proactive: true,
              proactiveKind: ev.finding_kind,
              curiosityId: ev.curiosity_id,
              createdAt: Date.now(),
            },
          ]);
          break;
        }
      }
    });
  }, [ws, sessionId, armWatchdog, clearWatchdog]);

  const send = useCallback(
    (content: string) => {
      const trimmed = content.trim();
      if (!trimmed || !sessionId) return false;

      // Mid-turn steering. When a turn is already in flight, send the
      // input as `steer` instead of `message` — the server drops it into
      // the running agent loop's steer channel and the loop drains it
      // between iterations. We render the user bubble optimistically
      // with `steered: true` so the transcript distinguishes it. The
      // model in use is resolved server-side from the settings store
      // (not from the WS frame) so there's a single source of truth.
      if (isStreaming) {
        setMessages((prev) => [
          ...prev,
          {
            id: makeId(),
            role: "user",
            text: trimmed,
            steered: true,
            createdAt: Date.now(),
          },
        ]);
        const ok = ws.send({ type: "steer", session_id: sessionId, content: trimmed });
        if (!ok) {
          setMessages((prev) => [
            ...prev,
            {
              id: makeId(),
              role: "assistant",
              text: "",
              error: "Steer dropped — connection is reconnecting. Try again.",
              createdAt: Date.now(),
            },
          ]);
        }
        return ok;
      }

      setMessages((prev) => [
        ...prev,
        { id: makeId(), role: "user", text: trimmed, createdAt: Date.now() },
        // Optimistic "Jarvis is thinking" indicator. Closes on first delta /
        // tool_call / complete. Hidden in the renderer if it ends up empty.
        { id: makeId(), role: "thinking", text: "", pending: true, createdAt: Date.now() },
      ]);
      turnStartRef.current = Date.now();
      setIsStreaming(true);
      armWatchdog();
      const ok = ws.send({ type: "message", session_id: sessionId, content: trimmed });
      if (!ok) {
        clearWatchdog();
        setIsStreaming(false);
        setMessages((prev) => [
          ...prev,
          {
            id: makeId(),
            role: "assistant",
            text: "",
            error: "Not connected to core. Tap reconnect in the footer.",
            createdAt: Date.now(),
          },
        ]);
      }
      return ok;
    },
    [ws, sessionId, isStreaming, armWatchdog, clearWatchdog],
  );

  // interrupt cancels the in-flight turn for the current session. The
  // server cancels the LLM stream context; the agent loop persists
  // whatever partial assistant text streamed and emits a clean
  // complete{stop_reason:"interrupted"} that flips isStreaming off and
  // marks the bubble. Safe to call when nothing is in flight (no-op).
  const interrupt = useCallback(() => {
    if (!sessionId || !isStreaming) return false;
    return ws.send({ type: "interrupt", session_id: sessionId });
  }, [ws, sessionId, isStreaming]);

  const newSession = useCallback(() => {
    clearWatchdog();
    // Drop the previous session's cache too — `/new` is a deliberate
    // reset, the user doesn't want it lingering in storage.
    setSessionId((prev) => {
      if (prev) writeCachedMessages(prev, []);
      return prev;
    });
    const id = newSessionId();
    setSessionId(id);
    writeStoredSessionId(id);
    setMessages([]);
    turnStartRef.current = null;
    setIsStreaming(false);
  }, [clearWatchdog]);

  // switchSession loads an existing session in place — same view, different
  // conversation. Used by the Sessions drawer in the Live header. We hydrate
  // from Core's authoritative transcript and fall back to localStorage only
  // when Core is unreachable.
  const switchSession = useCallback((id: string) => {
    if (!id || id === sessionId) return;
    clearWatchdog();
    setSessionId(id);
    writeStoredSessionId(id);
    setIsStreaming(false);
    turnStartRef.current = null;

    const cached = readCachedMessages(id);
    setMessages(cached);

    const ac = new AbortController();
    fetchSessionMessages(id, ac.signal).then((rows) => {
      if (!rows) return;
      const restored: ChatMessage[] = rows.map(rowToMessage);
      setMessages(restored);
      writeCachedMessages(id, restored);
      // Same seeded-session kick as the mount path: switching into a
      // Discuss-with-Jarvis session that has only the context block and
      // no reply should fire the opening turn.
      if (restored.length === 1 && restored[0].seeded) {
        kickSeededRef.current(id);
      }
    });
  }, [sessionId, clearWatchdog]);

  const clear = useCallback(() => {
    clearWatchdog();
    ws.send({ type: "clear", session_id: sessionId });
    setMessages([]);
    if (sessionId) writeCachedMessages(sessionId, []);
  }, [ws, sessionId, clearWatchdog]);

  // ── Voice integration ──────────────────────────────────────────────────
  //
  // Voice mode (OpenAI Realtime over WebRTC) streams transcripts on the
  // browser side instead of going through the WS turn pipeline. These
  // hooks let the voice client push final user utterances and live
  // assistant deltas straight into the same `messages` array text mode
  // populates — so the conversation reads as one continuous thread,
  // regardless of which modality each turn arrived through.
  //
  // The /api/voice/turn POST still fires on the Core side for memory
  // capture + cross-tab durability; these methods only mirror the same
  // data into the local view for live UX.

  /** Append a finalised user utterance from voice mode. */
  const addVoiceUserMessage = useCallback((text: string) => {
    const trimmed = text.trim();
    if (!trimmed) return;
    const userMessage: ChatMessage = {
      id: makeId(),
      role: "user",
      text: trimmed,
      createdAt: Date.now(),
    };
    setMessages((prev) => {
      const next = [...prev];
      const pendingAssistantIdx = findLatestPendingAssistant(next);
      if (pendingAssistantIdx >= 0) {
        next.splice(pendingAssistantIdx, 0, userMessage);
        return next;
      }
      next.push(userMessage);
      return next;
    });
  }, []);

  /** Stream an assistant delta from voice. Creates a pending assistant
   *  message on the first delta of a response and appends to it on
   *  subsequent deltas. On `isFinal`, the message is committed (loses
   *  its pending flag) so the cache picks it up.
   *
   *  Important: when `isFinal` is true, `delta` is the COMPLETE final
   *  transcript (not an incremental chunk). We replace the bubble's
   *  text wholesale instead of concatenating — otherwise the final
   *  transcript gets appended to the already-accumulated streamed
   *  text, producing duplicated "X X" bubbles. */
  const streamVoiceAssistantDelta = useCallback((delta: string, isFinal: boolean) => {
    setMessages((prev) => {
      const next = [...prev];
      const last = next[next.length - 1];
      if (last && last.role === "assistant" && last.pending) {
        next[next.length - 1] = {
          ...last,
          text: isFinal ? (delta.trim() || last.text) : last.text + delta,
          pending: !isFinal,
        };
        return next;
      }
      const pendingAssistantIdx = findLatestPendingAssistant(next);
      if (pendingAssistantIdx >= 0) {
        const pending = next[pendingAssistantIdx];
        next[pendingAssistantIdx] = {
          ...pending,
          text: isFinal ? (delta.trim() || pending.text) : pending.text + delta,
          pending: !isFinal,
        };
        return next;
      }
      if (isFinal) {
        for (let i = next.length - 1, seen = 0; i >= 0 && seen < 8; i--) {
          if (next[i].role !== "assistant") continue;
          seen++;
          if (isDuplicateVoiceAssistantText(next[i].text, delta)) return next;
        }
      }
      // No in-flight assistant bubble — start one. For the final-only
      // case (no preceding deltas) this captures the full transcript
      // in a single, immediately-committed message.
      next.push({
        id: makeId(),
        role: "assistant",
        text: delta,
        pending: !isFinal,
        createdAt: Date.now(),
      });
      return next;
    });
  }, []);

  const status = ws.status;

  return useMemo(
    () => ({
      sessionId,
      messages,
      usage,
      isStreaming,
      send,
      interrupt,
      newSession,
      switchSession,
      clear,
      status,
      addVoiceUserMessage,
      streamVoiceAssistantDelta,
    }),
    [
      sessionId,
      messages,
      usage,
      isStreaming,
      send,
      interrupt,
      newSession,
      switchSession,
      clear,
      status,
      addVoiceUserMessage,
      streamVoiceAssistantDelta,
    ],
  );
}
