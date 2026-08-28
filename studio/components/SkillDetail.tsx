"use client";

import { useEffect, useState } from "react";
import { Check, History, MousePointerSquareDashed, RefreshCw, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { TabsContent } from "@/components/ui/tabs";
import { PageTabs, PageTabsList, PageTabsTrigger } from "@/components/ui/page-tabs";
import { GroupLabel, ListRow } from "@/components/ui/list-row";
import { Inset, type InsetField } from "@/components/ui/inset";
import { SectionTitle } from "@/components/dashboard/Section";
import { RiskBadge } from "@/components/RiskBadge";
import { EmptyState } from "@/components/EmptyState";
import { RunIndicator } from "@/lib/runs";
import { cn } from "@/lib/utils";
import {
  fetchSkill,
  fetchSkillRuns,
  fetchSkillTests,
  fetchSkillVersions,
  generateSkillTests,
  invokeSkill,
  promoteSkillVersion,
  type SkillDTO,
  type SkillIODef,
  type SkillRunDTO,
  type SkillSummaryDTO,
  type SkillTestDTO,
  type SkillVersionEntry,
} from "@/lib/api";

/**
 * SkillDetail — one skill, read and run (Majordomo §2, §8).
 *
 * Header is now the Majordomo shape: the plain-English title, ONE quiet meta
 * line (`inbox-triage · v7 · medium risk · 12 runs`), and the close control.
 * The five stacked badges that used to sit under the title are that meta line
 * now. Inside, the six-tab strip is the house chip row (`PageTabsList
 * scrollable`) instead of a third packed tab bar, and every code-ish block —
 * SKILL.md, trigger phrases, run output, a test's inputs — renders in `Inset`
 * rather than a bordered `<pre>`.
 *
 * "Run skill" now goes through `RunIndicator` on the `skill` run kind, which
 * `skills/runner.go` already books into `mem_runs`: the spinner is server
 * state, so it survives navigation, refresh, and a second device, instead of
 * the local `useState(running)` it replaces (CLAUDE.md → "Server-tracked
 * progress").
 */
export function SkillDetail({
  selected,
  onClose,
}: {
  selected: SkillSummaryDTO | null;
  onClose: () => void;
}) {
  const [skill, setSkill] = useState<SkillDTO | null>(null);
  const [runs, setRuns] = useState<SkillRunDTO[]>([]);
  const [tests, setTests] = useState<SkillTestDTO[]>([]);
  const [versions, setVersions] = useState<SkillVersionEntry[]>([]);
  const [promotingVersion, setPromotingVersion] = useState<string | null>(null);
  const [runResult, setRunResult] = useState<string | null>(null);
  const [argsText, setArgsText] = useState("{}");
  const [openRun, setOpenRun] = useState<string | null>(null);

  useEffect(() => {
    setSkill(null);
    setRuns([]);
    setTests([]);
    setVersions([]);
    setRunResult(null);
    setArgsText("{}");
    if (!selected) return;
    const ctrl = new AbortController();
    fetchSkill(selected.name, ctrl.signal).then((s) => s && setSkill(s));
    fetchSkillRuns(selected.name, 25, ctrl.signal).then((r) => r && setRuns(r));
    fetchSkillTests(selected.name, ctrl.signal).then((t) => t && setTests(t));
    fetchSkillVersions(selected.name, ctrl.signal).then((v) => v && setVersions(v));
    return () => ctrl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selected?.name]);

  async function refreshVersions() {
    if (!selected) return;
    const v = await fetchSkillVersions(selected.name);
    if (v) setVersions(v);
  }

  async function promoteVersion(version: string) {
    if (!selected) return;
    setPromotingVersion(version);
    try {
      const ok = await promoteSkillVersion(selected.name, version);
      if (ok) {
        // Re-pull both versions (for active flag) and the skill detail
        // (Overview shows the active version label + updated_at).
        const [v, s] = await Promise.all([
          fetchSkillVersions(selected.name),
          fetchSkill(selected.name),
        ]);
        if (v) setVersions(v);
        if (s) setSkill(s);
      }
    } finally {
      setPromotingVersion(null);
    }
  }

  if (!selected) {
    return (
      <EmptyState
        icon={MousePointerSquareDashed}
        title="Pick a skill to inspect"
        description="Select a skill from the list to see its prompt, recent runs, and the trigger arguments it accepts. You can also invoke it from here."
      />
    );
  }

  async function refreshRuns() {
    if (!selected) return;
    const r = await fetchSkillRuns(selected.name, 25);
    if (r) setRuns(r);
  }

  async function refreshTests() {
    if (!selected) return;
    const t = await fetchSkillTests(selected.name);
    if (t) setTests(t);
  }

  async function generateTests() {
    if (!selected) return;
    const t = await generateSkillTests(selected.name);
    if (t) setTests((prev) => [...t, ...prev]);
  }

  async function run() {
    if (!selected) return;
    let parsed: Record<string, unknown> = {};
    try {
      parsed = argsText.trim() ? JSON.parse(argsText) : {};
    } catch (e) {
      setRunResult(`Invalid JSON: ${String(e)}`);
      return;
    }
    setRunResult(null);
    const res = await invokeSkill(selected.name, parsed);
    if (!res) {
      setRunResult("network error");
      return;
    }
    if (res.error) {
      setRunResult(`ERROR: ${res.error}\n\n${res.result?.stdout ?? ""}`);
    } else {
      setRunResult(res.result?.stdout ?? "(no output)");
    }
    void refreshRuns();
  }

  // The one meta line: id, version, risk, source, network, run count.
  const metaBits = [
    selected.name,
    `v${selected.version || "-"}`,
    `${selected.risk_level} risk`,
    selected.source,
    selected.network_egress?.length
      ? `egress ${selected.network_egress.join(", ")}`
      : "no network",
    `${skill?.total_runs ?? 0} runs`,
  ];

  return (
    <div className="flex h-full min-h-0 min-w-0 flex-col">
      <header className="min-w-0 border-b border-hairline px-4 py-3">
        <SectionTitle
          title={selected.description?.trim() || selected.name}
          headerExtra={
            <>
              <RiskBadge level={selected.risk_level} />
              <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close detail">
                <X className="size-4" />
              </Button>
            </>
          }
        />
        <p className="mt-0.5 min-w-0 truncate text-[12px] text-quiet">{metaBits.join(" · ")}</p>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3 scroll-touch">
        <PageTabs defaultValue="overview" className="min-w-0 space-y-3">
          <PageTabsList scrollable>
            <PageTabsTrigger value="overview">Overview</PageTabsTrigger>
            <PageTabsTrigger value="run">Run</PageTabsTrigger>
            <PageTabsTrigger value="tests">Tests</PageTabsTrigger>
            <PageTabsTrigger value="runs">Runs</PageTabsTrigger>
            <PageTabsTrigger value="versions">Versions</PageTabsTrigger>
            <PageTabsTrigger value="code">Code</PageTabsTrigger>
          </PageTabsList>

          <TabsContent value="overview" className="min-w-0 space-y-1">
            {!skill ? (
              <p className="py-2 text-[13.5px] text-quiet">Loading…</p>
            ) : (
              <>
                {/* Metadata strip: created / last updated / total runs.
                    Reads from the detail DTO which the server flattens
                    out of mem_skills + COUNT(mem_skill_runs). */}
                <GroupLabel label="Timeline" />
                <Inset
                  variant="kv"
                  items={[
                    {
                      label: "created",
                      value: (
                        <span suppressHydrationWarning>
                          {skill.created_at
                            ? new Date(skill.created_at).toLocaleDateString()
                            : "—"}
                        </span>
                      ),
                    },
                    {
                      label: "updated",
                      value: (
                        <span suppressHydrationWarning>
                          {skill.updated_at
                            ? new Date(skill.updated_at).toLocaleDateString()
                            : "—"}
                        </span>
                      ),
                    },
                    { label: "total runs", value: String(skill.total_runs ?? 0) },
                  ]}
                />

                <GroupLabel label="Trigger phrases" count={skill.trigger_phrases?.length} />
                {skill.trigger_phrases?.length ? (
                  <Inset>
                    <ul className="flex min-w-0 flex-col gap-1 font-mono text-[12px]">
                      {skill.trigger_phrases.map((p) => (
                        <li key={p} className="min-w-0 [overflow-wrap:anywhere]">
                          &ldquo;{p}&rdquo;
                        </li>
                      ))}
                    </ul>
                  </Inset>
                ) : (
                  <p className="pb-1 text-[13.5px] text-quiet">None — Jarvis picks it by name.</p>
                )}

                <GroupLabel label="Inputs" count={skill.inputs?.length} />
                {skill.inputs?.length ? (
                  <Inset variant="schema" fields={ioFields(skill.inputs)} />
                ) : (
                  <p className="pb-1 text-[13.5px] text-quiet">Takes no arguments.</p>
                )}

                <GroupLabel label="Outputs" count={skill.outputs?.length} />
                {skill.outputs?.length ? (
                  <Inset variant="schema" fields={ioFields(skill.outputs)} />
                ) : (
                  <p className="pb-1 text-[13.5px] text-quiet">Returns plain text.</p>
                )}

                <GroupLabel label="Strategic importance" count={selected.importance ?? 50} />
                <p className="text-[13.5px] leading-relaxed text-muted-foreground">
                  {selected.importance_reason || "General reusable skill."}
                </p>
                <p className="pb-1 text-[12px] leading-relaxed text-quiet">
                  Risk is execution danger. Importance is how central this skill is to autonomous
                  behavior.
                </p>

                {/* Last run sample - shows the boss what the skill was
                    actually called with + what it produced the most
                    recent time it ran. */}
                {skill.last_run ? (
                  <>
                    <GroupLabel
                      label="Last run"
                      trailing={
                        <span
                          className={cn(
                            "font-mono text-[11px]",
                            skill.last_run.success ? "text-success" : "text-danger",
                          )}
                        >
                          {skill.last_run.trigger_source} ·{" "}
                          {skill.last_run.success ? "ok" : "fail"}
                        </span>
                      }
                    />
                    <p className="text-[12px] text-quiet" suppressHydrationWarning>
                      <time>{new Date(skill.last_run.started_at).toLocaleString()}</time>
                    </p>
                    <GroupLabel label="Called with" />
                    <Inset text={JSON.stringify(skill.last_run.input ?? {}, null, 2)} />
                    <GroupLabel label="It produced" />
                    <Inset text={skill.last_run.output || "(empty)"} />
                  </>
                ) : null}
              </>
            )}
          </TabsContent>

          <TabsContent value="run" className="min-w-0 space-y-2">
            <GroupLabel label="Arguments (JSON)" />
            <Textarea
              value={argsText}
              onChange={(e) => setArgsText(e.target.value)}
              rows={6}
              className="font-mono text-xs"
              spellCheck={false}
              aria-label="Skill arguments as JSON"
            />
            {/* Server-tracked: runner.go books a mem_runs row of kind
                'skill' keyed by the skill name, so this spinner is the
                server's state, not this component's. */}
            <RunIndicator
              kind="skill"
              targetId={selected.name}
              label="Run skill"
              title={`Run ${selected.name}`}
              onRun={run}
            />
            {runResult !== null && (
              <>
                <GroupLabel label="Output" />
                <Inset text={runResult} />
              </>
            )}
          </TabsContent>

          <TabsContent value="runs" className="min-w-0">
            <GroupLabel
              label="Runs"
              count={runs.length}
              trailing={
                <Button size="sm" variant="ghost" onClick={refreshRuns} aria-label="Refresh runs">
                  <RefreshCw className="size-4" />
                </Button>
              }
            />
            {runs.length === 0 ? (
              <p className="py-2 text-[13.5px] text-quiet">No runs yet.</p>
            ) : (
              runs.map((r) => (
                <ListRow
                  key={r.id}
                  tone={r.success ? "success" : "danger"}
                  title={r.trigger_source || "run"}
                  meta={
                    <span suppressHydrationWarning>
                      {new Date(r.started_at).toLocaleString()} · {r.duration_ms}ms ·{" "}
                      {r.success ? "ok" : "fail"}
                    </span>
                  }
                  onClick={r.output ? () => setOpenRun((c) => (c === r.id ? null : r.id)) : undefined}
                  chevron={false}
                >
                  {r.output && openRun === r.id ? <Inset text={r.output} /> : null}
                </ListRow>
              ))
            )}
          </TabsContent>

          <TabsContent value="versions" className="min-w-0">
            <GroupLabel
              label="Versions"
              count={versions.length}
              trailing={
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={refreshVersions}
                  aria-label="Refresh versions"
                >
                  <RefreshCw className="size-4" />
                </Button>
              }
            />
            {versions.length === 0 ? (
              <p className="py-2 text-[13.5px] text-quiet">No version history yet.</p>
            ) : (
              versions.map((v) => (
                <ListRow
                  key={v.version}
                  tone={v.active ? "brand" : "quiet"}
                  title={<span className="font-mono text-[12.5px]">{v.version}</span>}
                  meta={
                    <span suppressHydrationWarning>
                      {new Date(v.created_at).toLocaleString()}
                      {v.source ? ` · ${v.source}` : ""}
                    </span>
                  }
                  trailing={
                    v.active ? (
                      <span className="inline-flex items-center gap-1 font-mono text-[11px] uppercase tracking-[0.06em] text-brand">
                        <Check className="size-3" aria-hidden /> active
                      </span>
                    ) : (
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={promotingVersion !== null}
                        onClick={() => promoteVersion(v.version)}
                      >
                        {promotingVersion === v.version ? (
                          <>
                            <RefreshCw className="size-3 animate-spin" />
                            Promoting…
                          </>
                        ) : (
                          <>
                            <History className="size-3" />
                            Promote
                          </>
                        )}
                      </Button>
                    )
                  }
                />
              ))
            )}
          </TabsContent>

          <TabsContent value="tests" className="min-w-0">
            <GroupLabel
              label="Verifier tests"
              count={tests.length}
              trailing={
                <span className="flex items-center gap-1">
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={refreshTests}
                    aria-label="Refresh tests"
                  >
                    <RefreshCw className="size-4" />
                  </Button>
                  <Button size="sm" onClick={generateTests}>
                    Generate
                  </Button>
                </span>
              }
            />
            {tests.length === 0 ? (
              <p className="py-2 text-[13.5px] leading-relaxed text-quiet">
                No verifier tests yet. Generate a smoke test before relying on this skill.
              </p>
            ) : (
              tests.map((t) => (
                <ListRow
                  key={t.id}
                  tone={t.last_passed == null ? "quiet" : t.last_passed ? "success" : "danger"}
                  title={t.description}
                  meta={`${t.source} · ${
                    t.last_passed == null ? "not run" : t.last_passed ? "passed" : "failed"
                  }`}
                  chevron={false}
                >
                  <div className="min-w-0 space-y-2">
                    {t.expected ? (
                      <p className="text-[12.5px] leading-relaxed text-muted-foreground">
                        Expects: {t.expected}
                      </p>
                    ) : null}
                    <Inset text={JSON.stringify(t.inputs ?? {}, null, 2)} />
                  </div>
                </ListRow>
              ))
            )}
          </TabsContent>

          <TabsContent value="code" className="min-w-0 space-y-1">
            {!skill ? (
              <p className="py-2 text-[13.5px] text-quiet">Loading…</p>
            ) : (
              <>
                <GroupLabel label="SKILL.md body" />
                <Inset text={skill.body || "-"} />
                {skill.impl_path && (
                  <>
                    <GroupLabel label={`Implementation · ${skill.impl_language}`} />
                    <Inset text={skill.impl_path} />
                  </>
                )}
              </>
            )}
          </TabsContent>
        </PageTabs>
      </div>
    </div>
  );
}

/** Skill inputs/outputs share the schema Inset's field shape. */
function ioFields(io: SkillIODef[]): InsetField[] {
  return io.map((f) => ({
    name: f.name,
    type: f.type,
    required: f.required,
    note: f.doc,
  }));
}
