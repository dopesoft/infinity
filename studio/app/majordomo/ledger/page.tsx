"use client";

import * as React from "react";

import { ActivityLedger } from "@/components/chat/ActivityLedger";
import { WorkingIndicator } from "@/components/WorkingIndicator";
import { PageHeader } from "@/components/ui/page-header";
import { Section } from "@/components/dashboard/Section";
import type { ChatMessage } from "@/hooks/useChat";
import type { WSToolEvent } from "@/lib/ws/client";

/* Activity-ledger stories (docs/studio/MAJORDOMO.md §6, §9 phase 2).
 *
 * The sibling of app/majordomo/page.tsx (primitive stories). Not linked from
 * any nav. It renders the ledger against fixed fixtures so the four states
 * that matter - a live turn with a folded run and a code write in flight, a
 * settled turn, an approval, a failure - can be checked at 375 / 768 / 1280
 * in both themes without Core being up.
 *
 * `base` is resolved in an effect, never in a useState initializer, because
 * a fixture built from Date.now() at render time is exactly the hydration
 * mismatch CLAUDE.md forbids. Until it lands the page renders nothing, which
 * is also what the real stream does before its first message. */

const DIFF_PREVIEW = `@@ -128,7 +128,7 @@ func (l *Loop) Run(ctx context.Context) error {
-\tif err != nil { return err }
+\tif err != nil { return fmt.Errorf("run: %w", err) }
 \treturn nil`;

const TRACE = `The turn is asking for two things at once: the migration has to apply, and
the serve path has to pick it up. Check the migration list first, then trace
the caller. If migrate.go already embeds it, the gap is in serve.go.`;

let n = 0;
function toolEvent(name: string, input?: Record<string, unknown>): WSToolEvent {
  return { id: `fixture-call-${++n}`, name, input };
}

type Opts = {
  input?: Record<string, unknown>;
  output?: string;
  isError?: boolean;
  running?: boolean;
  interrupted?: boolean;
  awaiting?: string;
  at: number;
  took?: number;
};

function step(name: string, o: Opts): ChatMessage {
  const call = toolEvent(name, o.input);
  if (o.awaiting) {
    call.awaiting_approval = true;
    call.contract_id = o.awaiting;
    call.preview = "rm -rf ./build";
  }
  const started = new Date(o.at).toISOString();
  call.started_at = started;
  const settled = !o.running && !o.interrupted && !o.awaiting;
  return {
    id: `fixture-${call.id}`,
    role: "tool",
    text: "",
    createdAt: o.at,
    toolCall: call,
    toolResult: settled
      ? {
          id: call.id,
          name,
          output: o.output ?? "",
          is_error: o.isError,
          started_at: started,
          ended_at: new Date(o.at + (o.took ?? 900)).toISOString(),
        }
      : undefined,
    pending: !settled && !o.interrupted,
    interrupted: o.interrupted,
    endedAt: o.interrupted ? o.at + (o.took ?? 900) : undefined,
  };
}

function thought(text: string, at: number, took: number): ChatMessage {
  return { id: `fixture-think-${at}`, role: "thinking", text, createdAt: at, endedAt: at + took };
}

function narration(text: string, at: number): ChatMessage {
  return {
    id: `fixture-say-${at}`,
    role: "assistant",
    text,
    createdAt: at,
    interim: true,
    endedAt: at + 400,
  };
}

export default function LedgerStoriesPage() {
  const [base, setBase] = React.useState(0);
  React.useEffect(() => setBase(Date.now()), []);
  if (!base) return null;

  const t = (secondsAgo: number) => base - secondsAgo * 1000;

  // A live turn: 8 steps, a folded "Ran 3 commands", and a code write in
  // flight that opens itself and streams its diff.
  const live: ChatMessage[] = [
    thought(TRACE, t(96), 11_000),
    narration("Let me look at what the migrator actually applies before I touch serve.", t(84)),
    step("fs_read", { at: t(80), input: { path: "core/cmd/infinity/migrate.go" }, output: "package main\n…" }),
    step("claude_code__grep", {
      at: t(76),
      input: { pattern: "schema_migrations", path: "core" },
      output: "core/cmd/infinity/migrate.go:41\ncore/db/migrations.go:12",
    }),
    step("bash_run", { at: t(70), input: { command: "go build ./..." }, output: "" }),
    step("bash_run", { at: t(62), input: { command: "go test ./internal/..." }, output: "ok  \tinternal/agent\t0.42s\nok  \tinternal/memory\t1.10s" }),
    step("bash_run", { at: t(50), input: { command: "go vet ./..." }, output: "" }),
    step("claude_code__edit", {
      at: t(9),
      running: true,
      input: { file_path: "core/cmd/infinity/migrate.go", new_string: DIFF_PREVIEW },
    }),
  ];

  const settled: ChatMessage[] = [
    narration("Checking the inbox now.", t(400)),
    step("composio__GMAIL_FETCH_EMAILS", { at: t(398), input: { query: "is:unread newer_than:1d" }, output: "12 messages" }),
    step("composio__GMAIL_FETCH_EMAILS", { at: t(392), input: { query: "label:follow-up" }, output: "3 messages" }),
    step("surface_item", { at: t(380), input: { title: "Rob is waiting on the quote", surface: "inbox" }, output: "surfaced" }),
    step("git_commit", { at: t(360), input: { message: "Fold the inbox triage into the surface contract" }, output: "[main a91f3c2] 4 files changed" }),
  ];

  const approval: ChatMessage[] = [
    narration("The build directory has to go before this will link.", t(240)),
    step("bash_run", { at: t(236), awaiting: "9f2c41ab-77d1-4e2a-9a55-0b1c2d3e4f50", input: { command: "rm -rf ./build" } }),
  ];

  const trouble: ChatMessage[] = [
    step("bash_run", {
      at: t(180),
      isError: true,
      input: { command: "go test ./internal/memory" },
      output: "--- FAIL: TestRRF (0.01s)\n    rrf_test.go:88: expected 5 results, got 0\nFAIL\texit status 1",
    }),
    step("claude_code__bash", {
      at: t(170),
      output: "BLOCKED: destructive command needs approval. Trust contract: 3b7d2c19-55aa-4f10-8c31-77e9a1b2c3d4",
      input: { command: "rm -rf node_modules" },
    }),
    step("fs_save", { at: t(160), interrupted: true, input: { path: "studio/lib/chat/activity.ts" } }),
  ];

  // Edge cases the ledger has to swallow silently: a thinking block that ended
  // with no trace, an interim bubble that never got text (both must vanish,
  // not render an empty row), and a sub-agent still working.
  const edges: ChatMessage[] = [
    thought("", t(60), 800),
    narration("", t(58)),
    step("delegate", { at: t(20), running: true, input: { task: "Audit every surface for a bordered box inside a bordered box" } }),
  ];

  return (
    <div // px-4 / sm:px-6 is not a taste choice: `Section tone="band"` bleeds with
    // -mx-4 / sm:-mx-6, so the page column has to carry the matching padding
    // or the band overhangs the viewport by 4px at 375.
    className="mx-auto flex w-full min-w-0 max-w-3xl flex-col gap-8 px-4 py-6 sm:px-6">
      <PageHeader title="Activity ledger" meta="Fixture stories · phase 2" live />

      <Section title="Live turn" badge={8}>
        <ActivityLedger items={live} />
      </Section>

      <Section title="Settled turn" tone="band">
        <ActivityLedger items={settled} />
      </Section>

      <Section title="Waiting on you">
        <ActivityLedger items={approval} />
      </Section>

      <Section title="Failure, gate, and a stop" tone="band">
        <ActivityLedger items={trouble} />
      </Section>

      <Section title="Edge cases" badge={1}>
        <ActivityLedger items={edges} />
      </Section>

      <Section title="No steps yet" tone="band">
        <WorkingIndicator label="Working" />
      </Section>
    </div>
  );
}
