"use client";

import { useState } from "react";
import { Check, Loader2, Plus, Sparkles, TriangleAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageSectionHeader } from "@/components/ui/page-tabs";
import { cn } from "@/lib/utils";
import { writeCockpit } from "@/lib/pursuits/pc/api";
import { CoachCard } from "./CoachCard";
import { EmptyNote, PCCard } from "./PCPrimitives";
import type { PCCockpit, PCEvidence, PCProof } from "@/lib/pursuits/pc/types";

/* Today - the action-first face of the cockpit.
 *
 * Order is deliberate and never changes: what the coach wants next, the
 * rehearsal material for that phase, the concrete proof actions, then the
 * capture box. The boss should be able to act without scrolling or choosing
 * a tab, which is why identity, memories, and history all live behind tabs
 * and today does not.
 */
export function TodayPanel({
  cockpit,
  onUpdated,
}: {
  cockpit: PCCockpit;
  onUpdated: (next: PCCockpit) => void;
}) {
  const rehearsing = cockpit.guidance.phase === "morning" || cockpit.guidance.phase === "recovery";

  return (
    <div className="space-y-4">
      <CoachCard cockpit={cockpit} onUpdated={onUpdated} />

      {cockpit.adjustment ? (
        <PCCard className="border-warning/40 bg-warning/5">
          <div className="flex min-w-0 items-start gap-2.5">
            <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden />
            <div className="min-w-0">
              <p className="text-sm font-medium text-foreground">{cockpit.adjustment.headline}</p>
              <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                {cockpit.adjustment.body}
              </p>
            </div>
          </div>
        </PCCard>
      ) : null}

      {rehearsing ? <RehearsalMaterial cockpit={cockpit} /> : null}

      <ProofList cockpit={cockpit} onUpdated={onUpdated} />
      <CaptureBox cockpit={cockpit} onUpdated={onUpdated} />
    </div>
  );
}

/* Rehearsal material - what the coach wants in front of him while he
 * rehearses: the identity itself, the memory picked for today (the server
 * decides which, so a seeded chat names the same one), and wherever the
 * pressure test said the identity would crack. */
function RehearsalMaterial({ cockpit }: { cockpit: PCCockpit }) {
  const { state, rehearsal_memory: memory } = cockpit;
  const pressure = [
    { label: "Where it cracks", value: state.pressure_test?.fear ?? "" },
    { label: "The doubt", value: state.pressure_test?.doubt ?? "" },
    { label: "The alternative", value: state.pressure_test?.alternate ?? "" },
  ].filter((p) => p.value.trim().length > 0);

  return (
    <PCCard>
      <PageSectionHeader title="rehearsal material" />
      <div className="mt-3 space-y-3">
        <div className="min-w-0">
          <p className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
            The identity you are practising
          </p>
          <p className="mt-1 whitespace-pre-wrap break-words text-sm text-foreground">
            {state.current_identity}
          </p>
        </div>

        {memory ? (
          <div className="min-w-0 rounded-xl border bg-muted/30 p-3">
            <p className="flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
              <Sparkles className="size-3" aria-hidden />
              Memory to return to first
            </p>
            <p className="mt-1 break-words text-sm font-medium text-foreground">{memory.title}</p>
            {memory.body.trim() ? (
              <p className="mt-1 whitespace-pre-wrap break-words text-sm text-muted-foreground">
                {memory.body}
              </p>
            ) : null}
          </div>
        ) : null}

        {pressure.length > 0 ? (
          <div className="min-w-0">
            <p className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
              What you said would test it
            </p>
            <ul className="mt-1 space-y-1">
              {pressure.map((p) => (
                <li key={p.label} className="break-words text-sm text-muted-foreground">
                  <span className="text-foreground">{p.label}:</span> {p.value}
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </div>
    </PCCard>
  );
}

/* Proof actions - the concrete behaviour that backs the identity today.
 * Pledging one here and pledging one through the morning form are the same
 * write, so the two can never disagree. */
function ProofList({
  cockpit,
  onUpdated,
}: {
  cockpit: PCCockpit;
  onUpdated: (next: PCCockpit) => void;
}) {
  const [label, setLabel] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function run(key: string, fn: () => Promise<PCCockpit>) {
    setBusy(key);
    setError(null);
    try {
      onUpdated(await fn());
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save that.");
    } finally {
      setBusy(null);
    }
  }

  const add = () => {
    const value = label.trim();
    if (!value) return;
    void run("add", async () => {
      const next = await writeCockpit(cockpit.pursuit.id, "proof", { label: value });
      setLabel("");
      return next;
    });
  };

  return (
    <PCCard>
      <PageSectionHeader title="today's proof" count={cockpit.today_proofs.length} />

      {cockpit.today_proofs.length === 0 ? (
        <EmptyNote className="mt-3">
          Nothing pledged yet. One deliberate action that only makes sense if the identity is true,
          small enough that you are certain to do it.
        </EmptyNote>
      ) : (
        <ul className="mt-3 space-y-1.5">
          {cockpit.today_proofs.map((p) => (
            <ProofRow
              key={p.id}
              proof={p}
              busy={busy === p.id}
              onToggle={() =>
                void run(p.id, () =>
                  writeCockpit(cockpit.pursuit.id, "proof/taken", {
                    proof_id: p.id,
                    taken: !p.taken,
                  }),
                )
              }
            />
          ))}
        </ul>
      )}

      <form
        className="mt-3 flex flex-col gap-2 sm:flex-row"
        onSubmit={(e) => {
          e.preventDefault();
          add();
        }}
      >
        <Input
          value={label}
          onChange={(e) => setLabel(e.target.value)}
          placeholder="Pledge another proof action for today"
          inputMode="text"
          aria-label="New proof action"
          className="min-w-0 flex-1"
        />
        <Button type="submit" variant="secondary" disabled={!label.trim() || busy === "add"}>
          {busy === "add" ? (
            <Loader2 className="size-4 animate-spin" aria-hidden />
          ) : (
            <Plus className="size-4" aria-hidden />
          )}
          Pledge
        </Button>
      </form>

      {error ? (
        <p className="mt-2 text-sm text-danger" role="alert">
          {error}
        </p>
      ) : null}
    </PCCard>
  );
}

function ProofRow({
  proof,
  busy,
  onToggle,
}: {
  proof: PCProof;
  busy: boolean;
  onToggle: () => void;
}) {
  return (
    <li className="flex min-w-0 items-start gap-2.5">
      <button
        type="button"
        onClick={onToggle}
        disabled={busy}
        aria-label={proof.taken ? "Mark proof not taken" : "Mark proof taken"}
        aria-pressed={proof.taken}
        className={cn(
          "mt-0.5 inline-flex size-7 shrink-0 items-center justify-center rounded-full border transition-colors",
          proof.taken
            ? "border-success bg-success text-success-foreground"
            : "border-border bg-background hover:border-foreground/40",
        )}
      >
        {busy ? (
          <Loader2 className="size-3.5 animate-spin" aria-hidden />
        ) : proof.taken ? (
          <Check className="size-3.5" aria-hidden />
        ) : null}
      </button>
      <div className="min-w-0 flex-1">
        <p
          className={cn(
            "break-words text-sm",
            proof.taken ? "text-muted-foreground line-through" : "text-foreground",
          )}
        >
          {proof.label}
        </p>
        {proof.note.trim() ? (
          <p className="mt-0.5 break-words text-xs text-muted-foreground">{proof.note}</p>
        ) : null}
      </div>
    </li>
  );
}

/* Capture - evidence the identity held, or resistance where the old pattern
 * ran. Available all day rather than only inside the midday form, because the
 * moment worth capturing rarely arrives when a form is open. */
function CaptureBox({
  cockpit,
  onUpdated,
}: {
  cockpit: PCCockpit;
  onUpdated: (next: PCCockpit) => void;
}) {
  const [kind, setKind] = useState<"evidence" | "resistance">("evidence");
  const [body, setBody] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save() {
    const value = body.trim();
    if (!value) return;
    setSaving(true);
    setError(null);
    try {
      const next = await writeCockpit(cockpit.pursuit.id, "evidence", { kind, body: value });
      setBody("");
      onUpdated(next);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save that.");
    } finally {
      setSaving(false);
    }
  }

  return (
    <PCCard>
      <PageSectionHeader title="captures today" count={cockpit.today_evidence.length} />

      <div className="mt-3 flex gap-1.5">
        <KindToggle active={kind === "evidence"} onClick={() => setKind("evidence")}>
          evidence
        </KindToggle>
        <KindToggle active={kind === "resistance"} onClick={() => setKind("resistance")}>
          resistance
        </KindToggle>
      </div>

      <p className="mt-2 text-xs text-muted-foreground">
        {kind === "evidence"
          ? "A moment the identity held, however small."
          : "A moment the old pattern ran. Data, not a score."}
      </p>

      <form
        className="mt-2 flex flex-col gap-2 sm:flex-row"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <Input
          value={body}
          onChange={(e) => setBody(e.target.value)}
          placeholder={kind === "evidence" ? "What happened?" : "Where did it show up?"}
          inputMode="text"
          aria-label={`Capture ${kind}`}
          className="min-w-0 flex-1"
        />
        <Button type="submit" variant="secondary" disabled={!body.trim() || saving}>
          {saving ? <Loader2 className="size-4 animate-spin" aria-hidden /> : null}
          Capture
        </Button>
      </form>

      {error ? (
        <p className="mt-2 text-sm text-danger" role="alert">
          {error}
        </p>
      ) : null}

      {cockpit.today_evidence.length === 0 ? (
        <EmptyNote className="mt-3">Nothing captured yet today.</EmptyNote>
      ) : (
        <ul className="mt-3 space-y-1.5">
          {cockpit.today_evidence.map((e) => (
            <EvidenceRow key={e.id} evidence={e} />
          ))}
        </ul>
      )}
    </PCCard>
  );
}

function KindToggle({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "inline-flex h-8 shrink-0 items-center rounded-full border px-3.5 font-mono text-[11px] uppercase tracking-wider transition-colors",
        active
          ? "border-foreground bg-foreground text-background"
          : "border-border bg-muted text-muted-foreground hover:bg-accent",
      )}
    >
      {children}
    </button>
  );
}

export function EvidenceRow({ evidence }: { evidence: PCEvidence }) {
  return (
    <li className="flex min-w-0 items-start gap-2">
      <span
        className={cn(
          "mt-1 size-1.5 shrink-0 rounded-full",
          evidence.kind === "resistance" ? "bg-rose-400" : "bg-success",
        )}
        aria-hidden
      />
      <p className="min-w-0 flex-1 break-words text-sm text-foreground">
        {evidence.body}
        <span className="ml-1.5 font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
          {evidence.kind}
        </span>
      </p>
    </li>
  );
}
