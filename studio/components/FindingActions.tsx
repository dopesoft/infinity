"use client";

import { useState } from "react";
import { Check, Loader2, Wrench, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { decideCuriosityQuestion } from "@/lib/api";

// The canned reply "Approve & fix" sends into the current session. It's a
// thin pointer at the `self-improve-from-finding` skill — a default skill
// shipped with Infinity whose body IS the recipe (diagnose → fix → verify
// → confirm). The *judgment* lives in that SKILL.md, not here; this string
// just tells the agent to run it. The skill's trigger phrases also match
// this text, so the agent gets it suggested even if it ignores the name.
const APPROVE_INSTRUCTION =
  "Approved — use your self-improve-from-finding skill to apply a durable fix " +
  "for this finding, then confirm exactly what you changed and how you verified it.";

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

  async function approve() {
    if (state !== "idle") return;
    setState("working");
    // Fire the turn first — the visible "Jarvis is fixing it" feedback
    // matters more than the bookkeeping write, which is best-effort.
    onSend?.(APPROVE_INSTRUCTION);
    if (curiosityId) {
      void decideCuriosityQuestion(curiosityId, "approved");
    }
    setState("approved");
  }

  async function dismiss() {
    if (state !== "idle") return;
    setState("working");
    if (curiosityId) {
      await decideCuriosityQuestion(curiosityId, "dismissed");
    }
    setState("dismissed");
  }

  if (state === "approved") {
    return (
      <div className="mt-2 flex items-center gap-1.5 text-[11px] font-medium text-success">
        <Check className="size-3.5" />
        <span>Approved — Jarvis is applying the fix.</span>
      </div>
    );
  }
  if (state === "dismissed") {
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
