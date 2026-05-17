"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import {
  Check,
  ChevronDown,
  ChevronRight,
  Loader2,
  Lock,
  ShieldCheck,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { ToolIcon } from "@/components/ToolIcon";
import { cn } from "@/lib/utils";
import { decideTrust } from "@/lib/api";
import {
  extractToolFilePath,
  extractToolFilePaths,
  extractToolPreview,
  isCodeChangeTool,
  isRepoWriteTool,
} from "@/lib/canvas/detection";
import type { ChatMessage } from "@/hooks/useChat";

function formatMs(start?: string, end?: string) {
  if (!start) return "";
  const s = new Date(start).getTime();
  const e = end ? new Date(end).getTime() : Date.now();
  const ms = Math.max(0, e - s);
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

// Recognise "BLOCKED: ... Trust contract: <uuid>" outputs synthesised by the
// agent gate so the card can offer a one-tap shortcut to the Trust tab.
const TRUST_CONTRACT_RE = /Trust contract:\s*([0-9a-fA-F-]{8,})/;

function detectGated(output?: string): { gated: boolean; contractId?: string } {
  if (!output || !output.startsWith("BLOCKED:")) return { gated: false };
  const m = output.match(TRUST_CONTRACT_RE);
  return { gated: true, contractId: m?.[1] };
}

// Best-effort unified-diff detection. Used to add subtle red/green hinting
// to lines that look like diff additions/removals so a phone reviewer can
// scan the change without zooming.
function looksLikeDiff(text?: string): boolean {
  if (!text || text.length > 64_000) return false;
  let plus = 0, minus = 0, hunk = 0;
  for (const line of text.split("\n", 200)) {
    if (line.startsWith("+++") || line.startsWith("---")) continue;
    if (line.startsWith("@@")) hunk++;
    else if (line.startsWith("+")) plus++;
    else if (line.startsWith("-")) minus++;
  }
  return hunk > 0 || (plus + minus) >= 4;
}

export function ToolCallCard({ message }: { message: ChatMessage }) {
  const call = message.toolCall;
  const result = message.toolResult;
  // "awaiting" = gate parked it on a contract; agent loop is blocked on
  // approval. Studio renders inline Approve / Deny buttons. When the
  // user taps, decideTrust() flips the contract status, the gate unblocks,
  // and the real tool result arrives as a follow-up tool_result event
  // (which transitions this same card to success / error / gated).
  const awaiting = !result && !!call?.awaiting_approval && !!call?.contract_id;
  const status: "running" | "success" | "error" | "gated" | "awaiting" = useMemo(() => {
    if (awaiting) return "awaiting";
    if (!result) return "running";
    if (detectGated(result.output).gated) return "gated";
    if (result.is_error) return "error";
    return "success";
  }, [result, awaiting]);

  // Classify the tool so the card can choose: high-signal at-a-glance
  // display for code/repo writes (boss wants to SEE Jarvis coding),
  // generic collapsed-tool-row for everything else (chat was getting
  // overrun by giant tool dumps that don't matter once they succeed).
  const isCodeWrite = isCodeChangeTool(call?.name);
  const isRepoWrite = isRepoWriteTool(call?.name);
  const isWriteCall = isCodeWrite || isRepoWrite;
  const filePath = call ? extractToolFilePath(call.input) : null;
  const filePaths = call ? extractToolFilePaths(call.input) : [];
  const preview = call ? extractToolPreview(call.input) : "";

  // Default-open rules — collapsed by default so the transcript stays
  // skimmable on mobile; expanded only when the boss genuinely needs to
  // see something:
  //   • awaiting approval     → must see the Approve/Deny buttons
  //   • error                 → must see what failed
  //   • running + code/repo   → preview of what's being written or
  //                             committed, so "I can tell when Jarvis
  //                             is coding" doesn't require a tap
  // Everything else lands collapsed; the header carries enough signal
  // (tool name, file path, status icon, latency) to skim past.
  const defaultOpen =
    status === "awaiting" ||
    status === "error" ||
    (status === "running" && isWriteCall);
  const [open, setOpen] = useState<boolean>(defaultOpen);
  const [deciding, setDeciding] = useState<"approve" | "deny" | null>(null);
  const [decisionError, setDecisionError] = useState<string | null>(null);
  const [decisionMade, setDecisionMade] = useState<"approved" | "denied" | null>(null);

  if (!call) return null;
  const gated = detectGated(result?.output);
  const isDiff = looksLikeDiff(result?.output);

  async function decide(action: "approve" | "deny") {
    if (!call?.contract_id) return;
    setDeciding(action);
    setDecisionError(null);
    const ok = await decideTrust(
      call.contract_id,
      action === "approve" ? "approved" : "denied",
    );
    setDeciding(null);
    if (ok) {
      setDecisionMade(action === "approve" ? "approved" : "denied");
    } else {
      setDecisionError("Couldn't reach Core. Try again.");
    }
  }

  // Header label — for code/repo writes, lead with what actually matters
  // (the file path, or the count when multiple). The tool name moves
  // into a small subscript so the boss can scan a transcript and pick
  // out file edits without parsing tool ids.
  const headerLabel = (() => {
    if (isCodeWrite) {
      if (filePaths.length > 1) {
        return `${filePaths.length} files`;
      }
      if (filePath) return basename(filePath);
    }
    if (isRepoWrite && preview) {
      // Commit message / PR title — first line, trimmed.
      const first = preview.split("\n")[0].trim();
      if (first) return first.length > 80 ? first.slice(0, 80) + "…" : first;
    }
    return call.name;
  })();
  const headerSubtitle = (() => {
    if (isCodeWrite && filePath && filePaths.length <= 1) {
      // Show directory part as subscript so the basename above reads cleanly.
      const dir = dirname(filePath);
      return dir ? `${shortToolKind(call.name)} · ${dir}` : shortToolKind(call.name);
    }
    if (isCodeWrite && filePaths.length > 1) {
      return shortToolKind(call.name);
    }
    if (isRepoWrite) return shortToolKind(call.name);
    return "";
  })();

  return (
    <div className="min-w-0 max-w-full overflow-hidden rounded-xl border bg-card text-card-foreground">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-start gap-2 px-3 py-2 text-left"
        aria-expanded={open}
      >
        <ToolIcon name={call.name} className="mt-0.5 size-4 shrink-0" />
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="truncate font-mono text-xs sm:text-sm">{headerLabel}</span>
          {headerSubtitle ? (
            <span className="truncate font-mono text-[10px] text-muted-foreground">
              {headerSubtitle}
            </span>
          ) : null}
        </div>
        <StatusIcon status={status} />
        <span className="ml-1 flex items-center gap-2 self-center text-[11px] text-muted-foreground">
          <span>{formatMs(call.started_at, result?.ended_at)}</span>
          {open ? (
            <ChevronDown className="size-4" aria-hidden />
          ) : (
            <ChevronRight className="size-4" aria-hidden />
          )}
        </span>
      </button>

      {open && (
        <div className="space-y-2 border-t px-3 py-2">
          {/* Code-write preview: show what's actually being written before
              dumping the raw JSON. For Edit this is new_string; for Write
              it's the full content; for GitHub push it's the first file.
              Capped height so a huge content blob doesn't blow the
              transcript out — scroll inside the box. */}
          {isWriteCall && preview && (
            <Section title={previewTitle(call.name)}>
              <pre className="min-w-0 max-w-full max-h-64 overflow-y-auto overflow-x-hidden whitespace-pre-wrap break-all rounded-md bg-muted p-2 font-mono text-[11px] leading-snug scroll-touch sm:text-xs">
                {preview}
              </pre>
              {filePaths.length > 1 && (
                <p className="mt-1 text-[10px] text-muted-foreground">
                  + {filePaths.length - 1} more file
                  {filePaths.length - 1 === 1 ? "" : "s"} in this call
                </p>
              )}
            </Section>
          )}
          <Section title="Input">
            <pre className="min-w-0 max-w-full overflow-x-hidden whitespace-pre-wrap break-all rounded-md bg-muted p-2 font-mono text-[11px] leading-snug sm:text-xs">
              {JSON.stringify(call.input ?? {}, null, 2)}
            </pre>
          </Section>
          {status === "awaiting" && (
            <Section title="Approval required">
              <div className="rounded-md border border-warning/40 bg-warning/5 p-2 dark:bg-warning/10">
                <p className="text-xs leading-relaxed text-foreground">
                  This call is paused waiting for your approval. Tap{" "}
                  <span className="font-semibold">Approve</span> and the same
                  command runs immediately — the output shows up right here.
                </p>
                {call.preview ? (
                  <pre className="mt-2 min-w-0 max-w-full max-h-32 overflow-y-auto overflow-x-hidden whitespace-pre-wrap break-all rounded-md bg-muted/70 p-2 font-mono text-[11px] text-muted-foreground scroll-touch sm:text-xs">
                    {call.preview}
                  </pre>
                ) : null}
              </div>
              {decisionMade ? (
                <p className="mt-2 text-xs text-muted-foreground">
                  {decisionMade === "approved"
                    ? "Approved — running now…"
                    : "Denied. Tell the agent if you want it to try something else."}
                </p>
              ) : (
                <div className="mt-2 flex flex-wrap items-center gap-2">
                  <Button
                    size="sm"
                    onClick={() => decide("approve")}
                    disabled={deciding !== null}
                    className="h-9"
                  >
                    {deciding === "approve" ? (
                      <Loader2 className="mr-1 size-4 animate-spin" />
                    ) : (
                      <Check className="mr-1 size-4" />
                    )}
                    Approve
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => decide("deny")}
                    disabled={deciding !== null}
                    className="h-9"
                  >
                    {deciding === "deny" ? (
                      <Loader2 className="mr-1 size-4 animate-spin" />
                    ) : (
                      <X className="mr-1 size-4" />
                    )}
                    Deny
                  </Button>
                </div>
              )}
              {decisionError ? (
                <p className="mt-2 text-xs text-danger">{decisionError}</p>
              ) : null}
            </Section>
          )}
          {result && (
            <Section title={status === "gated" ? "Awaiting approval" : result.is_error ? "Error" : "Output"}>
              {isDiff ? (
                <DiffPre text={result.output ?? ""} />
              ) : (
                <pre
                  className={cn(
                    "min-w-0 max-w-full max-h-72 overflow-y-auto overflow-x-hidden whitespace-pre-wrap break-all rounded-md p-2 font-mono text-[11px] leading-snug scroll-touch sm:text-xs",
                    // Gated rows: tinted background but TEXT IN FOREGROUND
                    // (never the all-white warning-foreground which is
                    // unreadable on the creme bg). The amber accent comes
                    // from the StatusIcon + the left ring below.
                    status === "gated" &&
                      "bg-warning/5 text-foreground ring-1 ring-inset ring-warning/40 dark:bg-warning/10",
                    status === "error" && "bg-danger/10 text-danger",
                    status === "success" && "bg-muted",
                  )}
                >
                  {result.output ?? ""}
                </pre>
              )}
              {gated.gated && (
                <Link
                  href={gated.contractId ? `/trust?focus=${gated.contractId}` : "/trust"}
                  className="mt-2 inline-flex h-9 items-center gap-1.5 rounded-md border bg-background px-3 text-xs font-medium hover:bg-accent"
                >
                  <ShieldCheck className="size-4" />
                  Approve in Trust tab
                </Link>
              )}
            </Section>
          )}
        </div>
      )}
    </div>
  );
}

function StatusIcon({ status }: { status: "running" | "success" | "error" | "gated" | "awaiting" }) {
  if (status === "running") return <Loader2 className="size-4 animate-spin text-info" aria-hidden />;
  if (status === "success") return <Check className="size-4 text-success" aria-hidden />;
  if (status === "gated" || status === "awaiting") return <Lock className="size-4 text-warning" aria-hidden />;
  return <X className="size-4 text-danger" aria-hidden />;
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
        {title}
      </div>
      {children}
    </div>
  );
}

// basename / dirname — tiny path helpers (no `path` polyfill needed for the
// browser). Works for both posix paths the agent uses (Mac bridge,
// GitHub) and Windows-style ones in case a Cloud session ever yields
// them.
function basename(p: string): string {
  const trimmed = p.replace(/[\\/]+$/, "");
  const idx = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  return idx >= 0 ? trimmed.slice(idx + 1) : trimmed;
}
function dirname(p: string): string {
  const trimmed = p.replace(/[\\/]+$/, "");
  const idx = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  return idx >= 0 ? trimmed.slice(0, idx) : "";
}

// shortToolKind — the verb the boss reads as a subscript under a file
// path. "claude_code__Edit" → "edit", "github__push_files" → "push",
// etc. Falls back to the raw tool name so unknown tools still show
// something legible.
function shortToolKind(name: string): string {
  const lower = name.toLowerCase();
  if (lower.endsWith("__edit") || lower.endsWith("__multiedit")) return "edit";
  if (lower.endsWith("__write") || lower.endsWith("__notebookedit")) return "write";
  if (lower === "fs_edit") return "edit";
  if (lower === "fs_save") return "save";
  if (lower === "github__create_or_update_file") return "github · upsert";
  if (lower === "github__push_files") return "github · push";
  if (lower === "github__delete_file") return "github · delete";
  if (lower === "git_commit") return "git commit";
  if (lower === "git_push") return "git push";
  if (lower === "git_stage") return "git stage";
  if (lower === "github__create_pull_request") return "github · open PR";
  if (lower === "github__merge_pull_request") return "github · merge PR";
  if (lower === "github__create_branch") return "github · branch";
  return name;
}

// previewTitle — labels the preview block based on the tool kind so the
// boss knows what they're looking at (the replacement text vs the full
// new file vs the commit message etc).
function previewTitle(name: string): string {
  const lower = name.toLowerCase();
  if (lower.endsWith("__edit") || lower === "fs_edit" || lower.endsWith("__multiedit")) {
    return "Replacement text";
  }
  if (lower.endsWith("__write") || lower === "fs_save") return "New file contents";
  if (lower === "github__create_or_update_file") return "New file contents";
  if (lower === "github__push_files") return "First file contents";
  if (lower === "git_commit") return "Commit message";
  if (lower === "github__create_pull_request" || lower === "github__update_pull_request") {
    return "PR description";
  }
  return "Preview";
}

// DiffPre renders unified-diff text with per-line color hints. Pure presentation —
// no parsing of multi-file structure (Claude Code returns single-file diffs in
// most edits, and large multi-file ones get the same treatment line by line).
function DiffPre({ text }: { text: string }) {
  const lines = text.split("\n");
  return (
    <pre className="min-w-0 max-w-full max-h-72 overflow-y-auto overflow-x-hidden rounded-md bg-muted p-2 font-mono text-[11px] leading-snug scroll-touch sm:text-xs">
      {lines.map((line, i) => {
        let cls = "";
        if (line.startsWith("+++") || line.startsWith("---")) cls = "text-muted-foreground";
        else if (line.startsWith("@@")) cls = "text-info";
        else if (line.startsWith("+")) cls = "bg-success/10 text-success";
        else if (line.startsWith("-")) cls = "bg-danger/10 text-danger";
        return (
          <div key={i} className={cn("whitespace-pre-wrap break-all px-1", cls)}>
            {line || " "}
          </div>
        );
      })}
    </pre>
  );
}
