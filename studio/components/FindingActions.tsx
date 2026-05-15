"use client";

import { useEffect, useState } from "react";
import { Check, Loader2, Wrench, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { decideCuriosityQuestion } from "@/lib/api";

// Module-scoped registry of curiosity findings that have been acted on
// in this tab. The heartbeat sometimes broadcasts the same curiosity
// question twice (consecutive ticks before the boss acts), so the chat
// can hold two cards for the same finding. Any card in this session
// that approves or dismisses adds its curiosity_id here and notifies
// every other mounted FindingActions; siblings with the same id collapse
// to the "handled" state so a second click can't double-fire the agent.
type FindingOutcome = "approved" | "dismissed";
const handledFindings = new Map<string, FindingOutcome>();
const handledListeners = new Set<() => void>();

function markHandled(id: string, outcome: FindingOutcome) {
  if (!id) return;
  handledFindings.set(id, outcome);
  for (const l of handledListeners) l();
}

function useHandledOutcome(id: string | undefined): FindingOutcome | null {
  const [, force] = useState(0);
  useEffect(() => {
    const l = () => force((t) => t + 1);
    handledListeners.add(l);
    return () => {
      handledListeners.delete(l);
    };
  }, []);
  return id ? handledFindings.get(id) ?? null : null;
}

// The canned reply "Approve & fix" sends into the current session. The
// recipe lives in the `self-improve-from-finding` skill (a default skill
// shipped with Infinity); this string is the trigger that tells the
// agent to run it. The wording is deliberately imperative + names the
// specific tool calls because earlier softer phrasing
// ("use your skill to fix this") let the model paraphrase the steps
// without actually executing them — reporting on the finding rather
// than applying a change. The skill's trigger phrases still match this
// text so the suggestion prefix kicks in too.
const APPROVE_INSTRUCTION =
  "Approved — fix this finding now. Steps you MUST execute, in order:\n" +
  "1. Call `skills_invoke` with `name: \"self-improve-from-finding\"` to load the recipe.\n" +
  "2. Follow it: diagnose the root cause, then make a real artifact change with " +
  "`skill_create` (rework the relevant skill, same name + bumped version) or " +
  "`memory_write` (procedural-tier rule) — don't just describe what you'd do.\n" +
  "3. Verify the change against the original evidence (re-read the new skill body / " +
  "`memory_recall` the rule).\n" +
  "4. Reply with the artifact name (e.g. \"reworked composio-search to v1.1.0\") and " +
  "the one-sentence root cause. If a tool call failed, say so — don't paper over it.";

// FindingActions is the "Approve & fix" / "Dismiss" row shown on heartbeat
// finding cards (the seeded DashboardContextCard and the inline proactive
// bubble). Approving marks the curiosity question approved AND fires the
// canned reply so the agent acts in this same conversation; dismissing
// just resolves the question. Both collapse to a status line once acted
// on — and after a refresh the question is no longer "open", so the row
// stops rendering entirely.
export function FindingActions({
  curiosityId,
  onSend,
}: {
  curiosityId: string;
  onSend?: (text: string) => void;
}) {
  const [state, setState] = useState<
    "idle" | "working" | "approved" | "dismissed"
  >("idle");
  // If a sibling card with the same curiosity_id was already acted on,
  // adopt its outcome — we don't want a second click to re-fire the
  // agent for a finding the boss already handled. effectiveState is
  // what we render against.
  const handledOutcome = useHandledOutcome(curiosityId);
  const effectiveState = state !== "idle" ? state : handledOutcome ?? "idle";

  async function approve() {
    if (effectiveState !== "idle") return;
    setState("working");
    // Fire the turn first — the visible "Jarvis is fixing it" feedback
    // matters more than the bookkeeping write, which is best-effort.
    onSend?.(APPROVE_INSTRUCTION);
    if (curiosityId) {
      void decideCuriosityQuestion(curiosityId, "approved");
      markHandled(curiosityId, "approved");
    }
    setState("approved");
  }

  async function dismiss() {
    if (effectiveState !== "idle") return;
    setState("working");
    if (curiosityId) {
      await decideCuriosityQuestion(curiosityId, "dismissed");
      markHandled(curiosityId, "dismissed");
    }
    setState("dismissed");
  }

  if (effectiveState === "approved") {
    return (
      <div className="mt-2 flex items-center gap-1.5 text-[11px] font-medium text-success">
        <Check className="size-3.5" />
        <span>Approved — Jarvis is applying the fix.</span>
      </div>
    );
  }
  if (effectiveState === "dismissed") {
    return (
      <div className="mt-2 flex items-center gap-1.5 text-[11px] text-muted-foreground">
        <X className="size-3.5" />
        <span>Dismissed.</span>
      </div>
    );
  }

  return (
    <div className="mt-2 flex flex-wrap items-center gap-2">
      <Button
        type="button"
        size="sm"
        onClick={approve}
        disabled={state === "working"}
        className="gap-1.5"
      >
        {state === "working" ? (
          <Loader2 className="size-4 animate-spin" />
        ) : (
          <Wrench className="size-4" />
        )}
        Approve &amp; fix
      </Button>
      <Button
        type="button"
        size="sm"
        variant="ghost"
        onClick={dismiss}
        disabled={state === "working"}
        className="gap-1.5 text-muted-foreground"
      >
        <X className="size-4" />
        Dismiss
      </Button>
    </div>
  );
}
