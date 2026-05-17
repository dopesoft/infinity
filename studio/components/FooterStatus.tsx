"use client";

import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";
import { useWebSocket } from "@/lib/ws/provider";
import { fetchCoreStatus, type CoreStatus } from "@/lib/api";
import { formatUptime, getBootedAt } from "@/lib/uptime";
import { findVendor, resolveModelEntry, VENDORS } from "@/lib/models-catalog";
import { useGlobalModel } from "@/lib/use-model";

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
  // Subscribe to the shared model/provider setting - both the composer's
  // ModelChip and the Settings page write through this same hook, so any
  // change there reaches the footer on the same tick (no 30s polling lag).
  const { setting } = useGlobalModel();
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
  // catalog (Settings + ModelChip use the same source of truth).
  //
  // Prefer the live `useGlobalModel` setting over the polled /api/status
  // snapshot - when the chip cycles or Settings saves, the hook broadcasts
  // synchronously and the footer re-renders the same tick. /api/status is
  // still the fallback so first paint isn't blank.
  //
  // CRITICAL: provider id (not model id) is the source of truth - `openai`
  // and `openai_oauth` share most model ids (gpt-5.4-mini etc.), so a naive
  // resolveModelEntry() walk picks `openai` (API key) first and would
  // mislabel a real ChatGPT-Plan turn. Look up the vendor by provider id
  // first, then look up the model inside THAT vendor's list.
  const liveProvider = setting?.provider || status?.provider || "";
  const liveModel = setting?.model || status?.model || "";

  const vendor = liveProvider
    ? VENDORS.find((v) => v.id === liveProvider) ?? null
    : null;
  const modelFromVendor = vendor && liveModel
    ? vendor.models.find((m) => m.id === liveModel) ?? null
    : null;
  const fallbackEntry =
    !vendor && liveModel ? resolveModelEntry(liveModel) : null;

  const vendorLabel =
    vendor?.label
    ?? fallbackEntry?.vendor.label
    ?? (liveProvider ? findVendor(liveProvider).label : null)
    ?? liveProvider
    ?? "-";
  const modelLabel =
    modelFromVendor?.label
    ?? fallbackEntry?.model.label
    ?? liveModel
    ?? "-";

  const toolCount = status?.tools?.length ?? 0;
  const sep = <span aria-hidden className="text-border">|</span>;

  return (
    <footer
      className="hidden h-10 items-center gap-2 whitespace-nowrap border-t bg-background px-3 pb-safe text-xs text-muted-foreground sm:gap-2.5 sm:px-4 lg:flex"
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
