"use client";

import { useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import {
  AlertTriangle,
  ArrowRight,
  BookOpen,
  Check,
  ChevronRight,
  Clock,
  Loader2,
  RefreshCw,
  Sparkles,
  Wrench,
  X,
  type LucideIcon,
} from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  PageTabs,
  PageTabsList,
  PageTabsTrigger,
  PageSectionHeader,
  HeaderAction,
} from "@/components/ui/page-tabs";
import { EmptyState } from "@/components/EmptyState";
import { TabsContent } from "@/components/ui/tabs";
import { authedFetch } from "@/lib/api";
import { seedSession } from "@/lib/dashboard/seed";
import { useRealtime } from "@/lib/realtime/provider";
import { relTime } from "@/lib/dashboard/format";
import { cn } from "@/lib/utils";

/* /lab - the single surface for "what Jarvis noticed, learned, and
 * taught himself". Replaces /code-proposals and /gym. Three tabs:
 *
 *   Fix this     - actionable proposals (curiosity questions + code
 *                  proposals). Approve & fix seeds a Live session that
 *                  runs the self-improve-from-finding skill.
 *   Lessons      - the training_examples gym has extracted. Already
 *                  feeds every turn via plasticity.Provider, this tab
 *                  just makes that learning visible in plain English.
 *   Skills evolved - skills Voyager has auto-promoted (currently
 *                    empty; populates as the auto-promotion fires).
 */

type LabTab = "proposals" | "lessons" | "skills";

type LabProposal = {
  id: string;
  kind: "curiosity" | "code_proposal";
  title: string;
  context?: string;
  file_path?: string;
  diff?: string;
  risk?: string;
  source?: string;
  created_at: string;
};

type LabLesson = {
  id: string;
  task_kind: string;
  label: string;
  input: string;
  output: string;
  score: number;
  source_kind: string;
  created_at: string;
};

type LabSkill = {
  id: string;
  name: string;
  description: string;
  source: string;
  version: number;
  updated_at: string;
};

type LabSnapshot = {
  proposals: LabProposal[];
  lessons: LabLesson[];
  skills: LabSkill[];
  counts: { open_proposals: number; lessons: number; evolved_skills: number };
};

const SOURCE_LABEL: Record<string, { label: string; tone: "info" | "warning" | "danger" }> = {
  high_surprise: { label: "Prediction missed", tone: "info" },
  contradiction: { label: "Two memories disagree", tone: "warning" },
  low_confidence: { label: "Low confidence memory", tone: "info" },
  uncovered_mention: { label: "Gap in what I know", tone: "info" },
  cron_failure: { label: "Cron job broke", tone: "danger" },
  repeated_tool_error: { label: "Tool keeps failing", tone: "danger" },
};

export default function LabPage() {
  const router = useRouter();
  const [tab, setTab] = useState<LabTab>("proposals");
  const [data, setData] = useState<LabSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [acting, setActing] = useState<Record<string, "fixing" | "dismissing" | null>>({});

  useEffect(() => {
    const requested = new URLSearchParams(window.location.search).get("tab") as LabTab | null;
    if (requested && ["proposals", "lessons", "skills"].includes(requested)) {
      setTab(requested);
    }
  }, []);

  async function load() {
    setLoading(true);
    try {
      const res = await authedFetch("/api/lab");
      if (res.ok) {
        const snap = (await res.json()) as LabSnapshot;
        setData(snap);
      }
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
  }, []);

  useRealtime(
    ["mem_curiosity_questions", "mem_code_proposals", "mem_training_examples", "mem_skills"],
    load,
  );

  async function approveProposal(p: LabProposal) {
    setActing((s) => ({ ...s, [p.id]: "fixing" }));
    try {
      const seedKind = p.kind === "curiosity" ? "curiosity" : "code_proposal";
      const sessionId = await seedSession(seedKind, p.id, p);
      if (sessionId) {
        router.push(`/live?session=${encodeURIComponent(sessionId)}`);
        return;
      }
      router.push("/live");
    } finally {
      setActing((s) => ({ ...s, [p.id]: null }));
    }
  }

  async function dismissProposal(p: LabProposal) {
    setActing((s) => ({ ...s, [p.id]: "dismissing" }));
    try {
      if (p.kind === "curiosity") {
        await authedFetch(`/api/curiosity/questions/${p.id}/decide`, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ decision: "dismissed" }),
        });
      } else {
        await authedFetch(`/api/voyager/code-proposals/${p.id}/decide`, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ decision: "rejected" }),
        });
      }
      await load();
    } finally {
      setActing((s) => ({ ...s, [p.id]: null }));
    }
  }

  const counts = data?.counts ?? { open_proposals: 0, lessons: 0, evolved_skills: 0 };

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="border-b px-3 py-3 sm:px-4">
          <PageSectionHeader title="lab">
            <HeaderAction
              icon={<RefreshCw className="size-4" />}
              label="Refresh"
              onClick={() => void load()}
              disabled={loading}
            />
          </PageSectionHeader>
          <p className="mt-1 text-xs text-muted-foreground">
            Things Jarvis noticed and proposes to fix, lessons learned from past
            sessions, and skills he taught himself. Approve a fix to send the
            full context to Live so he can apply it.
          </p>
        </div>

        <PageTabs value={tab} onValueChange={(v) => setTab(v as LabTab)} className="flex min-h-0 flex-1 flex-col">
          <div className="border-b px-3 pt-2 sm:px-4">
            <PageTabsList scrollable>
              <PageTabsTrigger value="proposals" className="gap-1.5">
                <span>Fix this</span>
                <CountChip n={counts.open_proposals} highlight={counts.open_proposals > 0} />
              </PageTabsTrigger>
              <PageTabsTrigger value="lessons" className="gap-1.5">
                <span>Lessons</span>
                <CountChip n={counts.lessons} />
              </PageTabsTrigger>
              <PageTabsTrigger value="skills" className="gap-1.5">
                <span>Skills evolved</span>
                <CountChip n={counts.evolved_skills} />
              </PageTabsTrigger>
            </PageTabsList>
          </div>

          <TabsContent value="proposals" className="mt-0 min-h-0 flex-1 overflow-y-auto px-3 py-3 scroll-touch sm:px-4">
            <ProposalsPane
              loading={loading}
              proposals={data?.proposals ?? []}
              acting={acting}
              onApprove={approveProposal}
              onDismiss={dismissProposal}
            />
          </TabsContent>
          <TabsContent value="lessons" className="mt-0 min-h-0 flex-1 overflow-y-auto px-3 py-3 scroll-touch sm:px-4">
            <LessonsPane loading={loading} lessons={data?.lessons ?? []} />
          </TabsContent>
          <TabsContent value="skills" className="mt-0 min-h-0 flex-1 overflow-y-auto px-3 py-3 scroll-touch sm:px-4">
            <SkillsPane loading={loading} skills={data?.skills ?? []} />
          </TabsContent>
        </PageTabs>
      </div>
    </TabFrame>
  );
}

function CountChip({ n, highlight }: { n: number; highlight?: boolean }) {
  return (
    <span
      className={cn(
        "inline-flex h-4 min-w-[18px] items-center justify-center rounded-full px-1 font-mono text-[10px] leading-none",
        highlight
          ? "bg-warning text-warning-foreground"
          : "bg-muted-foreground/15 text-muted-foreground",
      )}
    >
      {n}
    </span>
  );
}

function ProposalsPane({
  loading,
  proposals,
  acting,
  onApprove,
  onDismiss,
}: {
  loading: boolean;
  proposals: LabProposal[];
  acting: Record<string, "fixing" | "dismissing" | null>;
  onApprove: (p: LabProposal) => void;
  onDismiss: (p: LabProposal) => void;
}) {
  if (loading && proposals.length === 0) {
    return (
      <div className="flex h-32 items-center justify-center text-sm text-muted-foreground">
        <Loader2 className="mr-2 size-4 animate-spin" /> Loading
      </div>
    );
  }
  if (proposals.length === 0) {
    return (
      <EmptyState
        icon={Check}
        title="Nothing needs you right now"
        description="When Jarvis notices something he can fix (a tool that keeps erroring, a cron job that broke, a prediction that missed, code that needs cleanup) he posts it here for one-tap approval. The page is quiet because nothing has surfaced."
      />
    );
  }
  return (
    <ul className="space-y-3">
      {proposals.map((p) => (
        <li
          key={p.id}
          className="rounded-xl border bg-card p-4"
        >
          <ProposalCard
            p={p}
            acting={acting[p.id] ?? null}
            onApprove={() => onApprove(p)}
            onDismiss={() => onDismiss(p)}
          />
        </li>
      ))}
    </ul>
  );
}

function ProposalCard({
  p,
  acting,
  onApprove,
  onDismiss,
}: {
  p: LabProposal;
  acting: "fixing" | "dismissing" | null;
  onApprove: () => void;
  onDismiss: () => void;
}) {
  const sourceMeta = p.source ? SOURCE_LABEL[p.source] : null;
  return (
    <>
      <div className="flex flex-wrap items-center gap-2 text-[11px]">
        {p.kind === "code_proposal" ? (
          <Badge variant="outline" className="font-mono uppercase">
            <Wrench className="mr-1 size-3" />
            code refactor
          </Badge>
        ) : (
          <Badge variant="outline" className="font-mono uppercase">
            <Sparkles className="mr-1 size-3" />
            jarvis noticed
          </Badge>
        )}
        {sourceMeta ? (
          <span
            className={cn(
              "rounded-full border px-2 py-0.5 font-mono text-[10px] uppercase tracking-wider",
              sourceMeta.tone === "danger" && "border-danger/40 bg-danger/5 text-danger",
              sourceMeta.tone === "warning" && "border-warning/40 bg-warning/5 text-warning",
              sourceMeta.tone === "info" && "border-info/40 bg-info/5 text-info",
            )}
          >
            {sourceMeta.label}
          </span>
        ) : null}
        {p.risk ? (
          <Badge
            variant="outline"
            className={cn(
              "font-mono uppercase",
              p.risk === "high" && "border-danger/40 text-danger",
              p.risk === "critical" && "border-destructive/50 text-destructive",
            )}
          >
            risk: {p.risk}
          </Badge>
        ) : null}
        <span
          className="ml-auto shrink-0 whitespace-nowrap font-mono text-[10px] text-muted-foreground"
          suppressHydrationWarning
        >
          {relTime(p.created_at)}
        </span>
      </div>

      <h3 className="mt-2 text-[15px] font-semibold leading-snug text-foreground">
        {p.title}
      </h3>

      {p.file_path ? (
        <p className="mt-1 break-all font-mono text-[11px] text-muted-foreground">
          {p.file_path}
        </p>
      ) : null}

      {p.context ? (
        <p className="mt-2 whitespace-pre-wrap break-words text-[13px] leading-relaxed text-foreground/85">
          {p.context}
        </p>
      ) : null}

      {p.diff ? (
        <pre className="mt-2 max-h-48 min-w-0 max-w-full overflow-y-auto overflow-x-hidden whitespace-pre-wrap break-all rounded-md bg-muted p-2 font-mono text-[11px] leading-snug scroll-touch">
          {p.diff}
        </pre>
      ) : null}

      <div className="mt-3 flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          onClick={onApprove}
          disabled={acting !== null}
          className="gap-1.5"
        >
          {acting === "fixing" ? (
            <Loader2 className="size-4 animate-spin" />
          ) : (
            <ArrowRight className="size-4" />
          )}
          Approve & fix
        </Button>
        <Button
          size="sm"
          variant="ghost"
          onClick={onDismiss}
          disabled={acting !== null}
          className="gap-1.5"
        >
          {acting === "dismissing" ? (
            <Loader2 className="size-4 animate-spin" />
          ) : (
            <X className="size-4" />
          )}
          Dismiss
        </Button>
      </div>
    </>
  );
}

function LessonsPane({
  loading,
  lessons,
}: {
  loading: boolean;
  lessons: LabLesson[];
}) {
  if (loading && lessons.length === 0) {
    return (
      <div className="flex h-32 items-center justify-center text-sm text-muted-foreground">
        <Loader2 className="mr-2 size-4 animate-spin" /> Loading
      </div>
    );
  }
  if (lessons.length === 0) {
    return (
      <EmptyState
        icon={BookOpen}
        title="No lessons captured yet"
        description={
          <>
            Jarvis pulls lessons from your past sessions when he sees a
            pattern worth remembering. Each lesson is a one-line rule
            (&quot;when boss asks for X, prefer Y&quot;) that gets
            injected into every future turn&apos;s system prompt, so it
            shapes his behavior automatically. Once you&apos;ve had a
            few real exchanges he&apos;ll start collecting them here.
          </>
        }
      />
    );
  }
  // Plain-English explainer up top so the boss knows these lessons are
  // already influencing behavior, not aspirational dead data.
  return (
    <div className="space-y-3">
      <div className="rounded-lg border border-info/30 bg-info/5 p-3 text-[12px] leading-relaxed text-foreground/85">
        <p className="font-semibold text-info">How this works:</p>
        <p className="mt-1">
          These {lessons.length} lessons get folded into every chat
          turn&apos;s system prompt automatically (the top {Math.min(5, lessons.length)}{" "}
          ranked by relevance + score). You don&apos;t have to do anything to
          activate them - they&apos;re already shaping how Jarvis responds.
          This tab exists so you can SEE what he&apos;s learned, edit a bad
          lesson, or trace surprising behavior back to its source.
        </p>
      </div>
      <ul className="space-y-2">
        {lessons.map((l) => (
          <li key={l.id} className="rounded-lg border bg-card p-3">
            <LessonCard l={l} />
          </li>
        ))}
      </ul>
    </div>
  );
}

function LessonCard({ l }: { l: LabLesson }) {
  return (
    <>
      <div className="flex flex-wrap items-center gap-2 text-[10px]">
        <Badge variant="outline" className="font-mono uppercase">
          {l.task_kind || "lesson"}
        </Badge>
        {l.label ? (
          <span className="font-mono uppercase text-muted-foreground">
            {l.label}
          </span>
        ) : null}
        <span className="font-mono text-muted-foreground">
          score {l.score.toFixed(2)}
        </span>
        <span
          className="ml-auto shrink-0 whitespace-nowrap font-mono text-[10px] text-muted-foreground"
          suppressHydrationWarning
        >
          {relTime(l.created_at)}
        </span>
      </div>
      {l.input ? (
        <div className="mt-2">
          <p className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
            What happened
          </p>
          <p className="mt-0.5 break-words text-[13px] leading-relaxed text-foreground/85">
            {l.input}
          </p>
        </div>
      ) : null}
      {l.output ? (
        <div className="mt-2">
          <p className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
            What I learned
          </p>
          <p className="mt-0.5 break-words text-[13px] leading-relaxed text-foreground">
            {l.output}
          </p>
        </div>
      ) : null}
    </>
  );
}

function SkillsPane({
  loading,
  skills,
}: {
  loading: boolean;
  skills: LabSkill[];
}) {
  if (loading && skills.length === 0) {
    return (
      <div className="flex h-32 items-center justify-center text-sm text-muted-foreground">
        <Loader2 className="mr-2 size-4 animate-spin" /> Loading
      </div>
    );
  }
  if (skills.length === 0) {
    return (
      <EmptyState
        icon={Wrench}
        title="No skills self-promoted yet"
        description={
          <>
            When Jarvis solves a problem the same way 2 or 3 times, his
            Voyager loop writes a skill (a named recipe like
            &quot;triage gmail follow-ups&quot; or &quot;deploy core to
            railway&quot;) and saves it. Future sessions can call that
            skill by name instead of re-deriving the steps. This tab
            shows skills he taught HIMSELF. Manually-authored skills
            live in /skills.
          </>
        }
      />
    );
  }
  return (
    <ul className="space-y-2">
      {skills.map((s) => (
        <li key={s.id} className="rounded-lg border bg-card p-3">
          <div className="flex flex-wrap items-center gap-2 text-[11px]">
            <code className="truncate font-mono text-foreground">{s.name}</code>
            <Badge variant="outline" className="font-mono uppercase">
              v{s.version}
            </Badge>
            <Badge variant="success" className="font-mono uppercase">
              {s.source}
            </Badge>
            <span
              className="ml-auto shrink-0 whitespace-nowrap font-mono text-[10px] text-muted-foreground"
              suppressHydrationWarning
            >
              {relTime(s.updated_at)}
            </span>
          </div>
          {s.description ? (
            <p className="mt-2 break-words text-[13px] leading-relaxed text-foreground/85">
              {s.description}
            </p>
          ) : null}
        </li>
      ))}
    </ul>
  );
}
