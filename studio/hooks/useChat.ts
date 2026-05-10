"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { WSEvent, WSToolEvent } from "@/lib/ws/client";
import { useWebSocket } from "@/lib/ws/provider";

export type ChatRole = "user" | "assistant" | "tool";

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
};

type Usage = { input: number; output: number };

function makeId() {
  return Math.random().toString(36).slice(2) + Date.now().toString(36);
}

function newSessionId() {
  return crypto.randomUUID
    ? crypto.randomUUID()
    : `sess_${Date.now()}_${Math.random().toString(16).slice(2)}`;
}

export function useChat() {
  const ws = useWebSocket();
  const [sessionId, setSessionId] = useState<string>(() => newSessionId());
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [usage, setUsage] = useState<Usage>({ input: 0, output: 0 });
  const [isStreaming, setIsStreaming] = useState(false);
  const turnStartRef = useRef<number | null>(null);

  useEffect(() => {
    return ws.subscribe((ev: WSEvent) => {
      if ("session_id" in ev && ev.session_id && ev.session_id !== sessionId) return;

      switch (ev.type) {
        case "delta": {
          setMessages((prev) => {
            const next = [...prev];
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
          setMessages((prev) => [
            ...prev,
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
          const inputT = ev.usage?.input ?? 0;
          const outputT = ev.usage?.output ?? 0;
          setUsage((u) => ({ input: u.input + inputT, output: u.output + outputT }));
          const latency = turnStartRef.current ? Date.now() - turnStartRef.current : undefined;
          setMessages((prev) => {
            const next = [...prev];
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
          setMessages((prev) => [
            ...prev,
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
  }, [ws, sessionId]);

  const send = useCallback(
    (content: string) => {
      const trimmed = content.trim();
      if (!trimmed || isStreaming) return false;
      setMessages((prev) => [
        ...prev,
        { id: makeId(), role: "user", text: trimmed, createdAt: Date.now() },
      ]);
      turnStartRef.current = Date.now();
      setIsStreaming(true);
      const ok = ws.send({ type: "message", session_id: sessionId, content: trimmed });
      if (!ok) {
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
    [ws, sessionId, isStreaming],
  );

  const newSession = useCallback(() => {
    setSessionId(newSessionId());
    setMessages([]);
    turnStartRef.current = null;
    setIsStreaming(false);
  }, []);

  const clear = useCallback(() => {
    ws.send({ type: "clear", session_id: sessionId });
    setMessages([]);
  }, [ws, sessionId]);

  const status = ws.status;

  return useMemo(
    () => ({ sessionId, messages, usage, isStreaming, send, newSession, clear, status }),
    [sessionId, messages, usage, isStreaming, send, newSession, clear, status],
  );
}
