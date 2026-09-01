"use client";

import { useEffect, useMemo, useState } from "react";
import { Bot, CheckCircle2, ChevronDown, ChevronRight, Users } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import type { ChatMessage } from "@/hooks/useChat";
import { fetchChatSettings, type ChatSettings } from "@/lib/api";
import { cn } from "@/lib/utils";

type MemberInput = {
  role?: string;
  task?: string;
  capability?: string;
  model?: string;
};

type MemberResult = MemberInput & {
  id?: string;
  session_id?: string;
  status?: string;
  summary?: string;
  error?: string;
  input_tokens?: number;
  output_tokens?: number;
};

type TeamResult = {
  ok?: boolean;
  team_id?: string;
  status?: string;
  goal?: string;
  reason?: string;
  members?: MemberResult[];
  input_tokens?: number;
  output_tokens?: number;
  message?: string;
  error?: string;
  needs_confirmation?: boolean;
};

function parseResult(output?: string): TeamResult | null {
  if (!output) return null;
  try {
    return JSON.parse(output) as TeamResult;
  } catch {
    return null;
  }
}

export function AgentTeamCard({ message }: { message: ChatMessage }) {
  const call = message.toolCall;
  const result = message.toolResult;
  const parsed = useMemo(() => parseResult(result?.output), [result?.output]);
  const input = (call?.input ?? {}) as {
    goal?: string;
    reason?: string;
    members?: MemberInput[];
  };
  const pending = !result;
  const failed = !!result?.is_error || parsed?.ok === false;
  const [settings, setSettings] = useState<ChatSettings | null>(null);
  const [open, setOpen] = useState(true);
  useEffect(() => {
    const ac = new AbortController();
    fetchChatSettings(ac.signal).then((s) => {
      if (!s) return;
      setSettings(s);
      setOpen(s.default_team_card_state !== "collapsed");
    });
    return () => ac.abort();
  }, []);
  if (!call) return null;
  if (settings?.show_team_activity === "off") return null;

  const members: MemberResult[] = parsed?.members?.length
    ? parsed.members
    : (input.members ?? []).map((m) => ({ ...m }));
  const statusLabel = pending
    ? "running"
    : failed
      ? parsed?.needs_confirmation
        ? "needs confirmation"
        : "error"
      : "done";
  const totalTokens = (parsed?.input_tokens ?? 0) + (parsed?.output_tokens ?? 0);

  return (
    <div className="min-w-0 max-w-full overflow-hidden rounded-xl border bg-card text-card-foreground">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-start gap-2 px-3 py-2 text-left"
        aria-expanded={open}
      >
        <Users className="mt-0.5 size-4 shrink-0 text-info" aria-hidden />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate text-sm font-semibold">Agent team</span>
            <span
              className={cn(
                "rounded-full px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider",
                pending && "bg-info/10 text-info",
                !pending && !failed && "bg-success/10 text-success",
                failed && "bg-danger/10 text-danger",
              )}
            >
              {statusLabel}
            </span>
          </div>
          <p className="truncate text-xs text-muted-foreground">
            {input.goal || parsed?.goal || "Specialist agents spawned"}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2 text-[11px] text-muted-foreground">
          <span>{members.length} agents</span>
          {pending ? <Spinner className="size-4" /> : <CheckCircle2 className="size-4" />}
          {open ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}
        </div>
      </button>

      {open && (
        <div className="space-y-3 border-t px-3 py-2">
          {input.reason || parsed?.reason ? (
            <p className="break-words text-xs leading-relaxed text-muted-foreground [overflow-wrap:anywhere]">
              {input.reason || parsed?.reason}
            </p>
          ) : null}

          <div className="space-y-2">
            {members.map((m, idx) => (
              <div key={m.id ?? `${m.role}-${idx}`} className="min-w-0 rounded-md border bg-muted/30 p-2">
                <div className="flex min-w-0 items-start gap-2">
                  <Bot className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                  <div className="min-w-0 flex-1">
                    <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                      <span className="font-medium text-sm">{m.role || `Agent ${idx + 1}`}</span>
                      {m.capability && (
                        <span className="rounded bg-background px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                          {m.capability}
                        </span>
                      )}
                      {m.status && (
                        <span className="rounded bg-background px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                          {m.status}
                        </span>
                      )}
                    </div>
                    {m.task && <p className="mt-1 break-words text-xs leading-relaxed text-muted-foreground [overflow-wrap:anywhere]">{m.task}</p>}
                    {settings?.show_team_activity !== "compact" && settings?.show_worker_summaries !== false && m.summary && (
                      <pre className="mt-2 max-h-36 max-w-full overflow-y-auto overflow-x-hidden whitespace-pre-wrap break-words rounded bg-background p-2 text-[11px] leading-relaxed [overflow-wrap:anywhere]">
                        {m.summary}
                      </pre>
                    )}
                    {m.error && <p className="mt-1 text-xs text-danger">{m.error}</p>}
                  </div>
                </div>
              </div>
            ))}
          </div>

          {settings?.show_token_usage !== false && !pending && totalTokens > 0 && (
            <p className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
              tokens {parsed?.input_tokens ?? 0} in / {parsed?.output_tokens ?? 0} out
            </p>
          )}
          {parsed?.error && <p className="text-xs text-danger">{parsed.error}</p>}
        </div>
      )}
    </div>
  );
}
