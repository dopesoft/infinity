"use client";

import { useEffect, useState } from "react";
import { CalendarClock, Clock } from "lucide-react";
import { ResponsiveModal, ResponsiveModalHeader } from "@/components/ui/responsive-modal";
import { ModalSection, ModalPre, ModalDl } from "@/components/ui/modal-content";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { RunIndicator, useRuns } from "@/lib/runs";
import { previewCron, triggerCron, type CronJobDTO } from "@/lib/api";
import { cronKindMeta, casualTime, localTzAbbrev } from "./cronMeta";

/* The full cron, on click. Closes the boss's "I can't even read the entire
 * cron" gap: the list row shows a title + schedule; tapping it opens this
 * (Dialog on desktop, Drawer on mobile via ResponsiveModal) with the WHOLE
 * prompt, the schedule explained in his local time, the next fires, and the
 * last run's narrative — no truncation. */
export function CronDetailModal({
  cron,
  open,
  onOpenChange,
}: {
  cron: CronJobDTO | null;
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const [nextFires, setNextFires] = useState<string[] | null>(null);
  const [previewErr, setPreviewErr] = useState<string | null>(null);

  // Last-run narrative is server state (mem_runs), same source the list
  // spinner reads, so the detail survives navigation/refresh and shows the
  // "what I did / how it went" report rather than a bare status string.
  const { latest } = useRuns({
    kind: "cron",
    targetId: cron?.id,
    limit: 1,
  });

  useEffect(() => {
    if (!open || !cron) return;
    setNextFires(null);
    setPreviewErr(null);
    let alive = true;
    previewCron(cron.schedule, 3).then((r) => {
      if (!alive) return;
      if (!r) return setPreviewErr("couldn't compute the next fires");
      if ("error" in r) return setPreviewErr(r.error);
      setNextFires(r.next);
    });
    return () => {
      alive = false;
    };
  }, [open, cron]);

  if (!cron) return null;

  const meta = cronKindMeta(cron.job_kind);
  const tz = localTzAbbrev();
  const runStatus = latest?.status;
  const runNarrative = latest?.result_summary?.trim();

  return (
    <ResponsiveModal
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title={cron.name}
      description={meta.label}
      header={
        <ResponsiveModalHeader
          icon={<CalendarClock className="size-4" />}
          eyebrow={meta.label}
          title={cron.name}
          subtitle={cron.schedule_natural || `runs on schedule ${cron.schedule}`}
          trailing={
            cron.enabled ? (
              <Badge variant="success">enabled</Badge>
            ) : (
              <Badge variant="secondary">disabled</Badge>
            )
          }
        />
      }
      footer={
        <>
          <RunIndicator
            kind="cron"
            targetId={cron.id}
            label="Run now"
            title="Fire this cron immediately, regardless of schedule. Progress survives navigation and refresh."
            showResult={false}
            onRun={async () => {
              await triggerCron(cron.id);
            }}
          />
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>
            Close
          </Button>
        </>
      }
    >
      {/* What it actually does — the full prompt/target, never clamped. */}
      <ModalSection label={cron.job_kind === "system_task" ? "Maintenance task" : "What it does"}>
        <ModalPre>{cron.target || "(no instructions stored)"}</ModalPre>
        <p className="mt-2 text-[12px] text-muted-foreground">{meta.blurb}</p>
      </ModalSection>

      {/* Schedule — explained in the boss's local frame. ONE convention. */}
      <ModalSection label="Schedule" icon={<Clock className="size-3.5 text-muted-foreground" />}>
        <p className="text-[13px] text-foreground/90">
          {cron.schedule_natural || "Runs on a fixed schedule."}
        </p>
        <p className="mt-1 font-mono text-[11px] text-muted-foreground">{cron.schedule}</p>
        <div className="mt-3">
          <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
            Next fires · {tz}
          </p>
          {previewErr ? (
            <p className="mt-1 text-[12px] text-danger">{previewErr}</p>
          ) : nextFires === null ? (
            <p className="mt-1 text-[12px] text-muted-foreground">computing…</p>
          ) : (
            <ul className="mt-1 space-y-0.5">
              {nextFires.map((t, i) => (
                <li key={i} className="text-[13px] text-foreground/90" suppressHydrationWarning>
                  {casualTime(t)}
                </li>
              ))}
            </ul>
          )}
        </div>
      </ModalSection>

      {/* Last run — the narrative, status, when, how long. */}
      <ModalSection
        label="Last run"
        tone={runStatus === "error" ? "error" : "default"}
        meta={
          latest?.started_at ? (
            <span suppressHydrationWarning>{casualTime(latest.started_at)}</span>
          ) : undefined
        }
      >
        {latest ? (
          <>
            <div className="flex flex-wrap items-center gap-2">
              <Badge
                variant={
                  runStatus === "ok"
                    ? "success"
                    : runStatus === "error"
                      ? "danger"
                      : "secondary"
                }
              >
                {runStatus === "ok" ? "succeeded" : runStatus === "error" ? "failed" : runStatus}
              </Badge>
              {typeof latest.duration_ms === "number" && (
                <span className="text-[12px] text-muted-foreground">
                  took {formatDuration(latest.duration_ms)}
                </span>
              )}
              {cron.failure_count > 0 && (
                <span className="text-[12px] text-danger">
                  {cron.failure_count} consecutive failure{cron.failure_count === 1 ? "" : "s"}
                </span>
              )}
            </div>
            {runNarrative ? (
              <ModalPre className="mt-2">{runNarrative}</ModalPre>
            ) : latest.human_error?.title ? (
              <p className="mt-2 text-[13px] text-danger">{latest.human_error.title}</p>
            ) : (
              <p className="mt-2 text-[12px] text-muted-foreground">
                No narrative recorded for this run.
              </p>
            )}
          </>
        ) : (
          <p className="text-[12px] text-muted-foreground">Hasn&apos;t run yet.</p>
        )}
      </ModalSection>

      {/* Engine detail — the raw enum + knobs, on click, never in the title. */}
      <ModalSection label="Details">
        <ModalDl
          entries={[
            { k: "kind", v: cron.job_kind },
            { k: "schedule", v: cron.schedule },
            { k: "retries", v: String(cron.max_retries) },
            { k: "backoff", v: `${cron.backoff_seconds}s` },
            {
              k: "created",
              v: <span suppressHydrationWarning>{casualTime(cron.created_at)}</span>,
            },
            { k: "id", v: cron.id },
          ]}
        />
      </ModalSection>
    </ResponsiveModal>
  );
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const rem = s % 60;
  return rem ? `${m}m ${rem}s` : `${m}m`;
}
