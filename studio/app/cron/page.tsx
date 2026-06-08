"use client";

import { useEffect, useState } from "react";
import { Clock, Plus, Trash2, Zap } from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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
  upsertCron,
  upsertSentinel,
  deleteCron,
  deleteSentinel,
  previewCron,
  triggerCron,
  type CronJobDTO,
  type SentinelDTO,
} from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";
import { useTabParam } from "@/lib/useTabParam";
import { RunIndicator } from "@/lib/runs";
import { CronDetailModal } from "@/components/cron/CronDetailModal";
import { SentinelDetailModal } from "@/components/cron/SentinelDetailModal";
import { WorkflowsSection } from "@/components/workflows/WorkflowsSection";
import { cronKindMeta, watchTypeMeta, casualTime, localTzAbbrev, cronToHuman, CRON_KIND_META } from "@/components/cron/cronMeta";

// Workflows is the page's primary concept now (repeatable pipelines), with Cron
// (time-fired jobs) and Sentinels (event-fired) as siblings. Workflows leads.
const CRON_TABS = ["workflows", "cron", "sentinel"] as const;
type CronTab = (typeof CRON_TABS)[number];

export default function CronPage() {
  // Active tab persists in ?tab=<id> so a refresh keeps the chosen lens.
  const [tab, setTab] = useTabParam<CronTab>("tab", "workflows", CRON_TABS);
  return (
    <TabFrame>
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
          <div className="px-4 pb-3 sm:px-6 lg:px-8">
            <PageTabsList scrollable>
              <PageTabsTrigger value="workflows">Workflows</PageTabsTrigger>
              <PageTabsTrigger value="cron">Cron</PageTabsTrigger>
              <PageTabsTrigger value="sentinel">Sentinels</PageTabsTrigger>
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
    </TabFrame>
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
      <PageSectionHeader title="scheduled jobs" count={items.length}>
        <HeaderAction
          icon={<Plus className="size-4" />}
          label={showCreate ? "Cancel" : "New cron"}
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
            {loading ? "Loading…" : "No crons yet."}
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
                <code className="truncate font-mono text-foreground">{j.name}</code>
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
                  <RunIndicator
                    kind="cron"
                    targetId={j.id}
                    mode="icon"
                    label="Run now"
                    title="Fire this cron immediately, regardless of schedule. The next regular fire still happens on the cron expression's next tick. Progress survives navigation and refresh."
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
      />
    </div>
  );
}

function CronCreateCard({ onCreated }: { onCreated: () => void }) {
  const [name, setName] = useState("");
  const [schedule, setSchedule] = useState("0 9 * * 1-5");
  const [scheduleNatural, setScheduleNatural] = useState("");
  const [target, setTarget] = useState("");
  const [kind, setKind] = useState<"system_event" | "isolated_agent_turn">("isolated_agent_turn");
  const [previewNext, setPreviewNext] = useState<string[] | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  // Card mounts only on the client (gated by showCreate), so reading the
  // browser tz here is hydration-safe — there is no SSR pass for this subtree.
  const localTz = localTzAbbrev();

  async function preview() {
    setPreviewError(null);
    setPreviewNext(null);
    const r = await previewCron(schedule, 3);
    if (!r) {
      setPreviewError("network error");
      return;
    }
    if ("error" in r) {
      setPreviewError(r.error);
      return;
    }
    setPreviewNext(r.next);
  }

  async function save() {
    setSaving(true);
    const r = await upsertCron({
      name,
      schedule,
      schedule_natural: scheduleNatural,
      target,
      job_kind: kind,
      enabled: true,
      max_retries: 3,
      backoff_seconds: 60,
    });
    setSaving(false);
    if (r) onCreated();
  }

  return (
    <div className="space-y-2 rounded-xl border bg-card p-3">
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <Input placeholder="name (kebab-case)" value={name} onChange={(e) => setName(e.target.value)} />
        <Input placeholder="schedule (cron)" value={schedule} onChange={(e) => setSchedule(e.target.value)} />
        <Input
          placeholder="natural language label (optional)"
          value={scheduleNatural}
          onChange={(e) => setScheduleNatural(e.target.value)}
          className="sm:col-span-2"
        />
        <p className="text-[11px] text-muted-foreground sm:col-span-2">
          Schedules run on your local time ({localTz}). e.g. <code className="font-mono">0 9 * * 1-5</code> = 9am every weekday.
        </p>
        <textarea
          placeholder="prompt or instructions for the agent"
          value={target}
          onChange={(e) => setTarget(e.target.value)}
          rows={3}
          className="rounded-md border bg-muted/40 p-2 text-sm sm:col-span-2"
        />
        <div className="flex flex-col gap-2 sm:col-span-2">
          {(["isolated_agent_turn", "system_event"] as const).map((k) => (
            <label
              key={k}
              className="flex cursor-pointer items-start gap-2 rounded-md border p-2 text-xs has-[:checked]:border-foreground/40 has-[:checked]:bg-muted/40"
            >
              <input
                type="radio"
                name="kind"
                className="mt-0.5"
                checked={kind === k}
                onChange={() => setKind(k)}
              />
              <span className="min-w-0">
                <span className="font-medium text-foreground">{CRON_KIND_META[k].label}</span>
                <span className="mt-0.5 block text-muted-foreground">{CRON_KIND_META[k].blurb}</span>
              </span>
            </label>
          ))}
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" variant="outline" onClick={preview}>
          Preview next 3 fires
        </Button>
        <Button size="sm" onClick={save} disabled={!name.trim() || !schedule.trim() || saving}>
          <Zap className="mr-1 size-4" />
          {saving ? "saving…" : "Save"}
        </Button>
      </div>
      {previewError && <p className="text-xs text-danger">{previewError}</p>}
      {previewNext && (
        <div className="text-xs">
          <p className="text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
            Next fires · {localTz}
          </p>
          <ul className="ml-4 mt-1 list-disc space-y-0.5">
            {previewNext.map((t, i) => (
              <li key={i} suppressHydrationWarning>
                {casualTime(t)}
              </li>
            ))}
          </ul>
        </div>
      )}
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
      <PageSectionHeader title="sentinels" count={items.length}>
        <HeaderAction
          icon={<Plus className="size-4" />}
          label={showCreate ? "Cancel" : "New sentinel"}
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
                <code className="truncate font-mono text-foreground">{s.name}</code>
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
        <h3 className="text-sm font-medium text-foreground">No sentinels yet</h3>
      </div>
      <p className="mt-2 text-sm text-muted-foreground">
        A sentinel watches for an <span className="text-foreground">event</span> and acts when it
        happens — the event-driven sibling of a cron, which fires at a fixed{" "}
        <span className="text-foreground">time</span>. When it trips, it runs a skill.
      </p>
      <p className="mt-3 text-xs font-medium text-foreground">
        Easiest way: just ask Jarvis in chat —
      </p>
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
        He picks the right watch type and wires it up. Or use{" "}
        <span className="text-foreground">New sentinel</span> above to set one up by hand.
      </p>
    </div>
  );
}

function SentinelCreateCard({ onCreated }: { onCreated: () => void }) {
  const [name, setName] = useState("");
  const [watchType, setWatchType] = useState<SentinelDTO["watch_type"]>("webhook");
  const [cooldown, setCooldown] = useState(300);
  const [config, setConfig] = useState("{}");
  const [actions, setActions] = useState("[]");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  async function save() {
    setError(null);
    let cfg: Record<string, unknown> = {};
    let chain: Array<Record<string, unknown>> = [];
    try {
      cfg = config.trim() ? JSON.parse(config) : {};
      chain = actions.trim() ? JSON.parse(actions) : [];
    } catch (e) {
      setError(`JSON parse: ${String(e)}`);
      return;
    }
    setSaving(true);
    const r = await upsertSentinel({
      name,
      watch_type: watchType,
      watch_config: cfg,
      action_chain: chain,
      cooldown_seconds: cooldown,
      enabled: true,
    });
    setSaving(false);
    if (r) onCreated();
    else setError("save failed");
  }

  return (
    <div className="space-y-2 rounded-xl border bg-card p-3">
      <p className="text-[11px] text-muted-foreground">
        Tip: you can skip this form — ask Jarvis “ping me when …” in chat and he&apos;ll set it up.
        This manual form is for wiring the watch config + action chain by hand.
      </p>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <Input placeholder="name (kebab-case)" value={name} onChange={(e) => setName(e.target.value)} />
        <select
          value={watchType}
          onChange={(e) => setWatchType(e.target.value as SentinelDTO["watch_type"])}
          className="rounded-md border bg-background px-2 py-1 text-sm"
        >
          <option value="webhook">webhook</option>
          <option value="file_change">file_change</option>
          <option value="memory_event">memory_event</option>
          <option value="external_api_poll">external_api_poll</option>
          <option value="threshold">threshold</option>
        </select>
        <Input
          placeholder="cooldown seconds"
          type="number"
          value={cooldown}
          onChange={(e) => setCooldown(parseInt(e.target.value, 10) || 0)}
        />
        <textarea
          placeholder='watch config (JSON)'
          value={config}
          onChange={(e) => setConfig(e.target.value)}
          rows={3}
          className="rounded-md border bg-muted/40 p-2 font-mono text-xs sm:col-span-2"
          spellCheck={false}
        />
        <textarea
          placeholder='action chain (JSON array of {kind, args})'
          value={actions}
          onChange={(e) => setActions(e.target.value)}
          rows={3}
          className="rounded-md border bg-muted/40 p-2 font-mono text-xs sm:col-span-2"
          spellCheck={false}
        />
      </div>
      <div className="flex items-center gap-2">
        <Button size="sm" onClick={save} disabled={!name.trim() || saving}>
          <Zap className="mr-1 size-4" />
          {saving ? "saving…" : "Save"}
        </Button>
        {error && <p className="text-xs text-danger">{error}</p>}
      </div>
    </div>
  );
}
