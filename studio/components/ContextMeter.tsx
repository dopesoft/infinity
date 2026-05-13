"use client";

import { useEffect, useState } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Drawer, DrawerContent, DrawerHeader, DrawerTitle } from "@/components/ui/drawer";
import { useMediaQuery } from "@/lib/use-media-query";
import { useGlobalModel } from "@/lib/use-model";
import { resolveModelEntry } from "@/lib/models-catalog";
import {
  fetchContextUsage,
  type ContextCategoryDTO,
  type ContextUsageDTO,
} from "@/lib/api";
import { cn } from "@/lib/utils";

/**
 * Compact circular context meter — modeled on Claude Code / Codex CLI's
 * context indicator. Renders a tiny SVG ring that fills as the active
 * session's context window approaches capacity. Clicking opens a modal
 * (desktop) or bottom drawer (mobile) with the per-category breakdown.
 */
export function ContextMeter({ sessionId }: { sessionId?: string }) {
  const [data, setData] = useState<ContextUsageDTO | null>(null);
  const [open, setOpen] = useState(false);
  const isDesktop = useMediaQuery("(min-width: 1024px)");
  // Subscribe to the same model broadcast Settings + the chip use — when
  // the active model or provider changes, we re-fetch immediately so the
  // meter never lags the actual runtime.
  const { setting } = useGlobalModel();
  const watchModel = setting?.model ?? "";
  const watchProvider = setting?.provider ?? "";

  // Poll lightly while the composer is mounted. Single in-flight; cheap
  // server-side (chars/4 estimate on already-in-memory state). The deps
  // array also fires on model/provider change so a save in Settings
  // refreshes the meter without waiting for the 8s tick.
  useEffect(() => {
    let cancelled = false;
    const ac = new AbortController();
    async function tick() {
      const next = await fetchContextUsage(sessionId, ac.signal);
      if (!cancelled && next) setData(next);
    }
    void tick();
    const id = window.setInterval(tick, 8000);
    return () => {
      cancelled = true;
      ac.abort();
      window.clearInterval(id);
    };
  }, [sessionId, watchModel, watchProvider]);

  const pct =
    data && data.context_window > 0
      ? Math.min(1, data.used_tokens / data.context_window)
      : 0;

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        title={
          data
            ? `${formatTokens(data.used_tokens)} / ${formatTokens(data.context_window)} (${Math.round(pct * 100)}%)`
            : "Context usage"
        }
        className={cn(
          "inline-flex h-8 w-8 items-center justify-center rounded-full",
          "text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
        )}
        aria-label="Context usage"
      >
        <Ring pct={pct} />
      </button>

      {isDesktop ? (
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogContent className="max-w-md">
            <DialogHeader>
              <DialogTitle>Context usage</DialogTitle>
            </DialogHeader>
            <UsageBody data={data} />
          </DialogContent>
        </Dialog>
      ) : (
        <Drawer open={open} onOpenChange={setOpen}>
          <DrawerContent>
            <DrawerHeader>
              <DrawerTitle>Context usage</DrawerTitle>
            </DrawerHeader>
            <div className="px-4 pb-4">
              <UsageBody data={data} />
            </div>
          </DrawerContent>
        </Drawer>
      )}
    </>
  );
}

// ── Ring ──────────────────────────────────────────────────────────────────
// 18px SVG ring sitting inside the 32px button. Stroke colors shift with
// fill — info under 70%, warning above 70%, danger above 90% — same
// semantics Claude Code uses.
function Ring({ pct }: { pct: number }) {
  const r = 7;
  const c = 2 * Math.PI * r;
  const fill = c * pct;
  const color =
    pct >= 0.9 ? "text-danger" : pct >= 0.7 ? "text-warning" : "text-info";
  return (
    <svg viewBox="0 0 18 18" width={18} height={18} className={color} aria-hidden>
      <circle
        cx={9}
        cy={9}
        r={r}
        fill="none"
        strokeWidth={2}
        className="stroke-border"
      />
      <circle
        cx={9}
        cy={9}
        r={r}
        fill="none"
        strokeWidth={2}
        strokeLinecap="round"
        stroke="currentColor"
        strokeDasharray={`${fill} ${c - fill}`}
        transform="rotate(-90 9 9)"
        style={{ transition: "stroke-dasharray 240ms ease" }}
      />
    </svg>
  );
}

// ── UsageBody ─────────────────────────────────────────────────────────────
// Shared body for the dialog + drawer. Matches the screenshot's layout:
// model id + token totals on top, segmented progress bar, then a table
// of categories with colored dots + token counts + percentages.
function UsageBody({ data }: { data: ContextUsageDTO | null }) {
  if (!data) {
    return (
      <div className="flex min-h-32 items-center justify-center">
        <p className="text-sm text-muted-foreground">Loading…</p>
      </div>
    );
  }
  const pct = data.context_window > 0 ? data.used_tokens / data.context_window : 0;
  // Prefer the catalog's friendly label (e.g. "Opus 4.7") over the raw
  // model id when we recognise it; show the model id as a small caption
  // beneath so the user can still copy it for support.
  const entry = resolveModelEntry(data.model);
  const friendly = entry?.model.label ?? data.model;

  return (
    <div className="space-y-4">
      <div className="space-y-1.5">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <p className="text-base font-semibold tracking-tight">
            {friendly}
            <span className="ml-2 font-mono text-xs text-muted-foreground">
              [{formatTokens(data.context_window)}]
            </span>
          </p>
        </div>
        {entry && (
          <p className="font-mono text-[10px] text-muted-foreground/80">{data.model}</p>
        )}
        <p className="text-sm text-muted-foreground">
          {formatTokens(data.used_tokens)} / {formatTokens(data.context_window)} tokens (
          {Math.round(pct * 100)}%)
        </p>
        <SegmentedBar categories={data.categories} total={data.context_window} />
      </div>

      <table className="w-full text-sm">
        <thead>
          <tr className="border-b text-[10px] font-mono uppercase tracking-wider text-muted-foreground">
            <th className="px-2 py-1.5 text-left font-normal">Category</th>
            <th className="px-2 py-1.5 text-right font-normal">Tokens</th>
            <th className="px-2 py-1.5 text-right font-normal">Usage</th>
          </tr>
        </thead>
        <tbody>
          {data.categories.map((c) => {
            const p =
              data.context_window > 0 ? (c.tokens / data.context_window) * 100 : 0;
            return (
              <tr key={c.id} className="border-b last:border-b-0">
                <td className="px-2 py-2">
                  <span className="inline-flex items-center gap-2">
                    {c.id === "free" ? (
                      <span className="size-2.5" aria-hidden />
                    ) : (
                      <span
                        className={cn("size-2.5 rounded-sm", colorForCategory(c.id))}
                        aria-hidden
                      />
                    )}
                    <span className={c.id === "free" ? "text-muted-foreground" : ""}>
                      {c.label}
                    </span>
                  </span>
                </td>
                <td className="px-2 py-2 text-right font-mono">
                  {formatTokens(c.tokens)}
                </td>
                <td className="px-2 py-2 text-right font-mono text-muted-foreground">
                  {p < 0.1 && c.tokens > 0 ? "<0.1" : p.toFixed(1)}%
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function SegmentedBar({
  categories,
  total,
}: {
  categories: ContextCategoryDTO[];
  total: number;
}) {
  if (total <= 0) return null;
  return (
    <div className="flex h-2 w-full overflow-hidden rounded-full bg-muted">
      {categories.map((c) => {
        const pct = (c.tokens / total) * 100;
        if (pct <= 0) return null;
        if (c.id === "free") {
          return <span key={c.id} style={{ width: `${pct}%` }} />;
        }
        return (
          <span
            key={c.id}
            className={colorForCategory(c.id)}
            style={{ width: `${pct}%` }}
          />
        );
      })}
    </div>
  );
}

// Color palette mirrors Claude Code's: each category gets a distinct hue,
// "free space" stays muted. Tokens come from globals.css — info/warning/
// danger/success + the tier palette — so they swap with the theme.
function colorForCategory(id: string): string {
  switch (id) {
    case "system_prompt":
      return "bg-foreground/60";
    case "tools":
      return "bg-info";
    case "skills":
      return "bg-purple-500";
    case "memory":
      return "bg-warning";
    case "messages":
      return "bg-danger/80";
    case "autocompact":
      return "bg-success/60";
    default:
      return "bg-muted-foreground/40";
  }
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}
