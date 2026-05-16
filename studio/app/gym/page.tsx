"use client";

import { useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import {
  AlertTriangle,
  Dumbbell,
  FlaskConical,
  History,
  RefreshCw,
  Route,
  Search,
  ShieldCheck,
} from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { MetricCard } from "@/components/MetricCard";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import {
  FilterPill,
  HeaderAction,
  HScrollRow,
  PageSectionHeader,
  PageTabs,
  PageTabsList,
  PageTabsTrigger,
} from "@/components/ui/page-tabs";
import { useRealtime } from "@/lib/realtime/provider";
import {
  extractGymExamples,
  fetchAuditLog,
  fetchGym,
  type AuditRowDTO,
  type GymSnapshotDTO,
} from "@/lib/api";
import { cn } from "@/lib/utils";

const TABS = ["overview", "datasets", "adapters", "routes", "audit"] as const;
type Tab = (typeof TABS)[number];
const OPS = ["all", "create", "update", "delete", "supersede"] as const;

export default function GymPage() {
  const [tab, setTab] = useState<Tab>("overview");
  const [gym, setGym] = useState<GymSnapshotDTO | null>(null);
  const [auditRows, setAuditRows] = useState<AuditRowDTO[]>([]);
  const [op, setOp] = useState<(typeof OPS)[number]>("all");
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(true);
  const [extracting, setExtracting] = useState(false);
  const [extractMsg, setExtractMsg] = useState("");
  const [expandedAudit, setExpandedAudit] = useState<string | null>(null);

  useEffect(() => {
    const requested = new URLSearchParams(window.location.search).get("tab") as Tab | null;
    if (requested && TABS.includes(requested)) setTab(requested);
  }, []);

  async function load() {
    setLoading(true);
    const [g, audit] = await Promise.all([
      fetchGym(50),
      fetchAuditLog(200, op === "all" ? "" : op),
    ]);
    setGym(g);
    setAuditRows(audit ?? []);
    setLoading(false);
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [op]);

  useRealtime([
    "mem_training_examples",
    "mem_distillation_datasets",
    "mem_distillation_runs",
    "mem_model_adapters",
    "mem_adapter_evals",
    "mem_policy_routes",
    "mem_audit",
  ], load);

  const auditFiltered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return auditRows;
    return auditRows.filter((r) =>
      `${r.operation} ${r.actor} ${r.target} ${JSON.stringify(r.diff ?? {})}`.toLowerCase().includes(q),
    );
  }, [auditRows, query]);

  const summary = gym?.summary;
  const ready = summary?.ready ?? false;

  async function runExtract() {
    setExtracting(true);
    setExtractMsg("");
    const result = await extractGymExamples(100);
    if (result) {
      setExtractMsg(
        `Inserted ${result.inserted} example${result.inserted === 1 ? "" : "s"} from ${result.evals} evals, ${result.lessons} reflections, ${result.surprise} surprises.`,
      );
    } else {
      setExtractMsg("Extraction failed.");
    }
    await load();
    setExtracting(false);
  }

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="space-y-3 border-b px-3 py-3 sm:px-4">
          <PageSectionHeader title="gym" count={tab === "audit" ? auditFiltered.length : undefined}>
            <HeaderAction
              icon={<RefreshCw className="size-4" />}
              label="Refresh"
              onClick={load}
              disabled={loading}
            />
            <HeaderAction
              icon={<Dumbbell className="size-4" />}
              label="Extract"
              onClick={runExtract}
              disabled={loading || extracting || !ready}
              primary
            />
          </PageSectionHeader>

          <div className="-mx-3 sm:mx-0">
            <div className="no-scrollbar flex snap-x snap-mandatory gap-2 overflow-x-auto scroll-touch px-3 pb-1 sm:grid sm:grid-cols-3 sm:gap-2 sm:overflow-visible sm:px-0 sm:pb-0 lg:grid-cols-6">
              <MetricCard label="examples" value={summary?.examples ?? 0} className="min-w-[9.5rem] shrink-0 snap-start sm:min-w-0" />
              <MetricCard label="datasets" value={summary?.datasets ?? 0} className="min-w-[9.5rem] shrink-0 snap-start sm:min-w-0" />
              <MetricCard label="runs" value={summary?.runs ?? 0} className="min-w-[9.5rem] shrink-0 snap-start sm:min-w-0" />
              <MetricCard label="candidates" value={summary?.candidates ?? 0} highlight={(summary?.candidates ?? 0) > 0} className="min-w-[9.5rem] shrink-0 snap-start sm:min-w-0" />
              <MetricCard label="reflex" value={summary?.reflex_on ? "on" : "off"} highlight={!summary?.reflex_on} className="min-w-[9.5rem] shrink-0 snap-start sm:min-w-0" />
              <MetricCard label="regressions" value={summary?.regressions ?? 0} highlight={(summary?.regressions ?? 0) > 0} className="min-w-[9.5rem] shrink-0 snap-start sm:min-w-0" />
            </div>
          </div>

          <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
            <PageTabs value={tab} onValueChange={(v) => setTab(v as Tab)}>
              <PageTabsList columns={5}>
                {TABS.map((t) => (
                  <PageTabsTrigger key={t} value={t}>
                    {tabLabel(t)}
                  </PageTabsTrigger>
                ))}
              </PageTabsList>
            </PageTabs>
            {tab === "audit" && (
              <div className="relative flex-1 lg:max-w-sm">
                <Search
                  className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
                  aria-hidden
                />
                <Input
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="Filter audit rows…"
                  className="pl-9"
                  inputMode="search"
                />
              </div>
            )}
          </div>

          {tab === "audit" && (
            <HScrollRow>
              {OPS.map((o) => (
                <FilterPill key={o} active={op === o} onClick={() => setOp(o)}>
                  {o}
                </FilterPill>
              ))}
            </HScrollRow>
          )}
        </div>

        <div className="flex-1 overflow-y-auto px-3 py-3 scroll-touch sm:px-4">
          {!ready && (
            <StatusBanner
              icon={<AlertTriangle className="size-4" />}
              title="Plasticity substrate pending"
              body="Gym will start filling once migration 022 is applied and Core can write plasticity ledgers."
            />
          )}
          {extractMsg && (
            <StatusBanner
              icon={<Dumbbell className="size-4" />}
              title="Extraction complete"
              body={extractMsg}
            />
          )}

          {tab === "overview" && <Overview gym={gym} loading={loading} />}
          {tab === "datasets" && <Datasets gym={gym} loading={loading} />}
          {tab === "adapters" && <Adapters gym={gym} loading={loading} />}
          {tab === "routes" && <Routes gym={gym} loading={loading} />}
          {tab === "audit" && (
            <AuditLedger
              rows={auditFiltered}
              loading={loading}
              expanded={expandedAudit}
              onToggle={setExpandedAudit}
            />
          )}
        </div>
      </div>
    </TabFrame>
  );
}

function Overview({ gym, loading }: { gym: GymSnapshotDTO | null; loading: boolean }) {
  const latest = gym?.runs?.[0];
  return (
    <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
      <Panel title="loop" icon={<Dumbbell className="size-4" />}>
        <TimelineRow label="training examples" value={String(gym?.summary.examples ?? 0)} />
        <TimelineRow label="dataset runs" value={String(gym?.summary.runs ?? 0)} />
        <TimelineRow label="candidate adapters" value={String(gym?.summary.candidates ?? 0)} />
        <TimelineRow label="reflex lessons" value={gym?.summary.reflex_on ? "in prompt path" : "off"} />
      </Panel>
      <Panel title="latest run" icon={<FlaskConical className="size-4" />}>
        {latest ? (
          <>
            <TimelineRow label="status" value={latest.status} />
            <TimelineRow label="trigger" value={latest.trigger} />
            <TimelineRow label="model" value={latest.base_model || "—"} />
            <p className="mt-2 line-clamp-3 text-sm text-muted-foreground">{latest.reason || latest.error || "—"}</p>
          </>
        ) : (
          <EmptyLine loading={loading} text="No distillation runs yet." />
        )}
      </Panel>
      <Panel title="promotion gate" icon={<ShieldCheck className="size-4" />}>
        <TimelineRow label="regressions" value={String(gym?.summary.regressions ?? 0)} danger={(gym?.summary.regressions ?? 0) > 0} />
        <TimelineRow label="candidates" value={String(gym?.summary.candidates ?? 0)} />
        <TimelineRow label="active adapters" value={String(gym?.summary.active ?? 0)} />
        <p className="mt-2 text-sm text-muted-foreground">
          Extracted lessons can guide the prompt now. Adapter promotion still belongs in Trust before any policy route becomes active.
        </p>
      </Panel>
    </div>
  );
}

function Datasets({ gym, loading }: { gym: GymSnapshotDTO | null; loading: boolean }) {
  return (
    <div className="space-y-3">
      <Panel title="datasets" icon={<FlaskConical className="size-4" />}>
        <Rows
          loading={loading}
          empty="No datasets yet."
          rows={(gym?.datasets ?? []).map((d) => ({
            id: d.id,
            title: d.name,
            meta: `${d.status} · ${d.example_count} examples`,
            right: shortDate(d.updated_at),
            body: d.artifact_uri || d.checksum || JSON.stringify(d.filters ?? {}),
          }))}
        />
      </Panel>
      <Panel title="examples" icon={<History className="size-4" />}>
        <Rows
          loading={loading}
          empty="No training examples yet."
          rows={(gym?.examples ?? []).map((e) => ({
            id: e.id,
            title: e.task_kind || e.source_kind,
            meta: `${e.label} · ${e.privacy_class} · score ${e.score.toFixed(2)}`,
            right: shortDate(e.created_at),
            body: e.source_id ? `${e.source_kind}#${e.source_id}` : e.source_kind,
          }))}
        />
      </Panel>
    </div>
  );
}

function Adapters({ gym, loading }: { gym: GymSnapshotDTO | null; loading: boolean }) {
  return (
    <div className="space-y-3">
      <Panel title="adapters" icon={<Dumbbell className="size-4" />}>
        <Rows
          loading={loading}
          empty="No adapters yet."
          rows={(gym?.adapters ?? []).map((a) => ({
            id: a.id,
            title: a.name,
            meta: `${a.status} · ${a.base_model || "base model unknown"}`,
            right: shortDate(a.created_at),
            body: a.task_scope.length ? a.task_scope.join(", ") : JSON.stringify(a.metrics ?? {}),
            tone: a.status === "active" ? "success" : a.status === "candidate" ? "warning" : "muted",
          }))}
        />
      </Panel>
      <Panel title="evals" icon={<ShieldCheck className="size-4" />}>
        <Rows
          loading={loading}
          empty="No adapter evals yet."
          rows={(gym?.evals ?? []).map((e) => ({
            id: e.id,
            title: e.eval_name,
            meta: `${e.passed ? "passed" : "blocked"} · regressions ${e.regression_count}`,
            right: `${e.baseline_score.toFixed(2)} → ${e.candidate_score.toFixed(2)}`,
            body: e.adapter_id || JSON.stringify(e.metrics ?? {}),
            tone: e.passed ? "success" : "danger",
          }))}
        />
      </Panel>
    </div>
  );
}

function Routes({ gym, loading }: { gym: GymSnapshotDTO | null; loading: boolean }) {
  return (
    <Panel title="policy routes" icon={<Route className="size-4" />}>
      <Rows
        loading={loading}
        empty="No policy routes yet."
        rows={(gym?.routes ?? []).map((r) => ({
          id: r.id,
          title: r.route,
          meta: `${r.status} · ${r.task_kind || "unscoped"}`,
          right: `min ${r.min_score.toFixed(2)}`,
          body: r.active_adapter_id || JSON.stringify(r.metadata ?? {}),
          tone: r.status === "active" ? "success" : r.status === "shadow" ? "warning" : "muted",
        }))}
      />
    </Panel>
  );
}

function AuditLedger({
  rows,
  loading,
  expanded,
  onToggle,
}: {
  rows: AuditRowDTO[];
  loading: boolean;
  expanded: string | null;
  onToggle: (id: string | null) => void;
}) {
  if (rows.length === 0) return <EmptyLine loading={loading} text="No matching audit rows." />;
  return (
    <ul className="space-y-1">
      {rows.map((r) => (
        <li key={r.id} className="rounded-md border bg-card">
          <button
            type="button"
            className="flex w-full flex-col gap-1 px-3 py-2 text-left text-xs sm:flex-row sm:items-center sm:justify-between"
            onClick={() => onToggle(expanded === r.id ? null : r.id)}
          >
            <div className="flex min-w-0 items-center gap-2">
              <Badge variant="outline" className="font-mono uppercase">
                {r.operation}
              </Badge>
              <span className="font-mono text-muted-foreground">{r.actor}</span>
              <code className="truncate font-mono">{r.target}</code>
            </div>
            <time className="font-mono text-muted-foreground" suppressHydrationWarning>
              {new Date(r.created_at).toLocaleString()}
            </time>
          </button>
          {expanded === r.id && r.diff && (
            <pre className="max-h-80 overflow-auto border-t bg-muted/40 p-2 font-mono text-[11px] leading-relaxed scroll-touch">
              {JSON.stringify(r.diff, null, 2)}
            </pre>
          )}
        </li>
      ))}
    </ul>
  );
}

function Panel({ title, icon, children }: { title: string; icon: ReactNode; children: ReactNode }) {
  return (
    <section className="rounded-lg border bg-card">
      <div className="flex items-center gap-2 border-b px-3 py-2 text-xs font-medium uppercase text-muted-foreground">
        {icon}
        <span>{title}</span>
      </div>
      <div className="p-3">{children}</div>
    </section>
  );
}

function Rows({
  rows,
  empty,
  loading,
}: {
  rows: { id: string; title: string; meta: string; right: string; body?: string; tone?: "success" | "warning" | "danger" | "muted" }[];
  empty: string;
  loading: boolean;
}) {
  if (rows.length === 0) return <EmptyLine loading={loading} text={empty} />;
  return (
    <div className="space-y-2">
      {rows.map((r) => (
        <article key={r.id} className="rounded-md border bg-background px-3 py-2">
          <div className="flex items-start justify-between gap-2">
            <div className="min-w-0">
              <div className="truncate text-sm font-medium">{r.title}</div>
              <div className={cn("mt-0.5 text-xs text-muted-foreground", toneClass(r.tone))}>{r.meta}</div>
            </div>
            <div className="shrink-0 font-mono text-[11px] text-muted-foreground">{r.right}</div>
          </div>
          {r.body && <p className="mt-1 line-clamp-2 break-words text-xs text-muted-foreground">{r.body}</p>}
        </article>
      ))}
    </div>
  );
}

function StatusBanner({ icon, title, body }: { icon: ReactNode; title: string; body: string }) {
  return (
    <div className="mb-3 flex gap-2 rounded-lg border border-warning/40 bg-warning/10 px-3 py-2 text-sm">
      <div className="mt-0.5 text-warning">{icon}</div>
      <div>
        <div className="font-medium text-foreground">{title}</div>
        <div className="text-muted-foreground">{body}</div>
      </div>
    </div>
  );
}

function TimelineRow({ label, value, danger = false }: { label: string; value: string; danger?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-3 border-b py-2 last:border-b-0">
      <span className="text-sm text-muted-foreground">{label}</span>
      <span className={cn("font-mono text-sm", danger ? "text-danger" : "text-foreground")}>{value}</span>
    </div>
  );
}

function EmptyLine({ loading, text }: { loading: boolean; text: string }) {
  return <p className="text-sm text-muted-foreground">{loading ? "Loading…" : text}</p>;
}

function tabLabel(tab: Tab): string {
  if (tab === "overview") return "status";
  if (tab === "datasets") return "data";
  return tab;
}

function shortDate(value?: string) {
  if (!value) return "—";
  return new Date(value).toLocaleDateString();
}

function toneClass(tone?: "success" | "warning" | "danger" | "muted") {
  if (tone === "success") return "text-success";
  if (tone === "warning") return "text-warning";
  if (tone === "danger") return "text-danger";
  return "";
}
