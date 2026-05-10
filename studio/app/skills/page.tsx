"use client";

import { useEffect, useMemo, useState } from "react";
import { RefreshCw, Search } from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { SkillCard } from "@/components/SkillCard";
import { SkillDetail } from "@/components/SkillDetail";
import { cn } from "@/lib/utils";
import { fetchSkills, reloadSkills, type SkillSummaryDTO } from "@/lib/api";

const STATUS_FILTERS = ["all", "active", "candidate", "archived"] as const;
type StatusFilter = (typeof STATUS_FILTERS)[number];

const RISK_FILTERS = ["all", "low", "medium", "high", "critical"] as const;
type RiskFilter = (typeof RISK_FILTERS)[number];

export default function SkillsPage() {
  const [skills, setSkills] = useState<SkillSummaryDTO[]>([]);
  const [selected, setSelected] = useState<SkillSummaryDTO | null>(null);
  const [loading, setLoading] = useState(true);
  const [reloading, setReloading] = useState(false);
  const [showDetail, setShowDetail] = useState(false);
  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("active");
  const [riskFilter, setRiskFilter] = useState<RiskFilter>("all");

  async function load() {
    setLoading(true);
    const list = await fetchSkills();
    setSkills(list ?? []);
    setLoading(false);
  }

  async function onReload() {
    setReloading(true);
    await reloadSkills();
    await load();
    setReloading(false);
  }

  useEffect(() => {
    load();
  }, []);

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

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="space-y-3 border-b px-3 py-3 sm:px-4">
          <div className="flex items-center gap-2">
            <div className="relative flex-1">
              <Search
                className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
                aria-hidden
              />
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Search skills (name, description, trigger)…"
                className="pl-9"
                inputMode="search"
              />
            </div>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              onClick={onReload}
              aria-label="Reload skills from filesystem"
              disabled={reloading || loading}
              title="Re-walk ./skills/ on Core"
            >
              <RefreshCw className={cn("size-4", reloading && "animate-spin")} />
            </Button>
          </div>

          <div className="flex flex-wrap items-center gap-1 text-xs">
            <div className="flex items-center gap-1">
              {STATUS_FILTERS.map((s) => (
                <button
                  key={s}
                  onClick={() => setStatusFilter(s)}
                  className={cn(
                    "rounded-md border px-2 py-1 font-mono uppercase tracking-wide",
                    statusFilter === s
                      ? "border-info bg-info/10 text-info"
                      : "border-transparent bg-muted text-muted-foreground hover:bg-accent",
                  )}
                >
                  {s}
                </button>
              ))}
            </div>
            <div className="flex flex-wrap items-center gap-1">
              {RISK_FILTERS.map((r) => (
                <button
                  key={r}
                  onClick={() => setRiskFilter(r)}
                  className={cn(
                    "rounded-full border px-2 py-0.5 font-mono uppercase tracking-wide",
                    riskFilter === r
                      ? "border-info bg-info/10 text-info"
                      : "border-transparent bg-muted text-muted-foreground hover:bg-accent",
                  )}
                >
                  {r}
                </button>
              ))}
            </div>
          </div>
        </div>

        <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
          <aside
            className={cn(
              "min-h-0 flex-1 flex-col overflow-y-auto border-b bg-background scroll-touch lg:w-80 lg:border-b-0 lg:border-r",
              showDetail ? "hidden lg:flex" : "flex",
            )}
          >
            <div className="flex items-center justify-between gap-2 px-3 pb-1 pt-3 text-[11px] uppercase tracking-wide text-muted-foreground">
              <span>skills</span>
              <span>{filtered.length}</span>
            </div>
            <div className="flex flex-col gap-2 px-3 pb-4">
              {filtered.length === 0 ? (
                <p className="px-1 text-sm text-muted-foreground">
                  {loading
                    ? "Loading…"
                    : query || statusFilter !== "active" || riskFilter !== "all"
                      ? "No matches."
                      : "No skills installed. Drop a skill folder into ./skills/ on Core and reload."}
                </p>
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
            </div>
          </aside>

          <section
            className={cn(
              "min-h-0 flex-1 flex-col bg-background",
              showDetail ? "flex" : "hidden lg:flex",
            )}
          >
            {showDetail && (
              <button
                onClick={() => setShowDetail(false)}
                className="border-b px-4 py-2 text-left text-xs text-muted-foreground lg:hidden"
              >
                ← back to list
              </button>
            )}
            <SkillDetail selected={selected} onClose={() => setShowDetail(false)} />
          </section>
        </div>
      </div>
    </TabFrame>
  );
}
