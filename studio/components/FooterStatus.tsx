"use client";

import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";
import { useWebSocket } from "@/lib/ws/provider";
import { fetchCoreStatus, type CoreStatus } from "@/lib/api";
import { formatUptime, getBootedAt } from "@/lib/uptime";
import { findVendor, resolveModelEntry } from "@/lib/models-catalog";

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
  const [uptime, setUptime] = useState("");
  // Resolve the persisted boot timestamp client-side only, otherwise
  // SSR renders one value and the client re-renders another → hydration
  // mismatch. The footer just shows nothing for the first frame instead.
  const [bootTs, setBootTs] = useState<number | null>(null);
  useEffect(() => {
    setBootTs(getBootedAt());
  }, []);

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
    if (bootTs == null) return;
    const tick = () => setUptime(formatUptime(Date.now() - bootTs));
    tick();
    const t = setInterval(tick, 1000);
    return () => clearInterval(t);
  }, [bootTs]);

  const connLabel =
    ws.status === "connected"
      ? "core connected"
      : ws.status === "connecting"
        ? "connecting…"
        : "core offline";

  // Resolve raw provider id + model id to the friendly labels owned by the
  // catalog (Settings + ModelChip use the same source of truth). Falls back
  // to whatever Core returned when an id isn't catalogued — better an ugly
  // slug than a missing chip.
  const entry = status?.model ? resolveModelEntry(status.model) : null;
  const vendorLabel = entry?.vendor.label
    ?? (status?.provider ? findVendor(status.provider).label : null)
    ?? status?.provider
    ?? "—";
  const modelLabel = entry?.model.label ?? status?.model ?? "—";

  const toolCount = status?.tools?.length ?? 0;
  const sep = <span aria-hidden className="text-border">|</span>;

  return (
    <footer
      className="flex h-10 items-center gap-2 whitespace-nowrap border-t bg-background px-3 pb-safe text-xs text-muted-foreground sm:gap-2.5 sm:px-4"
      role="contentinfo"
    >
      <button
        type="button"
        onClick={ws.reconnect}
        className="flex min-h-9 items-center gap-1.5 font-medium sm:gap-2"
        title="Click to reconnect"
      >
        <span className={cn("inline-block size-2 rounded-full", dotClass[ws.status])} />
        <span className={cn(labelClass[ws.status])}>{connLabel}</span>
      </button>
      {sep}
      <span className="flex items-center gap-1.5">
        <span className="rounded-full border border-border/60 bg-muted/40 px-2 py-0.5 tabular-nums">
          {toolCount}
        </span>
        <span>tools</span>
      </span>
      {sep}
      <span className="tabular-nums" suppressHydrationWarning>
        {uptime}
      </span>
      <span className="ml-auto hidden items-center gap-2.5 truncate sm:flex">
        {sep}
        <span className="truncate">{vendorLabel} · {modelLabel}</span>
      </span>
    </footer>
  );
}
