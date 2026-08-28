"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { ChevronLeft, RefreshCw, SearchX, Wand2 } from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { Button } from "@/components/ui/button";
import { SearchInput } from "@/components/ui/search-input";
import { NativeSelect } from "@/components/ui/native-select";
import { PageHeader } from "@/components/ui/page-header";
import { GroupLabel } from "@/components/ui/list-row";
import { SkillCard } from "@/components/SkillCard";
import { SkillDetail } from "@/components/SkillDetail";
import { CandidateSkillsPanel } from "@/components/CandidateSkillsPanel";
import { EmptyState } from "@/components/EmptyState";
import { PageTabs, PageTabsList, PageTabsTrigger } from "@/components/ui/page-tabs";
import { cn } from "@/lib/utils";
import { fetchSkills, type SkillSummaryDTO } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";
import { useTabParam } from "@/lib/useTabParam";

const STATUS_FILTERS = ["all", "active", "candidate", "archived"] as const;
type StatusFilter = (typeof STATUS_FILTERS)[number];

const RISK_FILTERS = ["all", "low", "medium", "high", "critical"] as const;
type RiskFilter = (typeof RISK_FILTERS)[number];

const GROUP_LABEL: Record<StatusFilter, string> = {
  all: "All skills",
  active: "Active",
  candidate: "Candidate",
  archived: "Archived",
};

export default function SkillsPage() {
  const [skills, setSkills] = useState<SkillSummaryDTO[]>([]);
  const [selected, setSelected] = useState<SkillSummaryDTO | null>(null);
  const [loading, setLoading] = useState(true);
  const [showDetail, setShowDetail] = useState(false);
  const [query, setQuery] = useState("");
  // Reported up by the candidates group so the page header can say how many
  // are waiting on a decision without a second fetch.
  const [candidateCount, setCandidateCount] = useState(0);
  // Active status tab persists in ?status=<id> so a refresh keeps the view.
  const [statusFilter, setStatusFilter] = useTabParam<StatusFilter>(
    "status",
    "active",
    STATUS_FILTERS,
  );
  const [riskFilter, setRiskFilter] = useState<RiskFilter>("all");

  const load = useCallback(async () => {
    setLoading(true);
    const list = await fetchSkills();
    setSkills(list ?? []);
    setLoading(false);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // The active status filter is URL-backed (?status=<id>) via useTabParam, so
  // deep-links land on the right filter and a refresh keeps it — no separate
  // sync effect needed.

  useRealtime(["mem_skills", "mem_skill_runs"], load);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return skills.filter((s) => {
      if (statusFilter !== "all" && s.status !== statusFilter) return false;
      if (riskFilter !== "all" && s.risk_level !== riskFilter) return false;
      if (q) {
        const hay = `${s.name} ${s.description}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [skills, query, statusFilter, riskFilter]);

  // Per-status counts respect the active search + risk filter but ignore
  // the status filter itself - that way each tab chip previews how many
  // skills will appear if you switch to it under the current narrowing.
  const statusCounts = useMemo(() => {
    const q = query.trim().toLowerCase();
    const counts: Record<StatusFilter, number> = {
      all: 0,
      active: 0,
      candidate: 0,
      archived: 0,
    };
    for (const s of skills) {
      if (riskFilter !== "all" && s.risk_level !== riskFilter) continue;
      if (q) {
        const hay = `${s.name} ${s.description}`.toLowerCase();
        if (!hay.includes(q)) continue;
      }
      counts.all++;
      if (s.status === "active") counts.active++;
      else if (s.status === "candidate") counts.candidate++;
      else if (s.status === "archived") counts.archived++;
    }
    return counts;
  }, [skills, query, riskFilter]);

  // One quiet line under the title: counts only, never a description (§1.5).
  const meta = useMemo(() => {
    const activeCount = skills.filter((s) => s.status === "active").length;
    if (loading && skills.length === 0) return "Loading the library…";
    const bits = [`${activeCount} active`];
    if (skills.length !== activeCount) bits.push(`${skills.length} in the library`);
    if (candidateCount > 0) {
      bits.push(`${candidateCount} candidate${candidateCount === 1 ? "" : "s"} to review`);
    }
    return bits.join(" · ");
  }, [skills, candidateCount, loading]);

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="px-4 pt-4 sm:px-6 lg:px-8">
          <PageHeader
            title="Skills"
            meta={meta}
            actions={
              <Button
                variant="outline"
                size="sm"
                onClick={() => void load()}
                disabled={loading}
                title="Re-read the skill library from Core"
              >
                <RefreshCw className={cn("size-4", loading && "animate-spin")} aria-hidden />
                Reload
              </Button>
            }
          />
        </div>

        {/* Search + risk sit together, directly above the list they filter.
            ONE chip row below them carries status — the second unlabelled
            chip strip (risk) is gone; see the note on RISK_FILTERS. */}
        <div className="flex flex-col gap-2.5 px-4 pb-3 sm:px-6 lg:px-8">
          <div className="flex min-w-0 items-center gap-2">
            <div className="min-w-0 flex-1">
              <SearchInput
                value={query}
                onValueChange={setQuery}
                placeholder="Search your skill library…"
              />
            </div>
            <NativeSelect
              value={riskFilter}
              onValueChange={(v) => setRiskFilter(v as RiskFilter)}
              aria-label="Filter by risk level"
              className="w-[8.5rem] shrink-0"
            >
              {RISK_FILTERS.map((r) => (
                <option key={r} value={r}>
                  {r === "all" ? "Any risk" : `${r} risk`}
                </option>
              ))}
            </NativeSelect>
          </div>

          <PageTabs
            value={statusFilter}
            onValueChange={(v) => setStatusFilter(v as StatusFilter)}
            className="w-full"
          >
            <PageTabsList scrollable>
              {STATUS_FILTERS.map((s) => (
                <PageTabsTrigger key={s} value={s} className="gap-1.5">
                  <span>{s}</span>
                  <span
                    className={cn(
                      "inline-flex h-4 min-w-[18px] items-center justify-center rounded-full px-1 font-mono text-[10px] leading-none",
                      statusFilter === s
                        ? "bg-background/20 text-background"
                        : "bg-muted-foreground/15 text-muted-foreground",
                    )}
                    aria-label={`${statusCounts[s]} matching`}
                  >
                    {statusCounts[s]}
                  </span>
                </PageTabsTrigger>
              ))}
            </PageTabsList>
          </PageTabs>
        </div>

        <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
          {/* The list column. Voyager's candidates are the first GROUP in it,
              not a separate bordered rail: same data shape, one surface
              (CLAUDE.md → "consolidate similar surfaces"). */}
          <aside
            className={cn(
              "min-h-0 w-full min-w-0 flex-1 flex-col overflow-y-auto bg-background px-4 pb-6 scroll-touch sm:px-6",
              "lg:w-[24rem] lg:flex-none lg:shrink-0 lg:border-r lg:border-hairline lg:px-4",
              showDetail ? "hidden lg:flex" : "flex",
            )}
          >
            <CandidateSkillsPanel query={query} onCountChange={setCandidateCount} />

            <GroupLabel label={GROUP_LABEL[statusFilter]} count={filtered.length} />

            {filtered.length === 0 ? (
              loading ? (
                <p className="py-2 text-[13.5px] text-quiet">Loading…</p>
              ) : /* A genuinely empty library reads as "nothing installed",
                     never as "your filters are too narrow" — the filter copy
                     only makes sense when there IS something to filter. */
                skills.length > 0 &&
                (query || statusFilter !== "active" || riskFilter !== "all") ? (
                <EmptyState
                  icon={SearchX}
                  align="top"
                  className="pt-10"
                  title="No matching skills"
                  description={
                    query
                      ? "Nothing in your library matches that search under the current filters. Try widening the status or risk."
                      : "No skills match the current filters. Reset status or risk to see more."
                  }
                />
              ) : (
                <EmptyState
                  icon={Wand2}
                  align="top"
                  className="pt-10"
                  title="No skills installed"
                  description={
                    <>
                      Drop a skill folder into{" "}
                      <code className="rounded-[6px] bg-muted px-1 font-mono text-[11px]">
                        ./skills/
                      </code>{" "}
                      on Core and reload, or wait for Voyager to propose one from your sessions —
                      candidates arrive at the top of this list.
                    </>
                  }
                />
              )
            ) : (
              filtered.map((s) => (
                <SkillCard
                  key={s.name}
                  skill={s}
                  active={selected?.name === s.name}
                  onClick={() => {
                    setSelected(s);
                    setShowDetail(true);
                  }}
                />
              ))
            )}
          </aside>

          <section
            className={cn(
              "min-h-0 min-w-0 flex-1 flex-col bg-background",
              showDetail ? "flex" : "hidden lg:flex",
            )}
          >
            {showDetail && (
              <button
                type="button"
                onClick={() => setShowDetail(false)}
                className="inline-flex min-h-11 items-center gap-1 px-4 text-left text-[13.5px] font-medium text-quiet transition-colors hover:text-foreground lg:hidden"
              >
                <ChevronLeft className="size-4" aria-hidden />
                Back to list
              </button>
            )}
            <SkillDetail selected={selected} onClose={() => setShowDetail(false)} />
          </section>
        </div>
      </div>
    </TabFrame>
  );
}
