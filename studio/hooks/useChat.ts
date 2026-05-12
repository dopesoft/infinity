"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
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
// orphaned spinners. We also drop the optimistic "thinking" placeholder.
function isCacheable(m: ChatMessage): boolean {
  if (m.role === "thinking") return false;
  if (m.pending) return false;
  if (m.error) return false;
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

export function useChat() {
  const ws = useWebSocket();
  // Empty on first server render; assigned client-side in useEffect to avoid
  // hydration mismatches from non-deterministic UUID generation.
  const [sessionId, setSessionId] = useState<string>("");
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [usage, setUsage] = useState<Usage>({ input: 0, output: 0 });
  const [isStreaming, setIsStreaming] = useState(false);
  const turnStartRef = useRef<number | null>(null);
  const watchdogRef = useRef<ReturnType<typeof setTimeout> | null>(null);

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

  // Restore session id from localStorage on mount; mint a fresh one if none.
  // Hydrate from the optimistic local cache *immediately* so a refresh —
  // even one while Core is offline — keeps the visible conversation. Then
  // ask Core for the canonical transcript and overwrite the local cache
  // with whatever Core returns. Core wins; local cache only fills the
  // gap when Core is unreachable. Will be replaced by Supabase Realtime
  // once auth is wired so multiple tabs / devices stay in sync.
  useEffect(() => {
    const stored = readStoredSessionId();
    const id = stored || newSessionId();
    setSessionId(id);
    if (!stored) writeStoredSessionId(id);

    if (!stored) return;

    const cached = readCachedMessages(id);
    if (cached.length > 0) {
      setMessages((prev) => (prev.length === 0 ? cached : prev));
    }

    const ac = new AbortController();
    fetchSessionMessages(id, ac.signal).then((rows) => {
      if (!rows || rows.length === 0) return;
      const restored: ChatMessage[] = rows.map((r) => ({
        id: makeId(),
        role: r.role,
        text: r.text,
        createdAt: new Date(r.created_at).getTime() || Date.now(),
      }));
      // Core is authoritative — overwrite both state and cache.
      setMessages(restored);
      writeCachedMessages(id, restored);
    });
    return () => ac.abort();
  }, []);

  // Mirror the visible transcript into localStorage whenever it changes.
  // Pending / thinking messages are filtered out by writeCachedMessages
  // so the cache only contains finalized turns.
  useEffect(() => {
    if (!sessionId) return;
    writeCachedMessages(sessionId, messages);
  }, [sessionId, messages]);

  useEffect(() => () => clearWatchdog(), [clearWatchdog]);

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
          setMessages((prev) => {
            const next = closePendingThinking(prev);
            for (let i = next.length - 1; i >= 0; i--) {
              if (next[i].role === "assistant" && next[i].pending) {
                next[i] = {
                  ...next[i],
                  pending: false,
                  inputTokens: inputT,
                  outputTokens: outputT,
                  latencyMs: latency,
                };
                break;
              }
            }
            return next;
          });
          turnStartRef.current = null;
          setIsStreaming(false);
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
      }
    });
  }, [ws, sessionId, armWatchdog, clearWatchdog]);

  const send = useCallback(
    (content: string) => {
      const trimmed = content.trim();
      if (!trimmed || isStreaming || !sessionId) return false;
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
      const restored: ChatMessage[] = rows.map((r) => ({
        id: makeId(),
        role: r.role,
        text: r.text,
        createdAt: new Date(r.created_at).getTime() || Date.now(),
      }));
      setMessages(restored);
      writeCachedMessages(id, restored);
    });
  }, [sessionId, clearWatchdog]);

  const clear = useCallback(() => {
    clearWatchdog();
    ws.send({ type: "clear", session_id: sessionId });
    setMessages([]);
    if (sessionId) writeCachedMessages(sessionId, []);
  }, [ws, sessionId, clearWatchdog]);

  const status = ws.status;

  return useMemo(
    () => ({ sessionId, messages, usage, isStreaming, send, newSession, switchSession, clear, status }),
    [sessionId, messages, usage, isStreaming, send, newSession, switchSession, clear, status],
  );
}
