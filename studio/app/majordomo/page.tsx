"use client";

import * as React from "react";
import { Bot, FileText, Terminal } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { ListRow, GroupLabel, WorkRow, StatusDot } from "@/components/ui/list-row";
import { Inset } from "@/components/ui/inset";
import { MetricRow } from "@/components/ui/metric-row";
import { Section, TileCard } from "@/components/dashboard/Section";
import { Board, BoardBand, BoardCard } from "@/components/ui/board";
import { CountLine } from "@/components/ui/count-line";
import { DayRibbon } from "@/components/ui/day-ribbon";
import { ScopedTabs } from "@/components/ui/scoped-tabs";
import { SearchPage } from "@/components/ui/search-page";
import { Timeline, TimelineDay, TimelineRow } from "@/components/ui/timeline";
import {
  PickDetailHeader,
  PickList,
  PickListGroup,
  PickListItem,
  PickListItems,
} from "@/components/ui/pick-list";

/* Majordomo primitive stories (docs/studio/MAJORDOMO.md §9, phase 1
 * "done when": primitives exist with stories in a scratch page).
 *
 * Not linked from any nav. It renders every §5 primitive in every variant
 * and tone with fixed sample data, so a change to a token, the type scale,
 * or a primitive can be eyeballed at 375 / 768 / 1280 in both themes without
 * needing Core up or a seeded database. Delete it after phase 5 review if it
 * has stopped earning its keep. */

const DIFF = `--- a/core/internal/agent/loop.go
+++ b/core/internal/agent/loop.go
@@ -128,7 +128,7 @@ func (l *Loop) Run(ctx context.Context) error {
-\tif err != nil { return err }
+\tif err != nil { return fmt.Errorf("run: %w", err) }
 \treturn nil`;

const TERMINAL_OUT = Array.from({ length: 20 }, (_, i) => `ok  internal/pkg${i}  0.0${i}s`).join(
  "\n",
);

/** A fixed instant so the ribbon story never depends on the wall clock. */
const RIBBON_NOW = new Date(2026, 7, 29, 9, 0, 0).getTime();
const h = (n: number) => RIBBON_NOW + n * 60 * 60 * 1000;

export default function MajordomoStoriesPage() {
  return (
    <div className="mx-auto flex w-full max-w-3xl min-w-0 flex-col gap-8 px-4 py-6 sm:px-6">
      <PageHeader
        title="Majordomo"
        meta="Primitive stories · phase 1"
        live
        actions={<span className="font-mono text-[11px] uppercase tracking-[0.08em] text-quiet">scratch</span>}
      />

      <Section title="Type registers" badge={3}>
        <div className="flex flex-col gap-2">
          <p className="font-voice text-[15.5px] leading-[1.55] text-foreground">
            Voice — Geist, 15.5px. Jarvis&apos;s words, page and section titles.
          </p>
          <p className="font-sans text-[13.5px] font-medium text-foreground">
            Chrome — Geist, 13.5px medium. Labels, nav, buttons, meta.
          </p>
          <p className="font-mono text-[12px] tabular-nums text-foreground">
            Data — Geist Mono, 12px. 0123456789 · commands, diffs, ids, schemas.
          </p>
        </div>
      </Section>

      <Section title="ListRow" badge={4} tone="band">
        <div className="flex min-w-0 flex-col">
          <GroupLabel label="Today" count={3} />
          <ListRow title="Read 5 files" meta="core/internal/agent · 2m ago" onClick={() => {}} />
          <ListRow
            title="I checked the inbox and there was nothing that needed you."
            voice
            tone="brand"
            live
            meta="running · 14s"
          />
          <ListRow
            leading={<Terminal className="size-4" aria-hidden />}
            title="go test ./..."
            meta="exit 1"
            tone="danger"
            trailing={<span className="font-mono text-[11px] text-danger">FAILED</span>}
          />
          <GroupLabel label="Earlier" />
          <ListRow title="Nothing to do" meta="no tap target" chevron={false} />
        </div>
      </Section>

      <Section title="WorkRow" badge={3}>
        <div className="flex min-w-0 flex-col">
          <WorkRow
            kind="Plan"
            title="Rebuild the Studio design language"
            status="running"
            tone="brand"
            live
            meta="step 3 of 9 · 12m"
            progress={0.33}
            onClick={() => {}}
          />
          <WorkRow
            kind="Cron"
            title="Nightly cognition"
            status="needs you"
            tone="warning"
            meta="waiting since 03:04"
            onClick={() => {}}
          />
          <WorkRow kind="Skill" title="Inbox triage" status="done" meta="ran clean · 4m 02s" />
        </div>
      </Section>

      <Section title="Inset" badge={6} tone="band">
        <div className="flex min-w-0 flex-col gap-3">
          <Inset variant="terminal" command="go test ./..." text={TERMINAL_OUT} />
          <Inset variant="diff" text={DIFF} />
          <Inset variant="quote" text="I would not deploy that on a Friday, sir." />
          <Inset
            variant="kv"
            items={[
              { label: "session", value: "9f2c1b7e-0f4a-4c88-9c1e-0a5b3d2e7f10" },
              { label: "bridge", value: "cloud" },
              { label: "elapsed", value: "2m 14s" },
            ]}
          />
          <Inset
            variant="schema"
            fields={[
              { name: "id", type: "uuid", required: true },
              { name: "kind", type: "text", note: "one of plan | cron | skill" },
              { name: "meta", type: "jsonb" },
            ]}
          />
          <Inset text={'{\n  "ok": true,\n  "rows": 12\n}'} />
        </div>
      </Section>

      <Section title="MetricRow" badge={4}>
        <div className="flex min-w-0 flex-col">
          <MetricRow label="Observations" value="12,481" />
          <MetricRow label="Memories" value="3,207" meta="of 12,481" />
          <MetricRow label="Stale" value="42" tone="warning" />
          <MetricRow label="Contradictions" value="3" tone="danger" />
        </div>
      </Section>

      <Section
        title="Section tone: card"
        tone="card"
        Icon={Bot}
        action={{ label: "See all", href: "/majordomo" }}
      >
        <p className="font-voice text-[15.5px] leading-[1.55] text-foreground">
          One object you act on as a unit. A proposal, a reflection awaiting a decision. Never a
          card inside a card.
        </p>
      </Section>

      <Section title="TileCard (legacy shim)" badge={2}>
        <div className="flex min-w-0 flex-col">
          <TileCard className="gap-3">
            <FileText className="size-4 shrink-0 text-quiet" aria-hidden />
            <span className="min-w-0 flex-1 truncate text-[13.5px] font-medium">
              Still compiles, now renders as a row
            </span>
            <StatusDot tone="info" />
          </TileCard>
          <TileCard className="gap-3" tone="warning">
            <FileText className="size-4 shrink-0 text-quiet" aria-hidden />
            <span className="min-w-0 flex-1 truncate text-[13.5px] font-medium">
              tone is accepted and ignored
            </span>
          </TileCard>
        </div>
      </Section>

      <ShapeStories />
    </div>
  );
}

/**
 * Stories for the six page shapes (phase 2). Fixed sample data, no Core, no
 * database — so a token change, a type-scale change, or a primitive change
 * can be eyeballed at 375 / 768 / 1280 in both themes before any real page
 * is moved onto it.
 */
function ShapeStories() {
  const [tab, setTab] = React.useState("facts");
  const [chip, setChip] = React.useState("all");
  const [q, setQ] = React.useState("");
  const [memQ, setMemQ] = React.useState("");
  const [picked, setPicked] = React.useState("chase");
  const [pickOpen, setPickOpen] = React.useState(false);

  // Standing in for live per-tab match counts. "slack" is the interesting
  // case: zero in the active tab, matches in two others.
  const matchCounts = q.trim().toLowerCase().startsWith("slack")
    ? { facts: 0, lessons: 3, wrong: 1 }
    : { facts: 4, lessons: 1, wrong: 0 };

  return (
    <>
      <Section title="Board" badge={3}>
        <Board>
          <BoardCard title="Needs you" count={3} href="/">
            <ListRow title="Nadia wants net 30" meta="draft ready" tone="warning" onClick={() => {}} />
            <ListRow title="Stripe dispute closes Thursday" meta="needs a date from you" tone="warning" onClick={() => {}} />
            <ListRow title="Delete 400 old notes?" meta="waiting since 03:04" tone="danger" onClick={() => {}} />
          </BoardCard>
          <BoardCard title="Email" count={12} href="/" seeAll={{ label: "See all 12", href: "/" }}>
            <ListRow title="Nadia Osei" meta="Re: revised terms" tone="warning" onClick={() => {}} />
            <ListRow title="Stripe" meta="Evidence due Thursday" onClick={() => {}} />
            <ListRow title="M. Chen, CPA" meta="Q2 figures" onClick={() => {}} />
            <ListRow title="GitHub" meta="1 build failed" tone="quiet" onClick={() => {}} />
            <ListRow title="Vercel" meta="Deploy succeeded" tone="quiet" onClick={() => {}} />
          </BoardCard>
          <BoardCard title="In progress" count={2} href="/">
            <ListRow title="Rebuilding the design language" meta="step 3 of 9" tone="brand" live onClick={() => {}} />
            <ListRow title="Watching the Stripe dispute" meta="next check 11:00" onClick={() => {}} />
          </BoardCard>
        </Board>
      </Section>

      <BoardBand>
        <Board columns={2}>
          <BoardCard title="On a band" count={2}>
            <ListRow title="Alternating ground, no inner chrome" meta="one level of tone" />
            <ListRow title="Never a card on a band" meta="Majordomo §1.2" />
          </BoardCard>
          <BoardCard title="Two-row card">
            <ListRow title="No fade here" meta="fewer rows than the cap" />
            <ListRow title="A fade would promise content that is not there" />
          </BoardCard>
        </Board>
      </BoardBand>

      <Section title="CountLine" badge={5}>
        <CountLine
          items={[
            { value: "12,481", label: "facts", selected: true, onSelect: () => {} },
            { value: 64, label: "lessons", onSelect: () => {} },
            { value: 9, label: "wrong guesses", onSelect: () => {} },
            { value: "148k", label: "seen", onSelect: () => {} },
            { value: 31, label: "going stale", tone: "warning", onSelect: () => {} },
          ]}
        />
      </Section>

      <Section title="ScopedTabs" badge={3} tone="band">
        <ScopedTabs
          tabs={[
            { id: "facts", label: "Facts", count: 12481 },
            { id: "lessons", label: "Lessons", count: 64 },
            { id: "wrong", label: "Wrong guesses", count: 9 },
          ]}
          activeTab={tab}
          onTabChange={setTab}
          query={q}
          onQueryChange={setQ}
          matchCounts={matchCounts}
          chips={[
            { id: "all", label: "All" },
            { id: "people", label: "People", count: 402 },
            { id: "projects", label: "Projects", count: 1108 },
            { id: "stale", label: "Going stale", count: 31, tone: "warning" },
          ]}
          activeChip={chip}
          onChipChange={setChip}
        >
          <p className="pt-1 text-[12px] text-quiet">
            Type “slack” to see the cross-tab empty state.
          </p>
        </ScopedTabs>
      </Section>

      <Section title="SearchPage" badge={2}>
        <SearchPage
          query={memQ}
          onQueryChange={setMemQ}
          counts={
            <CountLine
              items={[
                { value: "12,481", label: "facts", selected: true },
                { value: 64, label: "lessons" },
                { value: "148k", label: "seen" },
              ]}
            />
          }
        >
          <div className="flex min-w-0 flex-col pt-1">
            <GroupLabel label="Learned today" />
            <ListRow voice title="Nadia at Vendor X negotiates terms, not price" meta="from 3 emails and a call" trailing={<span className="font-mono text-[11px] text-quiet">2h</span>} onClick={() => {}} />
            <ListRow voice title="The nightly build fails when the Mac is asleep" meta="from 4 failed jobs" trailing={<span className="font-mono text-[11px] text-quiet">9h</span>} onClick={() => {}} />
          </div>
        </SearchPage>
      </Section>

      <Section title="PickList" badge={2} noPad>
        <div className="min-h-[300px] border-t border-hairline">
          <PickList
            open={pickOpen}
            onOpenChange={setPickOpen}
            title="Chase unpaid invoices"
            description="He wrote this on Tuesday and would like to keep it."
            list={
              <PickListItems>
                <PickListGroup label="Waiting on you" count={2} tone="warning" />
                <PickListItem label="Chase unpaid invoices" selected={picked === "chase"} onSelect={() => { setPicked("chase"); setPickOpen(true); }} />
                <PickListItem label="Prep you faster for a call" selected={picked === "prep"} onSelect={() => { setPicked("prep"); setPickOpen(true); }} />
                <PickListGroup label="Not working" count={1} tone="danger" />
                <PickListItem label="Read your Slack" selected={picked === "slack"} onSelect={() => { setPicked("slack"); setPickOpen(true); }} />
                <PickListGroup label="In use" count={38} />
                <PickListItem label="Read the inbox" meta={412} selected={picked === "inbox"} onSelect={() => { setPicked("inbox"); setPickOpen(true); }} />
                <PickListItem label="Fix my own code" meta={61} selected={picked === "fix"} onSelect={() => { setPicked("fix"); setPickOpen(true); }} />
              </PickListItems>
            }
            detail={
              <div className="flex min-w-0 flex-col gap-3 p-4 sm:p-5">
                <PickDetailHeader
                  title="Chase unpaid invoices"
                  description="I wrote this on Tuesday after you chased three by hand. I would like to keep it."
                />
                <Inset variant="kv" items={[
                  { label: "What it does", value: "Finds invoices past due, checks whether you already chased them, and drafts a follow up in your voice. It never sends." },
                  { label: "When it runs", value: "Every weekday morning, after I read the inbox." },
                ]} />
              </div>
            }
          />
        </div>
      </Section>

      <Section title="DayRibbon" badge={5} tone="band">
        <DayRibbon
          now={RIBBON_NOW}
          marks={[
            { id: "nadia", label: "Nadia watch", at: h(2), tone: "brand" },
            { id: "prep", label: "Prep the call", at: h(8) },
            { id: "summary", label: "Summary", at: h(14) },
            { id: "tidy", label: "Tidy memory", at: h(19), tone: "danger" },
            { id: "improve", label: "Improve my code", at: h(22) },
            { id: "next-week", label: "Weekly summary", at: h(80) },
          ]}
        />
      </Section>

      <Section title="Timeline" badge={5}>
        <Timeline>
          <TimelineDay label="Today" />
          <TimelineRow time="now" tone="brand" live title="Rebuilding the design language" meta="step 3 of 9" trailing="12m" />
          <TimelineRow time="06:00" title="Read four inboxes, cleared 47 and kept 3 for you" trailing="41s" onClick={() => {}} />
          <TimelineRow time="04:12" tone="danger" title="I could not read your Slack, the sign in expired" meta="Tried three times, then stopped" trailing="8s" onClick={() => {}} />
          <TimelineRow time="03:41" tone="warning" title="I noticed the same file broke twice this week, so I drafted a fix" trailing="2m" onClick={() => {}} />
          <TimelineDay label="Yesterday" />
          <TimelineRow time="21:14" title="Fixed the Gmail sign in and checked that it worked" meta="3 files changed" trailing="4m" onClick={() => {}} />
        </Timeline>
      </Section>
    </>
  );
}
