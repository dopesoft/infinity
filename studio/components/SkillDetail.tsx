"use client";

import { useEffect, useState } from "react";
import { Clock, Hash, MousePointerSquareDashed, Play, RefreshCw, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { RiskBadge } from "@/components/RiskBadge";
import { EmptyState } from "@/components/EmptyState";
import {
  fetchSkill,
  fetchSkillRuns,
  invokeSkill,
  type SkillDTO,
  type SkillRunDTO,
  type SkillSummaryDTO,
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
  const [running, setRunning] = useState(false);
  const [runResult, setRunResult] = useState<string | null>(null);
  const [argsText, setArgsText] = useState("{}");

  useEffect(() => {
    setSkill(null);
    setRuns([]);
    setRunResult(null);
    setArgsText("{}");
    if (!selected) return;
    const ctrl = new AbortController();
    fetchSkill(selected.name, ctrl.signal).then((s) => s && setSkill(s));
    fetchSkillRuns(selected.name, 25, ctrl.signal).then((r) => r && setRuns(r));
    return () => ctrl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selected?.name]);

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
            <Badge variant="outline" className="font-mono">v{selected.version}</Badge>
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
            <TabsTrigger value="history">History</TabsTrigger>
            <TabsTrigger value="code">Code</TabsTrigger>
          </TabsList>

          <TabsContent value="overview" className="space-y-3">
            {!skill ? (
              <p className="text-sm text-muted-foreground">Loading…</p>
            ) : (
              <>
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

          <TabsContent value="history" className="space-y-2">
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

          <TabsContent value="code" className="space-y-2">
            {!skill ? (
              <p className="text-sm text-muted-foreground">Loading…</p>
            ) : (
              <>
                <Section title="SKILL.md body">
                  <pre className="whitespace-pre-wrap break-words rounded-md border bg-muted/60 p-3 font-mono text-xs leading-relaxed scroll-touch">
                    {skill.body || "—"}
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
