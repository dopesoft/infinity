"use client";

import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";

type ConnState = "connected" | "connecting" | "disconnected";

const dotClass: Record<ConnState, string> = {
  connected: "bg-success",
  connecting: "bg-warning",
  disconnected: "bg-danger",
};

const labelClass: Record<ConnState, string> = {
  connected: "text-success",
  connecting: "text-warning",
  disconnected: "text-muted-foreground",
};

export function FooterStatus() {
  // Phase 0: no live WS yet — start in disconnected. Phase 1 wires real state.
  const [conn] = useState<ConnState>("disconnected");
  const [latencyMs] = useState<number | null>(null);
  const [provider] = useState<string>("anthropic");
  const [model] = useState<string>("claude-sonnet-4-5");
  const [tokensToday] = useState<number>(0);
  const [version] = useState<string>(process.env.NEXT_PUBLIC_BUILD || "dev");
  const [uptime, setUptime] = useState<string>("0s");
  const [bootTs] = useState<number>(() => Date.now());

  useEffect(() => {
    const t = setInterval(() => {
      const s = Math.floor((Date.now() - bootTs) / 1000);
      setUptime(s < 60 ? `${s}s` : s < 3600 ? `${Math.floor(s / 60)}m` : `${Math.floor(s / 3600)}h`);
    }, 1000);
    return () => clearInterval(t);
  }, [bootTs]);

  return (
    <footer
      className="flex h-6 items-center justify-between gap-2 border-t bg-background px-3 text-[11px] text-muted-foreground pb-safe"
      role="contentinfo"
    >
      <div className="flex min-w-0 items-center gap-1.5">
        <span className={cn("inline-block size-1.5 rounded-full", dotClass[conn])} />
        <span className={cn("truncate", labelClass[conn])}>
          {conn === "connected" && latencyMs !== null
            ? `core ${latencyMs}ms`
            : conn === "connecting"
              ? "connecting…"
              : "core offline"}
        </span>
      </div>
      <div className="hidden truncate sm:block">
        {provider} · {model}
      </div>
      <div className="hidden truncate md:block">{tokensToday.toLocaleString()} tok today</div>
      <div className="truncate">
        v{version} · {uptime}
      </div>
    </footer>
  );
}
