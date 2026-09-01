"use client";

import { useEffect, useMemo, useState } from "react";
import { Workflow as WorkflowIcon, Play, ArrowRight } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { PageSectionHeader } from "@/components/ui/page-tabs";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { useRealtime } from "@/lib/realtime/provider";
import {
  fetchWorkflows,
  runWorkflow,
  type WorkflowDTO,
  type WorkflowInputDef,
} from "@/lib/api";
import { Chip, ChipGroup } from "@/components/ui/chip";
import { readableName } from "@/components/cron/cronMeta";
import { cn } from "@/lib/utils";

const KIND_TINT: Record<string, string> = {
  tool: "bg-info/10 text-info",
  skill: "bg-success/10 text-success",
  agent: "bg-warning/10 text-warning",
  checkpoint: "bg-muted text-muted-foreground",
};

/**
 * WorkflowsSection — the Workflows tab. Lists the saved `mem_workflows`
 * DEFINITIONS (the real gap: runs surface on the Agent Work board, but the
 * reusable pipelines had no home). Run is NOT a blind button — a workflow with
 * declared inputs opens a parameter form first, so "which YouTube video?" is
 * answered before the run fires. Live via the mem_workflows realtime publication.
 */
export function WorkflowsSection() {
  const [items, setItems] = useState<WorkflowDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [runTarget, setRunTarget] = useState<WorkflowDTO | null>(null);

  async function load() {
    setLoading(true);
    const r = await fetchWorkflows();
    setItems(r ?? []);
    setLoading(false);
  }
  useEffect(() => {
    load();
  }, []);
  useRealtime("mem_workflows", load);

  return (
    <div className="flex flex-col gap-3">
      {/* No title: the tab above already says "Routines". */}
      <PageSectionHeader title="" count={items.length} />

      <ul className="flex flex-col gap-2">
        {items.length === 0 ? (
          loading ? (
            <p className="text-sm text-muted-foreground">Loading…</p>
          ) : (
            <WorkflowsEmptyState />
          )
        ) : (
          items.map((wf) => (
            <li key={wf.id} className="rounded-xl border bg-card px-3 py-2.5">
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <WorkflowIcon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
                    <span className="truncate text-sm font-medium text-foreground">{readableName(wf.name)}</span>
                    {!wf.enabled && <Badge variant="secondary">disabled</Badge>}
                  </div>
                  {wf.description && (
                    <p className="mt-1 line-clamp-2 break-words text-sm text-muted-foreground">
                      {wf.description}
                    </p>
                  )}
                </div>
                <Button
                  size="sm"
                  variant="outline"
                  className="shrink-0"
                  onClick={() => setRunTarget(wf)}
                  disabled={!wf.enabled}
                >
                  <Play className="mr-1 size-3.5" />
                  Run
                </Button>
              </div>

              {/* Step pipeline — kind badges in order. */}
              <div className="mt-2 flex flex-wrap items-center gap-1 text-[10px]">
                {(wf.steps ?? []).map((s, i) => (
                  <span key={i} className="flex items-center gap-1">
                    <span
                      className={cn(
                        "rounded px-1.5 py-0.5 font-medium",
                        KIND_TINT[s.kind] ?? "bg-muted text-muted-foreground",
                      )}
                      title={`${s.kind}: ${s.name}`}
                    >
                      {s.name || s.kind}
                    </span>
                    {i < (wf.steps ?? []).length - 1 && (
                      <ArrowRight className="size-2.5 text-muted-foreground/50" aria-hidden />
                    )}
                  </span>
                ))}
              </div>

              {wf.inputs && wf.inputs.length > 0 && (
                <p className="mt-1.5 text-[10px] text-muted-foreground">
                  Asks you for: {wf.inputs.map((inp) => inp.label || readableName(inp.key)).join(" · ")}
                </p>
              )}
            </li>
          ))
        )}
      </ul>

      <RunWorkflowModal
        workflow={runTarget}
        open={runTarget !== null}
        onOpenChange={(o) => !o && setRunTarget(null)}
      />
    </div>
  );
}

function WorkflowsEmptyState() {
  return (
    <div className="rounded-xl border bg-card p-4">
      <div className="flex items-center gap-2">
        <WorkflowIcon className="size-4 text-muted-foreground" aria-hidden />
        <h3 className="text-sm font-medium text-foreground">No routines yet</h3>
      </div>
      <p className="mt-2 text-sm text-muted-foreground">
        A routine runs the same steps in the same order every time, so it cannot wander off
        halfway through. Just ask him: “make a routine that downloads a YouTube video and cuts
        the best parts into shorts.” He saves it here and you run it with one tap.
      </p>
    </div>
  );
}

/* The param-collection Run form — built from the workflow's declared inputs.
 * A workflow with no required inputs runs directly; otherwise the boss fills
 * the form, then we POST /api/workflows/run. */
function RunWorkflowModal({
  workflow,
  open,
  onOpenChange,
}: {
  workflow: WorkflowDTO | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const inputs = useMemo(() => workflow?.inputs ?? [], [workflow]);
  const [values, setValues] = useState<Record<string, string>>({});
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; msg: string } | null>(null);

  // Seed defaults whenever the target workflow changes.
  useEffect(() => {
    const seed: Record<string, string> = {};
    for (const inp of inputs) seed[inp.key] = inp.default ?? (inp.options?.[0] ?? "");
    setValues(seed);
    setResult(null);
  }, [inputs]);

  const missing = inputs.some(
    (inp) => inp.required && !String(values[inp.key] ?? "").trim(),
  );

  async function submit() {
    if (!workflow) return;
    setRunning(true);
    setResult(null);
    const r = await runWorkflow(workflow.name, values);
    setRunning(false);
    if (r?.ok) {
      setResult({ ok: true, msg: "Started — track it on the Agent Work board." });
    } else {
      setResult({ ok: false, msg: r?.error || "Failed to start the workflow." });
    }
  }

  return (
    <ResponsiveModal
      open={open}
      onOpenChange={onOpenChange}
      title={workflow ? `Run ${workflow.name}` : "Run workflow"}
      description={workflow?.description || "Fill in what this workflow needs, then run it."}
      size="md"
    >
      <div className="min-w-0 space-y-4">
        {inputs.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            This workflow needs no inputs — run it whenever you like.
          </p>
        ) : (
          <div className="space-y-3">
            {inputs.map((inp) => (
              <InputField
                key={inp.key}
                def={inp}
                value={values[inp.key] ?? ""}
                onChange={(v) => setValues((s) => ({ ...s, [inp.key]: v }))}
              />
            ))}
          </div>
        )}

        {result && (
          <p className={cn("text-sm", result.ok ? "text-success" : "text-danger")}>{result.msg}</p>
        )}

        <div className="flex items-center justify-end gap-2 pt-2">
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {result?.ok ? "Close" : "Cancel"}
          </Button>
          <Button onClick={submit} disabled={running || missing || result?.ok}>
            {running ? <Spinner className="mr-1 size-4" /> : <Play className="mr-1 size-4" />}
            {running ? "Starting…" : "Run"}
          </Button>
        </div>
      </div>
    </ResponsiveModal>
  );
}

function InputField({
  def,
  value,
  onChange,
}: {
  def: WorkflowInputDef;
  value: string;
  onChange: (v: string) => void;
}) {
  const label = def.label || def.key;
  return (
    <div className="min-w-0">
      <label className="mb-1 block text-xs font-medium text-foreground">
        {label}
        {def.required && <span className="ml-0.5 text-danger">*</span>}
      </label>
      {def.type === "enum" && def.options?.length ? (
        <ChipGroup wrap aria-label={label}>
          {def.options.map((opt) => (
            <Chip
              key={opt}
              raised={value === opt}
              aria-pressed={value === opt}
              onClick={() => onChange(opt)}
            >
              {opt}
            </Chip>
          ))}
        </ChipGroup>
      ) : (
        <Input
          value={value}
          inputMode={def.type === "number" ? "numeric" : "text"}
          type={def.type === "number" ? "number" : "text"}
          placeholder={def.doc || label}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
      {def.doc && <p className="mt-1 text-[11px] text-muted-foreground">{def.doc}</p>}
    </div>
  );
}
