"use client";

import { useWebSocket } from "@/lib/ws/provider";
import { StatusPill, type AgentState } from "@/components/StatusPill";

/* When no explicit state is provided, derive it from the WS connection so
 * the header pill never lies on tabs that don't drive their own state. */
export function AutoStatusPill() {
  const ws = useWebSocket();
  const state: AgentState =
    ws.status === "connected"
      ? "listening"
      : ws.status === "connecting"
        ? "idle"
        : "offline";
  return <StatusPill state={state} />;
}
