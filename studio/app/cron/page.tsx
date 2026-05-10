"use client";

import { useEffect, useState } from "react";
import { Clock, Plus, RefreshCw, Trash2, Zap } from "lucide-react";
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
  type CronJobDTO,
  type SentinelDTO,
} from "@/lib/api";

export default function CronPage() {
  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <Tabs defaultValue="cron" className="flex min-h-0 flex-1 flex-col">
          <div className="border-b px-3 py-3 sm:px-4">
            <PageTabsList columns={2}>
              <PageTabsTrigger value="cron">Cron</PageTabsTrigger>
              <PageTabsTrigger value="sentinel">Sentinels</PageTabsTrigger>
            </PageTabsList>
          </div>
          <TabsContent value="cron" className="flex min-h-0 flex-1 flex-col px-3 py-3 sm:px-4">
            <CronSection />
          </TabsContent>
          <TabsContent value="sentinel" className="flex min-h-0 flex-1 flex-col px-3 py-3 sm:px-4">
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

  async function load() {
    setLoading(true);
    const r = await fetchCrons();
    setItems(r ?? []);
    setLoading(false);
  }
  useEffect(() => {
    load();
  }, []);

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3">
      <PageSectionHeader title="scheduled jobs" count={items.length}>
        <HeaderAction
          icon={<Plus className="size-4" />}
          label={showCreate ? "Cancel" : "New cron"}
          primary
          onClick={() => setShowCreate((s) => !s)}
        />
        <HeaderAction
          icon={<RefreshCw className="size-4" />}
          label="Refresh"
          onClick={load}
          disabled={loading}
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

      <ul className="flex flex-col gap-2 overflow-y-auto scroll-touch">
        {items.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {loading ? "Loading…" : "No crons yet."}
          </p>
        ) : (
          items.map((j) => (
            <li key={j.id} className="rounded-xl border bg-card px-3 py-2">
              <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                <code className="truncate font-mono text-foreground">{j.name}</code>
                <span className="flex items-center gap-1 font-mono">
                  <Clock className="size-3" aria-hidden />
                  {j.schedule}
                </span>
              </div>
              {j.schedule_natural && (
                <p className="mt-1 text-xs text-muted-foreground">{j.schedule_natural}</p>
              )}
              <p className="mt-1 line-clamp-2 break-words text-sm">{j.target}</p>
              <div className="mt-1 flex flex-wrap items-center gap-1.5 text-[10px]">
                <Badge variant="outline" className="font-mono uppercase">{j.job_kind}</Badge>
                {j.enabled ? (
                  <Badge variant="success">enabled</Badge>
                ) : (
                  <Badge variant="secondary">disabled</Badge>
                )}
                {j.last_run_at && (
                  <span className="font-mono text-muted-foreground" suppressHydrationWarning>
                    last {new Date(j.last_run_at).toLocaleString()}
                  </span>
                )}
                <Button
                  size="icon"
                  variant="ghost"
                  className="ml-auto"
                  onClick={async () => {
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
            </li>
          ))
        )}
      </ul>
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
      <div className="grid gap-2 sm:grid-cols-2">
        <Input placeholder="name (kebab-case)" value={name} onChange={(e) => setName(e.target.value)} />
        <Input placeholder="schedule (cron)" value={schedule} onChange={(e) => setSchedule(e.target.value)} />
        <Input
          placeholder="natural language label (optional)"
          value={scheduleNatural}
          onChange={(e) => setScheduleNatural(e.target.value)}
          className="sm:col-span-2"
        />
        <textarea
          placeholder="prompt or instructions for the agent"
          value={target}
          onChange={(e) => setTarget(e.target.value)}
          rows={3}
          className="rounded-md border bg-muted/40 p-2 text-sm sm:col-span-2"
        />
        <div className="flex items-center gap-2 sm:col-span-2">
          <label className="flex items-center gap-2 text-xs">
            <input
              type="radio"
              name="kind"
              checked={kind === "isolated_agent_turn"}
              onChange={() => setKind("isolated_agent_turn")}
            />
            isolated_agent_turn
          </label>
          <label className="flex items-center gap-2 text-xs">
            <input
              type="radio"
              name="kind"
              checked={kind === "system_event"}
              onChange={() => setKind("system_event")}
            />
            system_event
          </label>
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
        <ul className="ml-4 list-disc space-y-0.5 text-xs">
          {previewNext.map((t, i) => (
            <li key={i} className="font-mono" suppressHydrationWarning>
              {new Date(t).toLocaleString()}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function SentinelSection() {
  const [items, setItems] = useState<SentinelDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreate, setShowCreate] = useState(false);

  async function load() {
    setLoading(true);
    const r = await fetchSentinels();
    setItems(r ?? []);
    setLoading(false);
  }
  useEffect(() => {
    load();
  }, []);

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3">
      <PageSectionHeader title="sentinels" count={items.length}>
        <HeaderAction
          icon={<Plus className="size-4" />}
          label={showCreate ? "Cancel" : "New sentinel"}
          primary
          onClick={() => setShowCreate((s) => !s)}
        />
        <HeaderAction
          icon={<RefreshCw className="size-4" />}
          label="Refresh"
          onClick={load}
          disabled={loading}
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
      <ul className="flex flex-col gap-2 overflow-y-auto scroll-touch">
        {items.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {loading ? "Loading…" : "No sentinels yet."}
          </p>
        ) : (
          items.map((s) => (
            <li key={s.id} className="rounded-xl border bg-card px-3 py-2">
              <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                <code className="truncate font-mono text-foreground">{s.name}</code>
                <Badge variant="outline" className="font-mono uppercase">{s.watch_type}</Badge>
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                cooldown {s.cooldown_seconds}s · fired {s.fire_count}×{" "}
                {s.last_triggered_at && (
                  <span suppressHydrationWarning>
                    · last {new Date(s.last_triggered_at).toLocaleString()}
                  </span>
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
                  onClick={async () => {
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
      <div className="grid gap-2 sm:grid-cols-2">
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
