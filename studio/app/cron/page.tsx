"use client";

import { useEffect, useState } from "react";
import { Clock, Pause, Power, Plus, Trash2, Zap } from "lucide-react";
import { AppShell } from "@/components/AppShell";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent } from "@/components/ui/tabs";
import {
  PageTabsList,
  PageTabsTrigger,
  PageSectionHeader,
  HeaderAction,
} from "@/components/ui/page-tabs";
import {
  fetchCrons,
  fetchSentinels,
  deleteCron,
  deleteSentinel,
  triggerCron,
  setCronEnabled,
  type CronJobDTO,
  type SentinelDTO,
} from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";
import { useTabParam } from "@/lib/useTabParam";
import { CronCreateCard, SentinelCreateCard } from "@/components/cron/CreateForms";
import { RunIndicator } from "@/lib/runs";
import { CronDetailModal } from "@/components/cron/CronDetailModal";
import { SentinelDetailModal } from "@/components/cron/SentinelDetailModal";
import { WorkflowsSection } from "@/components/workflows/WorkflowsSection";
import { cronKindMeta, watchTypeMeta, casualTime, cronToHuman, readableName } from "@/components/cron/cronMeta";

// Workflows is the page's primary concept now (repeatable pipelines), with Cron
// (time-fired jobs) and Sentinels (event-fired) as siblings. Workflows leads.
const CRON_TABS = ["workflows", "cron", "sentinel"] as const;
type CronTab = (typeof CRON_TABS)[number];

export default function CronPage() {
  // Active tab persists in ?tab=<id> so a refresh keeps the chosen lens.
  const [tab, setTab] = useTabParam<CronTab>("tab", "workflows", CRON_TABS);
  return (
    <AppShell>
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto pb-safe scroll-touch">
        <div className="flex items-center justify-between gap-3 px-4 py-5 sm:px-6 lg:px-8">
          <h1 className="text-base font-semibold tracking-tight text-foreground">
            Workflows
          </h1>
        </div>
        <Tabs
          value={tab}
          onValueChange={(v) => setTab(v as CronTab)}
          className="flex flex-col"
        >
          <div className="px-4 sm:px-6 lg:px-8">
            <PageTabsList scrollable>
              <PageTabsTrigger value="workflows">Routines</PageTabsTrigger>
              <PageTabsTrigger value="cron">On a schedule</PageTabsTrigger>
              <PageTabsTrigger value="sentinel">Watching</PageTabsTrigger>
            </PageTabsList>
          </div>
          <TabsContent value="workflows" className="flex flex-col px-4 py-5 sm:px-6 lg:px-8">
            <WorkflowsSection />
          </TabsContent>
          <TabsContent value="cron" className="flex flex-col px-4 py-5 sm:px-6 lg:px-8">
            <CronSection />
          </TabsContent>
          <TabsContent value="sentinel" className="flex flex-col px-4 py-5 sm:px-6 lg:px-8">
            <SentinelSection />
          </TabsContent>
        </Tabs>
      </div>
    </AppShell>
  );
}

function CronSection() {
  const [items, setItems] = useState<CronJobDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreate, setShowCreate] = useState(false);
  // Tapping a row opens the full cron (Dialog on desktop, Drawer on mobile).
  const [selected, setSelected] = useState<CronJobDTO | null>(null);

  // Run state is server-tracked via mem_runs (see CLAUDE.md →
  // "Server-tracked progress"). RunIndicator subscribes to the row's
  // runs via useRuns, so the spinner survives navigation, refresh,
  // focus loss, and second-device viewing. Local useState for "running"
  // was the original bug - never reintroduce it.
  async function runNow(id: string) {
    await triggerCron(id);
    // The server's row write also updates last_run_at / last_run_status,
    // which is read from mem_crons; reload to refresh those fields too.
    void load();
  }

  // Pause (disable) or resume (enable) without deleting. The row keeps its
  // history and config; a disabled cron simply drops off the schedule until
  // switched back on. Server reloads the scheduler; realtime + load() refresh
  // the badge.
  async function toggleEnabled(j: CronJobDTO) {
    await setCronEnabled(j.id, !j.enabled);
    void load();
  }

  async function load() {
    setLoading(true);
    const r = await fetchCrons();
    setItems(r ?? []);
    setLoading(false);
  }
  useEffect(() => {
    load();
  }, []);
  useRealtime("mem_crons", load);

  return (
    <div className="flex flex-col gap-3">
      {/* No title: the tab above already says "On a schedule". */}
      <PageSectionHeader title="" count={items.length}>
        <HeaderAction
          icon={<Plus className="size-4" />}
          label={showCreate ? "Cancel" : "New schedule"}
          primary
          onClick={() => setShowCreate((s) => !s)}
        />
      </PageSectionHeader>

      {showCreate && (
        <CronCreateCard
          onCreated={() => {
            setShowCreate(false);
            void load();
          }}
        />
      )}

      <ul className="flex flex-col gap-2">
        {items.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {loading ? "Loading…" : "Nothing runs on a schedule yet."}
          </p>
        ) : (
          items.map((j) => (
            <li
              key={j.id}
              role="button"
              tabIndex={0}
              onClick={() => setSelected(j)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  setSelected(j);
                }
              }}
              className="cursor-pointer rounded-xl border bg-card px-3 py-2 transition-colors hover:bg-muted/40 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                <span className="truncate font-medium text-foreground">{readableName(j.name)}</span>
                {/* Readable cadence is the headline ("every 6 hours"), not the
                    raw cron expression. Author's words win; cronToHuman is the
                    fallback; the raw expression drops to the mono line below. */}
                <span className="flex shrink-0 items-center gap-1">
                  <Clock className="size-3" aria-hidden />
                  {j.schedule_natural || cronToHuman(j.schedule)}
                </span>
              </div>
              <p className="mt-1 font-mono text-[10px] text-muted-foreground/70">{j.schedule}</p>
              <p className="mt-1 line-clamp-2 break-words text-sm">{j.target}</p>
              <div className="mt-1 flex flex-wrap items-center gap-1.5 text-[10px]">
                <Badge variant="outline">{cronKindMeta(j.job_kind).label}</Badge>
                {j.enabled ? (
                  <Badge variant="success">enabled</Badge>
                ) : (
                  <Badge variant="secondary">disabled</Badge>
                )}
                {j.last_run_at && (
                  <span className="text-muted-foreground" suppressHydrationWarning>
                    last {casualTime(j.last_run_at)}
                  </span>
                )}
                {/* Subtle outcome of the most recent run — the full
                    "what I did / how it went" narrative lives one tap away
                    in CronDetailModal, so the row stays quiet. */}
                <RunIndicator kind="cron" targetId={j.id} mode="inline" />
                <div className="ml-auto flex items-center gap-0.5" onClick={(e) => e.stopPropagation()}>
                  <Button
                    size="icon"
                    variant="ghost"
                    onClick={async (e) => {
                      e.stopPropagation();
                      await toggleEnabled(j);
                    }}
                    aria-label={j.enabled ? "Pause this schedule" : "Resume this schedule"}
                    title={
                      j.enabled
            ? "Pause this cron. It stops running on its schedule but keeps its history and config, re-enable anytime."
                        : "Resume this cron. It re-arms on its schedule immediately."
                    }
                  >
                    {j.enabled ? <Pause className="size-4" /> : <Power className="size-4" />}
                  </Button>
                  <RunIndicator
                    kind="cron"
                    targetId={j.id}
                    mode="icon"
                    label="Run now"
                    title="Run it now. The next scheduled run still happens as normal."
                    onRun={() => runNow(j.id)}
                  />
                  <Button
                    size="icon"
                    variant="ghost"
                    onClick={async (e) => {
                      e.stopPropagation();
                      if (confirm(`Delete cron "${j.name}"?`)) {
                        await deleteCron(j.id);
                        void load();
                      }
                    }}
                    aria-label="Delete"
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </div>
              </div>
            </li>
          ))
        )}
      </ul>

      <CronDetailModal
        cron={selected}
        open={selected !== null}
        onOpenChange={(v) => {
          if (!v) setSelected(null);
        }}
        onChanged={() => void load()}
      />
    </div>
  );
}


function SentinelSection() {
  const [items, setItems] = useState<SentinelDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreate, setShowCreate] = useState(false);
  const [selected, setSelected] = useState<SentinelDTO | null>(null);

  async function load() {
    setLoading(true);
    const r = await fetchSentinels();
    setItems(r ?? []);
    setLoading(false);
  }
  useRealtime("mem_sentinels", load);
  useEffect(() => {
    load();
  }, []);

  return (
    <div className="flex flex-col gap-3">
      {/* No title: the tab above already says "Watching". */}
      <PageSectionHeader title="" count={items.length}>
        <HeaderAction
          icon={<Plus className="size-4" />}
          label={showCreate ? "Cancel" : "New watcher"}
          primary
          onClick={() => setShowCreate((s) => !s)}
        />
      </PageSectionHeader>
      {showCreate && (
        <SentinelCreateCard
          onCreated={() => {
            setShowCreate(false);
            void load();
          }}
        />
      )}
      <ul className="flex flex-col gap-2">
        {items.length === 0 ? (
          loading ? (
            <p className="text-sm text-muted-foreground">Loading…</p>
          ) : (
            <SentinelEmptyState />
          )
        ) : (
          items.map((s) => (
            <li
              key={s.id}
              role="button"
              tabIndex={0}
              onClick={() => setSelected(s)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  setSelected(s);
                }
              }}
              className="cursor-pointer rounded-xl border bg-card px-3 py-2 transition-colors hover:bg-muted/40 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                <span className="truncate font-medium text-foreground">{readableName(s.name)}</span>
                <Badge variant="outline" className="shrink-0">{watchTypeMeta(s.watch_type).label}</Badge>
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                cooldown {s.cooldown_seconds}s · fired {s.fire_count}×{" "}
                {s.last_triggered_at && (
                  <span suppressHydrationWarning>· last {casualTime(s.last_triggered_at)}</span>
                )}
              </p>
              <div className="mt-1 flex items-center gap-1.5">
                {s.enabled ? (
                  <Badge variant="success">enabled</Badge>
                ) : (
                  <Badge variant="secondary">disabled</Badge>
                )}
                <Button
                  size="icon"
                  variant="ghost"
                  className="ml-auto"
                  onClick={async (e) => {
                    e.stopPropagation();
                    if (confirm(`Delete sentinel "${s.name}"?`)) {
                      await deleteSentinel(s.id);
                      void load();
                    }
                  }}
                  aria-label="Delete"
                >
                  <Trash2 className="size-4" />
                </Button>
              </div>
            </li>
          ))
        )}
      </ul>

      <SentinelDetailModal
        sentinel={selected}
        open={selected !== null}
        onOpenChange={(v) => {
          if (!v) setSelected(null);
        }}
      />
    </div>
  );
}

/* What the boss sees on an empty Sentinels tab. Answers "how do I even use
 * this?" in-product: explains the cron-vs-sentinel distinction and points at
 * the easy path — just ask Jarvis, who now has sentinel_create (the manual
 * JSON form below is the power-user escape hatch). */
function SentinelEmptyState() {
  const examples = [
    "Ping me when my unread email tops 20.",
    "Watch dopesoft.io and tell me if it goes down.",
    "When a memory mentions “invoice”, run my invoice-filer skill.",
  ];
  return (
    <div className="rounded-xl border bg-card p-4">
      <div className="flex items-center gap-2">
        <Zap className="size-4 text-muted-foreground" aria-hidden />
        <h3 className="text-sm font-medium text-foreground">He isn&apos;t watching for anything yet</h3>
      </div>
      <p className="mt-2 text-sm text-muted-foreground">
        He can wait for something to <span className="text-foreground">happen</span> and act the
        moment it does, rather than at a fixed <span className="text-foreground">time</span>.
      </p>
      <p className="mt-3 text-xs font-medium text-foreground">Easiest way is to ask him:</p>
      <ul className="mt-1.5 flex flex-col gap-1.5">
        {examples.map((e) => (
          <li
            key={e}
            className="rounded-md border bg-muted/30 px-2.5 py-1.5 text-xs text-muted-foreground"
          >
            “{e}”
          </li>
        ))}
      </ul>
      <p className="mt-3 text-[11px] text-muted-foreground">
        He picks how to watch for it and sets it up.
      </p>
    </div>
  );
}

