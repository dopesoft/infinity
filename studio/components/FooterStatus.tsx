"use client";

import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";
import { useWebSocket } from "@/lib/ws/provider";
import { fetchCoreStatus, type CoreStatus } from "@/lib/api";

const dotClass = {
  connected: "bg-success",
  connecting: "bg-warning",
  disconnected: "bg-danger",
} as const;

const labelClass = {
  connected: "text-success",
  connecting: "text-warning",
  disconnected: "text-muted-foreground",
} as const;

export function FooterStatus() {
  const ws = useWebSocket();
  const [status, setStatus] = useState<CoreStatus | null>(null);
  const [uptime, setUptime] = useState("0s");
  const [bootTs] = useState(() => Date.now());

  useEffect(() => {
    const ctrl = new AbortController();
    const tick = async () => {
      const s = await fetchCoreStatus(ctrl.signal);
      if (s) setStatus(s);
    };
    tick();
    const id = setInterval(tick, 30_000);
    return () => {
      ctrl.abort();
      clearInterval(id);
    };
  }, []);

  useEffect(() => {
    const t = setInterval(() => {
      const s = Math.floor((Date.now() - bootTs) / 1000);
      setUptime(s < 60 ? `${s}s` : s < 3600 ? `${Math.floor(s / 60)}m` : `${Math.floor(s / 3600)}h`);
    }, 1000);
    return () => clearInterval(t);
  }, [bootTs]);

  const connLabel =
    ws.status === "connected"
      ? "core connected"
      : ws.status === "connecting"
        ? "connecting…"
        : "core offline";

  const provider = status?.provider ?? "—";
  const model = status?.model ?? "—";

  const toolCount = status?.tools?.length ?? 0;
  const sep = <span aria-hidden className="text-border">|</span>;

  return (
    <footer
      className="flex h-9 items-center gap-2.5 border-t bg-background px-4 pb-safe text-xs text-muted-foreground sm:h-10"
      role="contentinfo"
    >
      <button
        type="button"
        onClick={ws.reconnect}
        className="flex min-h-9 items-center gap-2 font-medium"
        title="Click to reconnect"
      >
        <span className={cn("inline-block size-2 rounded-full", dotClass[ws.status])} />
        <span className={cn("truncate", labelClass[ws.status])}>{connLabel}</span>
      </button>
      {sep}
      <span className="rounded-full border border-border/60 bg-muted/40 px-2 py-0.5">
        <span className="tabular-nums">{toolCount}</span> tools
      </span>
      {sep}
      <span className="tabular-nums">{uptime}</span>
      <span className="ml-auto hidden items-center gap-2.5 truncate sm:flex">
        {sep}
        <span className="truncate">{provider} · {model}</span>
      </span>
    </footer>
  );
}
