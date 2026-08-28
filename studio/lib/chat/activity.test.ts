/**
 * Tests for the Majordomo activity ledger vocabulary (MAJORDOMO §6).
 *
 * These encode WHY, per CLAUDE.md operating rule 9. Each block names the
 * regression it exists to catch; if the described behaviour goes away, the
 * assertion has to fail. In particular:
 *
 *   • the vocabulary must never regress to showing a raw tool id
 *   • coalescing must never merge across a failure
 *   • an approval must never be folded away into a neighbouring count
 *   • a stopped call must never read as done
 *
 * Fixtures are built from the REAL ChatMessage shape (studio/hooks/useChat.ts):
 * role "tool" + toolCall/toolResult (WSToolEvent), `pending`, `interrupted`,
 * `interim`, `createdAt`, `endedAt`.
 */

import { describe, expect, it } from "vitest";

import type { ChatMessage } from "@/hooks/useChat";
import type { WSToolEvent } from "@/lib/ws/client";
import {
  activityIsLive,
  coalesce,
  DECISION_TOOLS,
  deriveStatus,
  describeStep,
  detectGated,
  firstSentence,
  formatDuration,
  headlineFor,
  summaryFor,
} from "./activity";

// ─── fixtures ────────────────────────────────────────────────────────────────

let seq = 0;
const T0 = 1_700_000_000_000;

function call(name: string, input?: Record<string, unknown>): WSToolEvent {
  return { id: `call-${++seq}`, name, input };
}

type ToolOpts = {
  input?: Record<string, unknown>;
  output?: string;
  isError?: boolean;
  /** No result yet, turn still live. */
  running?: boolean;
  /** Gate parked it: awaiting_approval + contract_id, no result. */
  awaitingContract?: string;
  /** Turn ended before the result frame arrived. */
  interrupted?: boolean;
  at?: number;
  endedAt?: number;
};

/** One tool step, exactly as useChat builds it from the WS frames. */
function toolMessage(name: string, opts: ToolOpts = {}): ChatMessage {
  const c = call(name, opts.input);
  if (opts.awaitingContract) {
    c.awaiting_approval = true;
    c.contract_id = opts.awaitingContract;
  }
  const m: ChatMessage = {
    id: `msg-${seq}`,
    role: "tool",
    text: "",
    toolCall: c,
    createdAt: opts.at ?? T0 + seq * 1000,
  };
  const noResult = opts.running || opts.awaitingContract || opts.interrupted;
  if (!noResult) {
    m.toolResult = {
      id: c.id,
      name,
      output: opts.output ?? "ok",
      is_error: opts.isError || undefined,
      ended_at: new Date(opts.endedAt ?? m.createdAt + 500).toISOString(),
    };
  }
  if (opts.running || opts.awaitingContract) m.pending = true;
  if (opts.interrupted) {
    m.interrupted = true;
    m.endedAt = opts.endedAt ?? m.createdAt + 500;
  }
  return m;
}

function narration(text: string, at?: number): ChatMessage {
  seq++;
  return {
    id: `msg-${seq}`,
    role: "assistant",
    text,
    interim: true,
    createdAt: at ?? T0 + seq * 1000,
  };
}

function thinking(text: string, at?: number): ChatMessage {
  seq++;
  return { id: `msg-${seq}`, role: "thinking", text, createdAt: at ?? T0 + seq * 1000 };
}

// Every native tool id enumerated from the Go registry that the boss is most
// likely to see. Deliberately literal so a rename in Go surfaces here.
const REAL_TOOL_IDS = [
  "bash_run", "fs_read", "fs_ls", "fs_save", "fs_edit",
  "git_status", "git_diff", "git_stage", "git_commit", "git_push", "git_pull",
  "claude_code__Read", "claude_code__Write", "claude_code__Edit", "claude_code__Bash",
  "claude_code__Grep", "claude_code__Glob", "claude_code__LS", "claude_code__MultiEdit",
  "claude_code__NotebookEdit", "claude_code__TodoWrite", "claude_code__WebSearch",
  "claude_code__WebFetch", "claude_code__Agent", "claude_code__Skill",
  "github__create_or_update_file", "github__push_files", "github__delete_file",
  "github__create_pull_request", "github__merge_pull_request", "github__create_branch",
  "github__get_file_contents", "github__create_issue", "github__list_issues",
  "remember", "recall", "forget", "mem_list", "mem_act", "action_register", "action_list",
  "compact_context", "delegate", "delegate_parallel", "background_build",
  "agent_team_start", "code_agent",
  "plan_create", "plan_approve", "plan_update", "plan_verify", "plan_get", "plan_list",
  "plan_revise", "plan_cancel",
  "goal_set", "goal_update", "goal_list", "worldmodel_extract", "entity_upsert",
  "entity_link", "entity_get", "entity_search",
  "todo_write", "task_create", "task_update", "task_done", "task_list",
  "pursuit_create", "pursuit_checkin", "pursuit_list", "pursuit_pc_state", "pursuit_pc_write",
  "followup_snooze", "followup_dismiss", "followup_list", "read_email",
  "saved_add", "saved_list", "surface_item", "surface_update", "surface_list",
  "mandate_open", "mandate_check", "mandate_close", "mandate_abandon", "mandate_verify",
  "outcome_track", "outcome_resolve",
  "skills_list", "skills_invoke", "skills_discover", "skills_history", "skill_create",
  "skill_propose", "skill_optimize", "skill_proposal_get", "skill_tests", "skill_test_generate",
  "cron_create_agent", "cron_create_poll", "cron_list", "cron_delete", "cron_pause",
  "cron_run_now", "sentinel_create", "sentinel_list", "sentinel_delete", "sentinel_pause",
  "sentinel_test", "watch_until",
  "workflow_create", "workflow_run", "workflow_status", "workflow_resume", "workflow_cancel",
  "workflow_validate", "workflow_list",
  "web_search", "http_fetch", "code_exec", "media_job", "document_create",
  "artifact_save", "artifact_list", "artifact_get", "artifact_delete",
  "browser_open", "browser_navigate", "browser_observe", "browser_act", "browser_extract",
  "browser_close", "browser_request_takeover",
  "calendar_respond", "calendar_sync_now", "calendar_create", "calendar_patch",
  "calendar_delete", "contact_save", "contact_find", "phone_call",
  "notify", "notification_digest", "intervention_score", "cost_record", "budget_status",
  "connector_identity_set", "connector_accounts_list", "connector_coverage_mark",
  "extension_register", "extension_list", "extension_remove", "extension_check",
  "extension_activate",
  "system_map", "domain_hint_add", "domain_hint_list", "tool_search", "load_tools",
  "unload_tools", "state_set", "state_get", "state_list", "state_delete",
  "self_improve_control", "code_proposal_decide", "deploy_status", "deploy_status_refresh",
  "traces_recent", "trace_inspect", "traces_search", "eval_record", "eval_scorecard",
  "trust_batch_assign", "question_list", "question_decide",
  "preview_start", "preview_stop", "preview_status",
  "project_create", "project_clone", "project_open",
  "composio__GMAIL_FETCH_EMAILS", "composio__GMAIL_SEND_EMAIL",
  "composio__GMAIL_CREATE_EMAIL_DRAFT", "composio__GOOGLECALENDAR_CREATE_EVENT",
  "composio__GITHUB_CREATE_ISSUE",
];

// ─── the bug this module exists to fix ───────────────────────────────────────

describe("the vocabulary never shows a raw tool id", () => {
  // WHY: the old ToolCallCard fell through to `call.name`, so the boss read
  // rows saying `composio__GMAIL_FETCH_EMAILS` and `mem_act`. A row is
  // plain English or it is a bug. If someone adds a tool without a verb and
  // the humanizer regresses, this fails.
  it.each(REAL_TOOL_IDS)("%s reads as English, not an id", (name: string) => {
    const d = describeStep(call(name, { file_path: "/repo/core/cmd/infinity/migrate.go" }));
    expect(d.verb).not.toBe(name);
    expect(d.verbPast).not.toBe(name);
    expect(d.verb).not.toContain("__");
    expect(d.verb).not.toMatch(/_/);
    expect(d.verb.length).toBeGreaterThan(0);
    // The glyph is a Lucide icon NAME, so this module stays React-free.
    expect(typeof d.glyph).toBe("string");
    expect(d.glyph).toMatch(/^[A-Z][A-Za-z0-9]*$/);
  });

  it("humanizes an unknown MCP tool as 'Server · verb'", () => {
    // WHY §6: MCP tool names are discovered at CONNECT time, so the table can
    // never be complete. The fallback has to stay legible, not leak the id.
    expect(describeStep(call("github__search_repositories")).verb).toBe(
      "GitHub · search repositories",
    );
    expect(describeStep(call("tavily__extract")).verb).toBe("Tavily · extract");
    expect(describeStep(call("acme_corp__doTheThing")).verb).toBe("Acme Corp · do the thing");
  });

  it("humanizes a bare unknown native tool", () => {
    expect(describeStep(call("quantum_flux_capacitor")).verb).toBe("Quantum flux capacitor");
  });

  it("humanizes an unmapped Composio action through its toolkit", () => {
    // WHY: Composio mints one tool per action slug from a live catalog, so
    // most ids will never be in the table. They must still read as work.
    const d = describeStep(call("composio__SLACK_SEND_MESSAGE"));
    expect(d.verb).toBe("Sending message");
    expect(d.verbPast).toBe("Sent message");
    expect(d.meta).toBe("Slack");
  });

  it("keeps a brand-new code-write verb rendering as a diff", () => {
    // WHY: isCodeChangeTool in lib/canvas/detection.ts is the shared source of
    // truth for "this is Jarvis coding". A write verb the verb table has not
    // caught up with must not degrade to a grey generic row.
    const d = describeStep(call("github__delete_file", { path: "a/b/gone.ts" }));
    expect(d.kind).toBe("diff");
    expect(d.verbPast).toBe("Deleted gone.ts");
  });
});

describe("describeStep speaks the tenses §6 asks for", () => {
  it("names the file it is editing, present and past", () => {
    const d = describeStep(call("claude_code__Edit", { file_path: "/repo/core/cmd/infinity/migrate.go" }));
    expect(d.verb).toBe("Editing migrate.go");
    expect(d.verbPast).toBe("Edited migrate.go");
    expect(d.meta).toBe("/repo/core/cmd/infinity"); // meta is the directory, not the id
    expect(d.kind).toBe("diff");
  });

  it.each([
    ["bash_run", { command: "go build ./..." }, "Running a command", "Ran a command"],
    ["remember", { text: "the boss hates em dashes" }, "Saving a note", "Saved a note"],
    ["goal_update", { status: "blocked" }, "Updating the goal", "Updated the goal"],
    ["web_search", { query: "geist font" }, "Searching the web", "Searched the web"],
    ["fs_read", { path: "/w/app/page.tsx" }, "Reading page.tsx", "Read page.tsx"],
    ["git_commit", { message: "fix the gate" }, "Committing the changes", "Committed the changes"],
  ])(
    "%s reads as plain English",
    (name: string, input: Record<string, unknown>, present: string, past: string) => {
    const d = describeStep(call(name, input));
    expect(d.verb).toBe(present);
    expect(d.verbPast).toBe(past);
    },
  );

  it("draws meta from the field that is the point of the call", () => {
    // WHY: the meta line is the only context on a collapsed row. Taking the
    // wrong field (an id, a flag) makes the whole ledger useless.
    expect(describeStep(call("bash_run", { command: "go test ./...", timeout: 30 })).meta).toBe("go test ./...");
    expect(describeStep(call("web_search", { query: "pgvector hnsw" })).meta).toBe("pgvector hnsw");
    expect(describeStep(call("git_commit", { message: "wire the gate\n\nlong body" })).meta).toBe("wire the gate");
  });

  it("switches the detail kind on a failure and on a gate block", () => {
    const c = call("bash_run", { command: "go build" });
    expect(describeStep(c, { id: c.id, name: c.name, output: "boom", is_error: true }).kind).toBe("failure");
    expect(
      describeStep(c, { id: c.id, name: c.name, output: "BLOCKED: needs approval. Trust contract: abcd1234" }).kind,
    ).toBe("approval");
  });
});

// ─── status derivation: ONE source of truth ─────────────────────────────────

describe("deriveStatus", () => {
  it("parks a gated call on approval and keeps the contract id", () => {
    // WHY: the agent loop is BLOCKED inside WaitForDecision. Reading this as
    // "running" would put a spinner on a row that will never finish by itself.
    const m = toolMessage("bash_run", { awaitingContract: "11112222-3333", input: { command: "rm -rf /" } });
    const s = deriveStatus(m);
    expect(s.status).toBe("approval");
    expect(s.awaiting).toBe(true);
    expect(s.contractId).toBe("11112222-3333");
  });

  it("reads the gate's BLOCKED sentinel out of the result", () => {
    const m = toolMessage("bash_run", {
      output: "BLOCKED: destructive command. Trust contract: 9f8e7d6c-1111-2222",
    });
    const s = deriveStatus(m);
    expect(s.status).toBe("approval");
    expect(s.gated).toBe(true);
    expect(s.contractId).toBe("9f8e7d6c-1111-2222");
    expect(detectGated("all good").gated).toBe(false);
  });

  it("never lets a stopped call read as done", () => {
    // WHY (2026-08-26): the turn ended before the result frame arrived. Nobody
    // knows whether the call did anything. Rendering it green is a lie, and
    // rendering it running leaves a timer ticking forever.
    const m = toolMessage("code_agent", { interrupted: true, input: { task: "refactor" } });
    const s = deriveStatus(m);
    expect(s.status).toBe("stopped");
    expect(s.status).not.toBe("done");
    expect(s.status).not.toBe("running");
  });

  it("covers running, error and done", () => {
    expect(deriveStatus(toolMessage("bash_run", { running: true })).status).toBe("running");
    expect(deriveStatus(toolMessage("bash_run", { isError: true, output: "no" })).status).toBe("error");
    expect(deriveStatus(toolMessage("bash_run", {})).status).toBe("done");
  });
});

// ─── coalescing ─────────────────────────────────────────────────────────────

describe("coalesce folds a run of the same work", () => {
  it("counts commands, files read, and files edited", () => {
    const items = coalesce([
      toolMessage("bash_run", { input: { command: "go build" } }),
      toolMessage("bash_run", { input: { command: "go vet ./..." } }),
      toolMessage("bash_run", { input: { command: "go test ./..." } }),
    ]);
    expect(items).toHaveLength(1);
    expect(items[0].verbPast).toBe("Ran 3 commands");
    expect(items[0].count).toBe(3);

    const reads = coalesce(
      ["a.ts", "b.ts", "c.ts", "d.ts", "e.ts"].map((f) => toolMessage("fs_read", { input: { path: `/w/${f}` } })),
    );
    expect(reads).toHaveLength(1);
    expect(reads[0].verbPast).toBe("Read 5 files");

    const edits = coalesce([
      toolMessage("claude_code__Edit", { input: { file_path: "/r/serve.go" } }),
      toolMessage("fs_edit", { input: { path: "/r/migrate.go" } }),
    ]);
    expect(edits).toHaveLength(1);
    expect(edits[0].verbPast).toBe("Edited 2 files");
    // Mac-bridge Edit and cloud fs_edit are the SAME work to the boss; folding
    // them apart would make a cross-bridge session read as twice the churn.
  });

  it("keeps every underlying message on the folded row", () => {
    // WHY: the component renders the group's detail from `messages`. Dropping
    // them would make the counted row untappable, so the boss could not see
    // WHICH three commands ran.
    const msgs = [
      toolMessage("bash_run", { input: { command: "go build" } }),
      toolMessage("bash_run", { input: { command: "go vet" } }),
      toolMessage("bash_run", { input: { command: "go test" } }),
    ];
    const [item] = coalesce(msgs);
    expect(item.messages).toHaveLength(3);
    expect(item.messages.map((m) => m.id)).toEqual(msgs.map((m) => m.id));
    // and the meta names all three, not only the first
    expect(item.meta).toBe("go build, go vet, go test");
  });

  it("breaks the run when the verb changes", () => {
    const items = coalesce([
      toolMessage("bash_run", { input: { command: "go build" } }),
      toolMessage("fs_read", { input: { path: "/w/a.ts" } }),
      toolMessage("bash_run", { input: { command: "go test" } }),
    ]);
    expect(items.map((i) => i.verbPast)).toEqual(["Ran a command", "Read a.ts", "Ran a command"]);
  });

  it("NEVER merges across a failure", () => {
    // WHY: "Ran 3 commands" with one silently red inside is exactly the
    // empty-because-broken-reads-as-fine failure CLAUDE.md bans. The break
    // makes the failing call its own row, so it can open itself and explain.
    const items = coalesce([
      toolMessage("bash_run", { input: { command: "go build" } }),
      toolMessage("bash_run", { input: { command: "go test" }, isError: true, output: "FAIL" }),
      toolMessage("bash_run", { input: { command: "go vet" } }),
    ]);
    expect(items).toHaveLength(3);
    expect(items.map((i) => i.status)).toEqual(["done", "error", "done"]);
    expect(items[1].count).toBe(1);
    expect(items[1].kind).toBe("failure");
  });

  it("NEVER folds an approval away", () => {
    // WHY: an approval is a decision the boss has to make. Counted into a
    // neighbouring row it becomes invisible and the agent loop hangs forever.
    const items = coalesce([
      toolMessage("bash_run", { input: { command: "ls" } }),
      toolMessage("bash_run", { input: { command: "rm -rf build" }, awaitingContract: "c-1" }),
      toolMessage("bash_run", { input: { command: "ls" } }),
    ]);
    expect(items).toHaveLength(3);
    expect(items[1].status).toBe("approval");
    expect(items[1].count).toBe(1);
    expect(items[1].contractId).toBe("c-1");
  });

  it("NEVER folds a gated (BLOCKED) result away either", () => {
    const items = coalesce([
      toolMessage("bash_run", { input: { command: "ls" } }),
      toolMessage("bash_run", {
        input: { command: "rm -rf /" },
        output: "BLOCKED: destructive. Trust contract: aa11bb22",
      }),
      toolMessage("bash_run", { input: { command: "ls" } }),
    ]);
    expect(items).toHaveLength(3);
    expect(items[1].gated).toBe(true);
    expect(items[1].contractId).toBe("aa11bb22");
  });

  it("NEVER folds a stopped call away, and it does not read as done", () => {
    const items = coalesce([
      toolMessage("bash_run", { input: { command: "ls" } }),
      toolMessage("bash_run", { input: { command: "sleep 900" }, interrupted: true }),
      toolMessage("bash_run", { input: { command: "ls" } }),
    ]);
    expect(items).toHaveLength(3);
    expect(items[1].status).toBe("stopped");
    expect(items.filter((i) => i.status === "done")).toHaveLength(2);
  });

  it("keeps decision cards standing alone", () => {
    // WHY §6: PlanProposalCard / SkillProposalCard / AgentTeamCard never fold.
    // Enforced here (structurally) so no component has to remember the rule.
    expect(DECISION_TOOLS.has("plan_create")).toBe(true);
    expect(DECISION_TOOLS.has("skill_propose")).toBe(true);
    expect(DECISION_TOOLS.has("agent_team_start")).toBe(true);
    const items = coalesce([
      toolMessage("plan_create", { input: { title: "Ship phase 2" } }),
      toolMessage("plan_create", { input: { title: "Ship phase 3" } }),
    ]);
    expect(items).toHaveLength(2);
    expect(items.every((i) => i.count === 1)).toBe(true);
  });

  it("stays live while any member of the run is still working", () => {
    const items = coalesce([
      toolMessage("bash_run", { input: { command: "go build" } }),
      toolMessage("bash_run", { input: { command: "go test" }, running: true }),
    ]);
    expect(items).toHaveLength(1);
    expect(items[0].status).toBe("running");
    // A live row speaks in the present tense so the boss reads it as happening.
    expect(items[0].label).toBe(items[0].verb);
    expect(items[0].label).toBe("Running 2 commands");
    expect(activityIsLive(items)).toBe(true);
  });

  it("settled rows speak in the past tense", () => {
    const items = coalesce([toolMessage("fs_edit", { input: { path: "/r/serve.go" } })]);
    expect(items[0].label).toBe("Edited serve.go");
    expect(activityIsLive(items)).toBe(false);
  });

  it("ignores the boss's messages and the final reply", () => {
    // WHY: only churn belongs in the ledger. A user turn or Jarvis's actual
    // answer showing up as a "step" would double-render the conversation.
    const items = coalesce([
      { id: "u1", role: "user", text: "build it", createdAt: T0 },
      narration("Let me check the plan first. Then I will build."),
      thinking("considering the gate"),
      toolMessage("bash_run", { input: { command: "go build" } }),
      { id: "a1", role: "assistant", text: "Done, boss.", createdAt: T0 + 9000 },
    ]);
    expect(items.map((i) => i.kind)).toEqual(["note", "thought", "terminal"]);
  });
});

// ─── headline and summary ────────────────────────────────────────────────────

describe("headlineFor", () => {
  const items = coalesce([toolMessage("claude_code__Edit", { input: { file_path: "/r/migrate.go" }, running: true })]);

  it("prefers the caller's narration, first sentence only", () => {
    expect(headlineFor(items, "Checking the migration runner. Then I will patch it.")).toBe(
      "Checking the migration runner.",
    );
  });

  it("falls back to the turn's own interim narration", () => {
    const withNote = coalesce([
      narration("Let me look at the gate. It might be the header."),
      toolMessage("bash_run", { input: { command: "go build" }, running: true }),
    ]);
    expect(headlineFor(withNote)).toBe("Let me look at the gate.");
  });

  it("falls back to the live step's own present-tense verb", () => {
    // WHY: "Working" tells the boss nothing. When the ledger knows a file is
    // being edited right now, that IS the headline.
    expect(headlineFor(items)).toBe("Editing migrate.go");
  });

  it("says Working only when there is genuinely nothing to say", () => {
    expect(headlineFor([])).toBe("Working");
  });

  it("firstSentence handles prose without terminal punctuation", () => {
    expect(firstSentence("no full stop here")).toBe("no full stop here");
    expect(firstSentence("")).toBe("");
  });
});

describe("summaryFor", () => {
  it("reads as the §6 example: duration then the three most telling verbs", () => {
    const items = coalesce([
      toolMessage("plan_revise", { input: { reason: "scope moved" }, at: T0, endedAt: T0 + 500 }),
      toolMessage("bash_run", { input: { command: "go build" }, at: T0 + 1000 }),
      toolMessage("bash_run", { input: { command: "go vet" }, at: T0 + 2000 }),
      toolMessage("bash_run", { input: { command: "go test" }, at: T0 + 3000 }),
      toolMessage("fs_edit", { input: { path: "/r/a.go" }, at: T0 + 4000 }),
      toolMessage("fs_edit", { input: { path: "/r/b.go" }, at: T0 + 5000, endedAt: T0 + 134_000 }),
    ]);
    expect(summaryFor(items)).toBe("Worked for 2m 14s · revised the plan, ran 3 commands, edited 2 files");
  });

  it("names the telling work, not merely the first three rows", () => {
    // WHY: a turn that read nine files and shipped one commit is ABOUT the
    // commit. Ranking by kind weight is what keeps the summary honest.
    const items = coalesce([
      ...["a", "b", "c", "d", "e", "f", "g", "h", "i"].map((f, n) =>
        toolMessage("fs_read", { input: { path: `/w/${f}.ts` }, at: T0 + n * 100 }),
      ),
      toolMessage("git_commit", { input: { message: "ship it" }, at: T0 + 2000, endedAt: T0 + 4000 }),
    ]);
    expect(summaryFor(items)).toBe("Worked for 4s · read 9 files, committed the changes");
  });

  it("keeps a brand name capitalised inside the sentence", () => {
    const items = coalesce([toolMessage("github__search_repositories", { at: T0, endedAt: T0 + 1200 })]);
    expect(summaryFor(items)).toBe("Worked for 1s · GitHub · search repositories");
  });

  it("does not summarise a turn as its thinking", () => {
    // WHY: "thought it through" is not what the boss wants to read about a
    // turn that also edited a file.
    const items = coalesce([
      thinking("hmm", T0),
      thinking("still hmm", T0 + 200),
      toolMessage("fs_edit", { input: { path: "/r/a.go" }, at: T0 + 1000, endedAt: T0 + 2000 }),
    ]);
    expect(summaryFor(items)).toBe("Worked for 2s · edited a.go");
  });

  it("handles an empty ledger", () => {
    expect(summaryFor([])).toBe("Worked for a moment");
  });
});

describe("formatDuration", () => {
  it.each([
    [400, "under a second"],
    [1400, "1s"],
    [42_000, "42s"],
    [134_000, "2m 14s"],
    [3_600_000, "60m 0s"],
  ])("%dms → %s", (ms: number, want: string) => {
    expect(formatDuration(ms)).toBe(want);
  });
});
