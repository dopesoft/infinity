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

  // Default collapsed on completion (matches PDF spec). Open while running
  // or while awaiting approval so the boss sees the buttons immediately.
  const [open, setOpen] = useState<boolean>(status === "running" || status === "awaiting");
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

  return (
    <div className="min-w-0 max-w-full overflow-hidden rounded-xl border bg-card text-card-foreground">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left"
        aria-expanded={open}
      >
        <ToolIcon name={call.name} className="size-4 shrink-0" />
        <span className="truncate font-mono text-xs sm:text-sm">{call.name}</span>
        <StatusIcon status={status} />
        <span className="ml-auto flex items-center gap-2 text-[11px] text-muted-foreground">
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
