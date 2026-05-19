"use client";

import { useEffect, useState } from "react";
import { Check, Clock, Hash, History, MousePointerSquareDashed, Play, RefreshCw, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { RiskBadge } from "@/components/RiskBadge";
import { EmptyState } from "@/components/EmptyState";
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
  type SkillRunDTO,
  type SkillSummaryDTO,
  type SkillTestDTO,
  type SkillVersionEntry,
} from "@/lib/api";

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
  const [running, setRunning] = useState(false);
  const [runResult, setRunResult] = useState<string | null>(null);
  const [argsText, setArgsText] = useState("{}");

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
    setRunning(true);
    setRunResult(null);
    const res = await invokeSkill(selected.name, parsed);
    setRunning(false);
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

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-start justify-between gap-2 border-b px-4 py-3">
        <div className="min-w-0">
          <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
            <Hash className="size-3" aria-hidden />
            <code className="truncate font-mono">{selected.name}</code>
          </div>
          <h3 className="mt-1 truncate text-sm font-semibold">
            {selected.description || selected.name}
          </h3>
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            <RiskBadge level={selected.risk_level} />
            <Badge variant="outline" className="font-mono">
              importance {selected.importance ?? 50}
            </Badge>
            <Badge variant="outline" className="font-mono">{selected.version}</Badge>
            <Badge variant="secondary" className="font-mono">{selected.source}</Badge>
            {selected.network_egress?.length ? (
              <Badge variant="outline" className="font-mono">
                egress: {selected.network_egress.join(", ")}
              </Badge>
            ) : (
              <Badge variant="outline" className="font-mono">no network</Badge>
            )}
          </div>
        </div>
        <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close detail">
          <X className="size-4" />
        </Button>
      </header>

      <div className="flex-1 overflow-y-auto px-4 py-3 scroll-touch">
        <Tabs defaultValue="overview" className="space-y-3">
          <TabsList>
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="run">Run</TabsTrigger>
            <TabsTrigger value="tests">Tests</TabsTrigger>
            <TabsTrigger value="runs">Runs</TabsTrigger>
            <TabsTrigger value="versions">Versions</TabsTrigger>
            <TabsTrigger value="code">Code</TabsTrigger>
          </TabsList>

          <TabsContent value="overview" className="space-y-3">
            {!skill ? (
              <p className="text-sm text-muted-foreground">Loading…</p>
            ) : (
              <>
                {/* Metadata strip: created / last updated / total runs.
                    Reads from the detail DTO which the server flattens
                    out of mem_skills + COUNT(mem_skill_runs). */}
                <Section title="Timeline">
                  <dl className="grid grid-cols-3 gap-2 text-xs">
                    <div>
                      <dt className="text-muted-foreground">Created</dt>
                      <dd className="font-mono" suppressHydrationWarning>
                        {skill.created_at
                          ? new Date(skill.created_at).toLocaleDateString()
                          : "—"}
                      </dd>
                    </div>
                    <div>
                      <dt className="text-muted-foreground">Last updated</dt>
                      <dd className="font-mono" suppressHydrationWarning>
                        {skill.updated_at
                          ? new Date(skill.updated_at).toLocaleDateString()
                          : "—"}
                      </dd>
                    </div>
                    <div>
                      <dt className="text-muted-foreground">Total runs</dt>
                      <dd className="font-mono">{skill.total_runs ?? 0}</dd>
                    </div>
                  </dl>
                </Section>
                <Section title="Trigger phrases">
                  {skill.trigger_phrases?.length ? (
                    <ul className="ml-4 list-disc text-sm">
                      {skill.trigger_phrases.map((p) => (
                        <li key={p} className="font-mono">&ldquo;{p}&rdquo;</li>
                      ))}
                    </ul>
                  ) : (
                    <p className="text-sm text-muted-foreground">none</p>
                  )}
                </Section>
                <Section title="Inputs">
                  {skill.inputs?.length ? (
                    <ul className="space-y-1 text-sm">
                      {skill.inputs.map((io) => (
                        <li key={io.name} className="flex items-center gap-2">
                          <code className="font-mono">{io.name}</code>
                          <span className="text-muted-foreground">
                            {io.type}
                            {io.required ? " (required)" : ""}
                          </span>
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p className="text-sm text-muted-foreground">none</p>
                  )}
                </Section>
                <Section title="Outputs">
                  {skill.outputs?.length ? (
                    <ul className="space-y-1 text-sm">
                      {skill.outputs.map((io) => (
                        <li key={io.name} className="flex items-center gap-2">
                          <code className="font-mono">{io.name}</code>
                          <span className="text-muted-foreground">{io.type}</span>
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p className="text-sm text-muted-foreground">none</p>
                  )}
                </Section>
                <Section title="Strategic importance">
                  <p className="text-sm text-muted-foreground">
                    {selected.importance_reason || "General reusable skill."}
                  </p>
                  <p className="mt-1 text-xs text-muted-foreground">
                    Risk is execution danger. Importance is how central this skill is to autonomous behavior.
                  </p>
                </Section>
                {/* Last run sample - shows the boss what the skill was
                    actually called with + what it produced the most
                    recent time it ran. Powers the "show me an example"
                    Overview expectation (#2 in the skill-review thread). */}
                {skill.last_run ? (
                  <Section title="Last run">
                    <div className="space-y-2 text-xs">
                      <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                        <span className="flex items-center gap-1">
                          <Clock className="size-3" aria-hidden />
                          <time suppressHydrationWarning>
                            {new Date(skill.last_run.started_at).toLocaleString()}
                          </time>
                        </span>
                        <span className="font-mono">
                          {skill.last_run.trigger_source} ·{" "}
                          <span className={skill.last_run.success ? "text-success" : "text-danger"}>
                            {skill.last_run.success ? "ok" : "fail"}
                          </span>
                        </span>
                      </div>
                      <div>
                        <p className="text-[11px] uppercase tracking-wider text-muted-foreground">
                          Input
                        </p>
                        <pre className="mt-1 max-h-32 overflow-y-auto whitespace-pre-wrap break-words rounded-md border bg-muted/40 p-2 font-mono text-[11px] leading-relaxed">
                          {JSON.stringify(skill.last_run.input ?? {}, null, 2)}
                        </pre>
                      </div>
                      <div>
                        <p className="text-[11px] uppercase tracking-wider text-muted-foreground">
                          Output
                        </p>
                        <pre className="mt-1 max-h-48 overflow-y-auto whitespace-pre-wrap break-words rounded-md border bg-muted/40 p-2 font-mono text-[11px] leading-relaxed">
                          {skill.last_run.output || "(empty)"}
                        </pre>
                      </div>
                    </div>
                  </Section>
                ) : null}
              </>
            )}
          </TabsContent>

          <TabsContent value="run" className="space-y-3">
            <Section title="Arguments (JSON)">
              <textarea
                value={argsText}
                onChange={(e) => setArgsText(e.target.value)}
                rows={6}
                className="w-full rounded-md border bg-muted/40 p-2 font-mono text-xs"
                spellCheck={false}
              />
            </Section>
            <Button onClick={run} disabled={running}>
              <Play className="mr-1 size-4" />
              {running ? "running…" : "Run skill"}
            </Button>
            {runResult !== null && (
              <Section title="Output">
                <pre className="whitespace-pre-wrap break-words rounded-md border bg-muted/60 p-3 font-mono text-xs leading-relaxed scroll-touch">
                  {runResult}
                </pre>
              </Section>
            )}
          </TabsContent>

          <TabsContent value="runs" className="space-y-2">
            <div className="flex items-center justify-between">
              <p className="text-xs text-muted-foreground">{runs.length} runs</p>
              <Button size="sm" variant="ghost" onClick={refreshRuns}>
                <RefreshCw className="size-4" />
              </Button>
            </div>
            {runs.length === 0 ? (
              <p className="text-sm text-muted-foreground">No runs yet.</p>
            ) : (
              <ul className="space-y-2">
                {runs.map((r) => (
                  <li key={r.id} className="rounded-md border bg-card p-2 text-xs">
                    <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                      <span className="flex items-center gap-1">
                        <Clock className="size-3" aria-hidden />
                        <time suppressHydrationWarning>
                          {new Date(r.started_at).toLocaleString()}
                        </time>
                      </span>
                      <span className="font-mono">
                        {r.trigger_source} · {r.duration_ms}ms ·{" "}
                        <span className={r.success ? "text-success" : "text-danger"}>
                          {r.success ? "ok" : "fail"}
                        </span>
                      </span>
                    </div>
                    {r.output && (
                      <pre className="mt-1 line-clamp-3 whitespace-pre-wrap break-words font-mono">
                        {r.output}
                      </pre>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </TabsContent>

          <TabsContent value="versions" className="space-y-2">
            <div className="flex items-center justify-between">
              <p className="text-xs text-muted-foreground">
                {versions.length} version{versions.length === 1 ? "" : "s"}
              </p>
              <Button size="sm" variant="ghost" onClick={refreshVersions}>
                <RefreshCw className="size-4" />
              </Button>
            </div>
            {versions.length === 0 ? (
              <p className="text-sm text-muted-foreground">No version history yet.</p>
            ) : (
              <ul className="space-y-2">
                {versions.map((v) => (
                  <li
                    key={v.version}
                    className={cn(
                      "rounded-md border bg-card p-2 text-xs",
                      v.active && "border-brand/60 bg-brand/5",
                    )}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <div className="flex items-center gap-2">
                        <code className="font-mono text-[12px] font-medium">
                          {v.version}
                        </code>
                        {v.active ? (
                          <Badge className="bg-brand text-brand-foreground">
                            <Check className="mr-1 size-3" /> Active
                          </Badge>
                        ) : null}
                        {v.source ? (
                          <Badge variant="outline" className="font-mono text-[10px]">
                            {v.source}
                          </Badge>
                        ) : null}
                      </div>
                      {!v.active ? (
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={promotingVersion !== null}
                          onClick={() => promoteVersion(v.version)}
                        >
                          {promotingVersion === v.version ? (
                            <>
                              <RefreshCw className="mr-1 size-3 animate-spin" />
                              Promoting…
                            </>
                          ) : (
                            <>
                              <History className="mr-1 size-3" />
                              Promote
                            </>
                          )}
                        </Button>
                      ) : null}
                    </div>
                    <p
                      className="mt-1 flex items-center gap-1 text-[11px] text-muted-foreground"
                      suppressHydrationWarning
                    >
                      <Clock className="size-3" aria-hidden />
                      <time>{new Date(v.created_at).toLocaleString()}</time>
                    </p>
                  </li>
                ))}
              </ul>
            )}
          </TabsContent>

          <TabsContent value="tests" className="space-y-2">
            <div className="flex items-center justify-between gap-2">
              <p className="text-xs text-muted-foreground">{tests.length} verifier tests</p>
              <div className="flex gap-1">
                <Button size="sm" variant="ghost" onClick={refreshTests}>
                  <RefreshCw className="size-4" />
                </Button>
                <Button size="sm" onClick={generateTests}>
                  Generate
                </Button>
              </div>
            </div>
            {tests.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No verifier tests yet. Generate a smoke test before relying on this skill.
              </p>
            ) : (
              <ul className="space-y-2">
                {tests.map((t) => (
                  <li key={t.id} className="rounded-md border bg-card p-2 text-xs">
                    <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                      <span className="font-mono">{t.source}</span>
                      <span className="font-mono">
                        {t.last_passed == null ? "not run" : t.last_passed ? "passed" : "failed"}
                      </span>
                    </div>
                    <p className="mt-1 font-medium">{t.description}</p>
                    <p className="mt-1 text-muted-foreground">{t.expected}</p>
                    <pre className="mt-2 whitespace-pre-wrap break-words rounded bg-muted/60 p-2 font-mono">
                      {JSON.stringify(t.inputs ?? {}, null, 2)}
                    </pre>
                  </li>
                ))}
              </ul>
            )}
          </TabsContent>

          <TabsContent value="code" className="space-y-2">
            {!skill ? (
              <p className="text-sm text-muted-foreground">Loading…</p>
            ) : (
              <>
                <Section title="SKILL.md body">
                  <pre className="whitespace-pre-wrap break-words rounded-md border bg-muted/60 p-3 font-mono text-xs leading-relaxed scroll-touch">
                    {skill.body || "-"}
                  </pre>
                </Section>
                {skill.impl_path && (
                  <Section title={`Implementation (${skill.impl_language})`}>
                    <code className="font-mono text-xs">{skill.impl_path}</code>
                  </Section>
                )}
              </>
            )}
          </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
        {title}
      </div>
      <div className="space-y-1">{children}</div>
    </section>
  );
}
