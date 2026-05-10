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
  // Then rehydrate the visible transcript from the core, so a refresh keeps
  // the conversation the user could see before reload.
  useEffect(() => {
    const stored = readStoredSessionId();
    const id = stored || newSessionId();
    setSessionId(id);
    if (!stored) writeStoredSessionId(id);

    if (!stored) return; // brand new session, nothing to fetch
    const ac = new AbortController();
    fetchSessionMessages(id, ac.signal).then((rows) => {
      if (!rows || rows.length === 0) return;
      const restored: ChatMessage[] = rows.map((r) => ({
        id: makeId(),
        role: r.role,
        text: r.text,
        createdAt: new Date(r.created_at).getTime() || Date.now(),
      }));
      setMessages((prev) => (prev.length === 0 ? restored : prev));
    });
    return () => ac.abort();
  }, []);

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
          armWatchdog();
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
    const id = newSessionId();
    setSessionId(id);
    writeStoredSessionId(id);
    setMessages([]);
    turnStartRef.current = null;
    setIsStreaming(false);
  }, [clearWatchdog]);

  const clear = useCallback(() => {
    clearWatchdog();
    ws.send({ type: "clear", session_id: sessionId });
    setMessages([]);
  }, [ws, sessionId, clearWatchdog]);

  const status = ws.status;

  return useMemo(
    () => ({ sessionId, messages, usage, isStreaming, send, newSession, clear, status }),
    [sessionId, messages, usage, isStreaming, send, newSession, clear, status],
  );
}
