"use client";

import { useEffect, useState } from "react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { ThemeToggle } from "@/components/ThemeToggle";
import { SignOutButton } from "@/components/SignOutButton";
import { useWebSocket } from "@/lib/ws/provider";
import { fetchCoreStatus, type CoreStatus } from "@/lib/api";
import { formatUptime, getBootedAt } from "@/lib/uptime";
import { findVendor, resolveModelEntry, VENDORS } from "@/lib/models-catalog";
import { standbyLabel, useGlobalModel } from "@/lib/use-model";
import { Chip, ChipGroup } from "@/components/ui/chip";
import { cn } from "@/lib/utils";

/**
 * RailStatus - the one dot at the foot of the rail that says whether the
 * machine is alive, and opens to say everything else about it.
 *
 * This REPLACES the desktop footer bar. That bar spent 40px of every screen
 * on four facts you glance at maybe twice a day (connection, tool count,
 * uptime, which model is answering) and it was only ever visible on desktop.
 * All four facts are here, none was dropped, and the theme toggle and sign
 * out come with them so the rail carries everything the old header cluster
 * did.
 *
 * The dot itself is the signal: brand when connected, amber while
 * connecting, red when Core is unreachable. Clicking the dot reconnects,
 * exactly as clicking the footer did.
 */

const DOT = {
  connected: "bg-brand",
  connecting: "bg-warning",
  disconnected: "bg-danger",
} as const;

const LABEL = {
  connected: "Core connected",
  connecting: "Connecting…",
  disconnected: "Core offline",
} as const;

export function RailStatus({ compact = false }: { compact?: boolean }) {
  const ws = useWebSocket();
  const { setting } = useGlobalModel();
  const [status, setStatus] = useState<CoreStatus | null>(null);
  const [uptime, setUptime] = useState("");
  const [bootTs, setBootTs] = useState<number | null>(null);

  useEffect(() => setBootTs(getBootedAt()), []);

  useEffect(() => {
    const ctrl = new AbortController();
    const tick = async () => {
      const s = await fetchCoreStatus(ctrl.signal);
      if (s) setStatus(s);
    };
    void tick();
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

  // Provider id is the source of truth, not the model id: `openai` and
  // `openai_oauth` share model ids, so resolving by model alone mislabels a
  // real ChatGPT-plan turn as an API one.
  const liveProvider = setting?.provider || status?.provider || "";
  const liveModel = setting?.model || status?.model || "";
  const vendor = liveProvider ? (VENDORS.find((v) => v.id === liveProvider) ?? null) : null;
  const modelFromVendor =
    vendor && liveModel ? (vendor.models.find((m) => m.id === liveModel) ?? null) : null;
  const fallback = !vendor && liveModel ? resolveModelEntry(liveModel) : null;
  const vendorLabel =
    vendor?.label ??
    fallback?.vendor.label ??
    (liveProvider ? (findVendor(liveProvider)?.label ?? null) : null) ??
    "-";
  const modelLabel = modelFromVendor?.label ?? fallback?.model.label ?? liveModel ?? "-";
  const standby = standbyLabel(setting?.standby);
  const toolCount = status?.tools?.length ?? 0;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={LABEL[ws.status]}
          className={cn(
            "grid shrink-0 place-items-center rounded-lg text-quiet transition-colors hover:bg-accent/60",
            compact ? "size-11" : "size-9",
          )}
        >
          <span className={cn("size-2 rounded-full", DOT[ws.status])} aria-hidden />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        side={compact ? "bottom" : "right"}
        align="end"
        sideOffset={8}
        className="w-72 p-0"
      >
        {/* The whole row used to BE the reconnect button, with the word
            "reconnect" as grey mono hint text on the right - which reads as a
            label, not something you can press. The state is now text and the
            action is a chip, so what is a control looks like one. */}
        <div className="flex items-center gap-2 border-b border-hairline px-3 py-2">
          <span className={cn("size-2 shrink-0 rounded-full", DOT[ws.status])} aria-hidden />
          <span className="min-w-0 flex-1 truncate text-[13px]">{LABEL[ws.status]}</span>
          <ChipGroup>
            <Chip onClick={ws.reconnect}>Reconnect</Chip>
          </ChipGroup>
        </div>

        <dl className="flex flex-col gap-1.5 border-b border-hairline px-3 py-2.5 text-[12px]">
          <Fact label="Answering" value={standby ? `${standby} · standby` : `${vendorLabel} · ${modelLabel}`} />
          <Fact label="Tools" value={String(toolCount)} />
          <Fact label="Uptime" value={uptime || "—"} />
        </dl>

        {/* These were the nav drawer's rows, reused at half width: 48px tall,
            16px text, 20px icons, in a panel whose other text is 12px. At
            ~113px each "Sign out" wrapped onto two lines. They are chips now,
            the same scale as every other small control in the app. */}
        <div className="p-1.5">
          <ChipGroup size="md" className="w-full">
            <ThemeToggle variant="chip" />
            <SignOutButton variant="chip" />
          </ChipGroup>
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex min-w-0 items-baseline gap-3">
      <dt className="shrink-0 font-mono text-[9.5px] uppercase tracking-[0.12em] text-quiet">
        {label}
      </dt>
      <dd
        className="min-w-0 flex-1 truncate text-right tabular-nums"
        suppressHydrationWarning
      >
        {value}
      </dd>
    </div>
  );
}
