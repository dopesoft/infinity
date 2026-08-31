"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Zap } from "lucide-react";
import { previewCron, upsertCron, upsertSentinel, type SentinelDTO } from "@/lib/api";
import { CRON_KIND_META, WATCH_TYPE_META, casualTime, localTzAbbrev } from "./cronMeta";

/**
 * The two "make a new automation" forms, extracted from the /cron page so
 * Automations can open either one as a sheet.
 *
 * They were page-local, which is why Automations had to send you to another
 * route to create anything. Nothing about the forms themselves changed — this
 * is a move, so the fields, the defaults and the submit paths are identical.
 */

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
        <Input placeholder="A short name" value={name} onChange={(e) => setName(e.target.value)} />
        <Input placeholder="e.g. 0 9 * * 1-5" value={schedule} onChange={(e) => setSchedule(e.target.value)} />
        <Input
          placeholder="Say it in plain words (optional)"
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
    } catch {
      setError("That is not valid JSON, so I did not save it.");
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
    else setError("That did not save.");
  }

  return (
    <div className="space-y-2 rounded-xl border bg-card p-3">
      <p className="text-[11px] text-muted-foreground">
    Tip: you can skip this form, ask Jarvis “ping me when …” in chat and he&apos;ll set it up.
        Use this if you would rather set it up yourself.
      </p>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <Input placeholder="A short name" value={name} onChange={(e) => setName(e.target.value)} />
        <select
          value={watchType}
          onChange={(e) => setWatchType(e.target.value as SentinelDTO["watch_type"])}
          className="rounded-md border bg-background px-2 py-1 text-sm"
        >
          {/* The readable names already exist in cronMeta's WATCH_TYPE_META
              and this form was printing the raw values beside them. One
              source, so a new watch type shows up here named. */}
          {(Object.keys(WATCH_TYPE_META) as (keyof typeof WATCH_TYPE_META)[]).map((t) => (
            <option key={t} value={t}>
              {WATCH_TYPE_META[t].label}
            </option>
          ))}
        </select>
        <Input
          placeholder="Wait this long between fires (seconds)"
          type="number"
          value={cooldown}
          onChange={(e) => setCooldown(parseInt(e.target.value, 10) || 0)}
        />
        <textarea
          placeholder='What to watch for (JSON)'
          value={config}
          onChange={(e) => setConfig(e.target.value)}
          rows={3}
          className="rounded-md border bg-muted/40 p-2 font-mono text-xs sm:col-span-2"
          spellCheck={false}
        />
        <textarea
          placeholder='What to do when it fires (JSON)'
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

export { CronCreateCard, SentinelCreateCard };
