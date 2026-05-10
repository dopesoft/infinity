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
  Wrench,
  X,
} from "lucide-react";
import { cn } from "@/lib/utils";
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
  const status: "running" | "success" | "error" | "gated" = useMemo(() => {
    if (!result) return "running";
    if (detectGated(result.output).gated) return "gated";
    if (result.is_error) return "error";
    return "success";
  }, [result]);

  // Default collapsed on completion (matches PDF spec). Open while running.
  const [open, setOpen] = useState<boolean>(status === "running");

  if (!call) return null;
  const gated = detectGated(result?.output);
  const isDiff = looksLikeDiff(result?.output);

  return (
    <div className="rounded-xl border bg-card text-card-foreground">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left"
        aria-expanded={open}
      >
        <Wrench className="size-4 shrink-0 text-muted-foreground" aria-hidden />
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
            <pre className="overflow-x-auto whitespace-pre rounded-md bg-muted p-2 font-mono text-[11px] leading-snug scroll-touch sm:text-xs">
              {JSON.stringify(call.input ?? {}, null, 2)}
            </pre>
          </Section>
          {result && (
            <Section title={status === "gated" ? "Awaiting approval" : result.is_error ? "Error" : "Output"}>
              {isDiff ? (
                <DiffPre text={result.output ?? ""} />
              ) : (
                <pre
                  className={cn(
                    "max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-md p-2 font-mono text-[11px] leading-snug scroll-touch sm:text-xs",
                    status === "gated" && "bg-warning/10 text-warning-foreground",
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

function StatusIcon({ status }: { status: "running" | "success" | "error" | "gated" }) {
  if (status === "running") return <Loader2 className="size-4 animate-spin text-info" aria-hidden />;
  if (status === "success") return <Check className="size-4 text-success" aria-hidden />;
  if (status === "gated") return <Lock className="size-4 text-warning" aria-hidden />;
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
    <pre className="max-h-72 overflow-auto rounded-md bg-muted p-2 font-mono text-[11px] leading-snug scroll-touch sm:text-xs">
      {lines.map((line, i) => {
        let cls = "";
        if (line.startsWith("+++") || line.startsWith("---")) cls = "text-muted-foreground";
        else if (line.startsWith("@@")) cls = "text-info";
        else if (line.startsWith("+")) cls = "bg-success/10 text-success";
        else if (line.startsWith("-")) cls = "bg-danger/10 text-danger";
        return (
          <div key={i} className={cn("whitespace-pre-wrap break-words px-1", cls)}>
            {line || " "}
          </div>
        );
      })}
    </pre>
  );
}
