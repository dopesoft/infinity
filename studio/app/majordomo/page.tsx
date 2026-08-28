"use client";

import * as React from "react";
import { Bot, FileText, Terminal } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { ListRow, GroupLabel, WorkRow, StatusDot } from "@/components/ui/list-row";
import { Inset } from "@/components/ui/inset";
import { MetricRow } from "@/components/ui/metric-row";
import { Section, TileCard } from "@/components/dashboard/Section";

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
    </div>
  );
}
