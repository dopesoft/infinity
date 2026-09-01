"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useAppRouter } from "@/lib/loading";
import { RefreshCw, Wand2 } from "lucide-react";
import { AppShell } from "@/components/AppShell";
import { Button } from "@/components/ui/button";
import { SearchInput } from "@/components/ui/search-input";
import { EmptyState } from "@/components/EmptyState";
import { SkillDetail } from "@/components/SkillDetail";
import {
  PickDetailHeader,
  PickList,
  PickListGroup,
  PickListItem,
  PickListItems,
} from "@/components/ui/pick-list";
import { Inset } from "@/components/ui/inset";
import { authedFetch, fetchSkills, type SkillSummaryDTO } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";
import { usePageLoading } from "@/lib/loading";

/**
 * Skills — pick on the left, read on the right.
 *
 * WHAT CHANGED, AND WHY
 *
 * This page used to be a status tab strip (all / active / candidate /
 * archived), a risk dropdown, a search box, a card grid, and a separate
 * side rail for candidates. Next door, /lab held four more tabs (open
 * issues, recently fixed, lessons, gym) over what are also skills: a
 * candidate skill, a broken skill and a learned lesson are all skills. Two
 * pages and seven tabs for one concept.
 *
 * A catalog of forty is something you MOVE THROUGH — browse, open one, read
 * it, act, open the next. A board would make you open and shut forty sheets
 * to do that. So: list down the side, the chosen one open beside it.
 *
 * The left rail is the one place a mono group label is correct, because
 * everything in it is the same kind of thing and the groups are its STATE.
 * The dot lives on the GROUP, so no row repeats it.
 *
 * What is deliberately NOT on a row here: no risk chip, no last-run, no
 * description. A name, and one number where it earns its place. Everything
 * else is in the detail, which is right there.
 *
 * MOBILE: <PickList> makes the rail the page and opens the detail as a
 * sheet. That switch lives in the primitive, not here.
 */

type LabProposal = {
  id: string;
  kind: "curiosity" | "code_proposal";
  title: string;
  context?: string;
  file_path?: string;
  source?: string;
  created_at: string;
};

type LabSnapshot = {
  proposals: LabProposal[];
  counts: { open_proposals: number };
};

/** One row in the rail, whatever it actually is underneath. */
type Entry =
  | { id: string; label: string; group: "waiting"; skill: SkillSummaryDTO }
  | { id: string; label: string; group: "broken"; proposal: LabProposal }
  | { id: string; label: string; group: "inuse"; skill: SkillSummaryDTO };

export default function SkillsPage() {
  const router = useAppRouter();
  const [skills, setSkills] = useState<SkillSummaryDTO[]>([]);
  const [proposals, setProposals] = useState<LabProposal[]>([]);
  const [loading, setLoading] = useState(true);
  usePageLoading(loading);
  const [query, setQuery] = useState("");
  const [pickedId, setPickedId] = useState<string | null>(null);
  const [sheetOpen, setSheetOpen] = useState(false);
  const [reloading, setReloading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    const [list, lab] = await Promise.all([
      fetchSkills(),
      authedFetch("/api/lab")
        .then((r) => (r.ok ? (r.json() as Promise<LabSnapshot>) : null))
        .catch(() => null),
    ]);
    setSkills(list ?? []);
    setProposals(lab?.proposals ?? []);
    setLoading(false);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  useRealtime(["mem_skills", "mem_skill_runs", "mem_curiosity_questions", "mem_code_proposals"], load);

  const entries = useMemo<Entry[]>(() => {
    const q = query.trim().toLowerCase();
    const match = (s: string) => !q || s.toLowerCase().includes(q);

    const waiting: Entry[] = skills
      .filter((s) => s.status === "candidate" && match(s.name + s.description))
      .map((s) => ({ id: `skill:${s.name}`, label: humanName(s.name), group: "waiting", skill: s }));

    // A proposal about a cron that broke is a skill that is not working; the
    // rest are things he is asking about.
    const broken: Entry[] = proposals
      .filter((p) => match(p.title))
      .map((p) => ({ id: `prop:${p.id}`, label: p.title, group: "broken", proposal: p }));

    const inuse: Entry[] = skills
      .filter((s) => s.status === "active" && match(s.name + s.description))
      .map((s) => ({ id: `skill:${s.name}`, label: humanName(s.name), group: "inuse", skill: s }));

    return [...waiting, ...broken, ...inuse];
  }, [skills, proposals, query]);

  const waiting = entries.filter((e) => e.group === "waiting");
  const broken = entries.filter((e) => e.group === "broken");
  const inuse = entries.filter((e) => e.group === "inuse");

  // Default to the first thing that wants a decision, then the first in use.
  const picked = entries.find((e) => e.id === pickedId) ?? entries[0] ?? null;

  const pick = (id: string) => {
    setPickedId(id);
    setSheetOpen(true);
  };

  return (
    <AppShell>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="flex min-w-0 shrink-0 items-center gap-3 border-b border-hairline px-4 py-3 sm:px-6">
          <h1 className="flex-1 font-voice text-[22px] font-medium tracking-tight">Skills</h1>
          <Button
            variant="ghost"
            size="sm"
            onClick={async () => {
              setReloading(true);
              await load();
              setReloading(false);
            }}
            aria-label="Re-read the library"
            className="h-9 gap-1.5"
          >
            <RefreshCw className={reloading ? "size-4 animate-spin" : "size-4"} aria-hidden />
            <span className="hidden sm:inline">Refresh</span>
          </Button>
          <Button
            size="sm"
            className="h-9 gap-1.5"
            onClick={() => router.push("/live?ask=" + encodeURIComponent("Teach yourself a new skill. Ask me what I need first."))}
          >
            <Wand2 className="size-4" aria-hidden />
            <span className="hidden sm:inline">Teach him something</span>
            <span className="sm:hidden">Teach</span>
          </Button>
        </div>

        <PickList
          open={sheetOpen}
          onOpenChange={setSheetOpen}
          title={picked?.label ?? "Skill"}
          list={
            <>
              <div className="shrink-0 border-b border-hairline p-2.5">
                <SearchInput
                  value={query}
                  onValueChange={setQuery}
                  placeholder={`Search ${entries.length || ""}`.trim()}
                  className="h-9"
                />
              </div>
              <PickListItems>
                {waiting.length > 0 && (
                  <PickListGroup label="Waiting on you" count={waiting.length} tone="warning" />
                )}
                {waiting.map((e) => (
                  <PickListItem key={e.id} label={e.label} selected={picked?.id === e.id} onSelect={() => pick(e.id)} />
                ))}

                {broken.length > 0 && (
                  <PickListGroup label="Not working" count={broken.length} tone="danger" />
                )}
                {broken.map((e) => (
                  <PickListItem key={e.id} label={e.label} selected={picked?.id === e.id} onSelect={() => pick(e.id)} />
                ))}

                {inuse.length > 0 && <PickListGroup label="In use" count={inuse.length} />}
                {inuse.map((e) => (
                  <PickListItem
                    key={e.id}
                    label={e.label}
                    meta={e.group === "inuse" ? runCount(e.skill) : undefined}
                    selected={picked?.id === e.id}
                    onSelect={() => pick(e.id)}
                  />
                ))}

                {entries.length === 0 && (
                  <p className="px-2 py-6 text-[12.5px] text-muted-foreground">
                    {loading ? "Loading…" : query ? "Nothing matches that." : "He has not learned anything yet."}
                  </p>
                )}
              </PickListItems>
            </>
          }
          detail={<Detail entry={picked} loading={loading} />}
        />
      </div>
    </AppShell>
  );
}

function Detail({ entry, loading }: { entry: Entry | null; loading: boolean }) {
  const router = useAppRouter();
  if (!entry) {
    return (
      <div className="p-6">
        <EmptyState
          icon={Wand2}
          title={loading ? "Loading…" : "Nothing to show"}
          description="Pick something on the left, or teach him a new skill and it will appear here."
        />
      </div>
    );
  }

  if (entry.group === "broken") {
    const p = entry.proposal;
    return (
      <div className="flex min-w-0 flex-col gap-3 p-4 sm:p-6">
        <PickDetailHeader
          title={p.title}
          description="I noticed this myself and drafted a fix. Nothing is applied until you say so."
        />
        {p.context ? <Inset variant="plain">{p.context}</Inset> : null}
        {p.file_path ? (
          <p className="font-mono text-[11px] text-quiet">{p.file_path}</p>
        ) : null}
        <div className="flex items-center gap-2 pt-2">
          <Button
            size="sm"
            className="h-9"
            onClick={() =>
              router.push("/live?ask=" + encodeURIComponent(`Fix this: ${p.title}`))
            }
          >
            Have him fix it
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="min-w-0">
      <SkillDetail selected={entry.skill} onClose={() => {}} />
    </div>
  );
}

/**
 * A skill's stored name is a slug ("inbox-triage"). A person reads
 * "Inbox triage". The slug survives in the detail, where it is the thing you
 * need when something breaks.
 */
function humanName(name: string): string {
  const s = name.replace(/[-_]+/g, " ").trim();
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/** One number, and only when it means something. */
function runCount(s: SkillSummaryDTO): string | undefined {
  if (!s.last_run_at) return undefined;
  const pct = Math.round((s.success_rate ?? 0) * 100);
  return Number.isFinite(pct) && pct > 0 ? `${pct}%` : undefined;
}
