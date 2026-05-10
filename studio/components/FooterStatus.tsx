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
  const version = process.env.NEXT_PUBLIC_BUILD || "dev";

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

  return (
    <footer
      className="flex h-6 items-center justify-between gap-2 border-t bg-background px-3 text-[11px] text-muted-foreground pb-safe"
      role="contentinfo"
    >
      <button
        type="button"
        onClick={ws.reconnect}
        className="flex min-w-0 items-center gap-1.5"
        title="Click to reconnect"
      >
        <span className={cn("inline-block size-1.5 rounded-full", dotClass[ws.status])} />
        <span className={cn("truncate", labelClass[ws.status])}>{connLabel}</span>
      </button>
      <div className="hidden truncate sm:block">
        {provider} · {model}
      </div>
      <div className="hidden truncate md:block">{(status?.tools?.length ?? 0)} tools</div>
      <div className="truncate">
        v{status?.version || version} · {uptime}
      </div>
    </footer>
  );
}
