"use client";

import { useState } from "react";
import { Loader2, ArrowLeft, ArrowRight, Compass } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { writeCockpit } from "@/lib/pursuits/pc/api";
import { Field } from "./CoachCard";
import type { PCCockpit } from "@/lib/pursuits/pc/types";

/* Guided onboarding.
 *
 * Three steps, in the order Maltz's method needs them: the objective you are
 * aiming at, the pattern pulling you back, then the identity you are trying
 * on. The identity is pressure tested in step three before anything is saved,
 * because an identity that has not been tested against a real situation tends
 * to collapse the first hard afternoon.
 *
 * Nothing here is prefilled. Every sentence is the boss's own writing; the
 * copy only frames the question.
 */

type Draft = {
  objective: string;
  pattern: string;
  identity: string;
  fear: string;
  doubt: string;
  alternate: string;
};

const EMPTY: Draft = {
  objective: "",
  pattern: "",
  identity: "",
  fear: "",
  doubt: "",
  alternate: "",
};

export function PCOnboarding({
  cockpit,
  onDone,
}: {
  cockpit: PCCockpit;
  onDone: (next: PCCockpit) => void;
}) {
  // Seeded from whatever already exists, so re-running onboarding after a
  // partial answer edits rather than restarts.
  const [draft, setDraft] = useState<Draft>({
    ...EMPTY,
    objective: cockpit.state.current_objective,
    pattern: cockpit.state.current_limiting_pattern,
    identity: cockpit.state.current_identity,
    fear: cockpit.state.pressure_test?.fear ?? "",
    doubt: cockpit.state.pressure_test?.doubt ?? "",
    alternate: cockpit.state.pressure_test?.alternate ?? "",
  });
  const [step, setStep] = useState(0);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const set = (k: keyof Draft) => (v: string) => setDraft((p) => ({ ...p, [k]: v }));

  const stepValid = [
    draft.objective.trim().length > 0,
    draft.pattern.trim().length > 0,
    draft.identity.trim().length > 0,
  ][step];

  async function finish() {
    setSaving(true);
    setError(null);
    try {
      const next = await writeCockpit(cockpit.pursuit.id, "identity", {
        identity: draft.identity.trim(),
        objective: draft.objective.trim(),
        pattern: draft.pattern.trim(),
        pressure_test: {
          fear: draft.fear.trim(),
          doubt: draft.doubt.trim(),
          alternate: draft.alternate.trim(),
        },
      });
      // Record the onboarding conversation itself so the history shows how the
      // cycle opened, not just where it ended up.
      const withSession = await writeCockpit(cockpit.pursuit.id, "session", {
        kind: "onboarding",
        answers: {
          objective: draft.objective.trim(),
          limiting_pattern: draft.pattern.trim(),
          identity: draft.identity.trim(),
          pressure_fear: draft.fear.trim(),
          pressure_doubt: draft.doubt.trim(),
          pressure_alternate: draft.alternate.trim(),
        },
      }).catch(() => next);
      onDone(withSession);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save that.");
      setSaving(false);
    }
  }

  return (
    <section className="min-w-0 max-w-full overflow-hidden rounded-2xl border bg-card p-4 sm:p-6">
      <div className="flex min-w-0 items-center gap-2.5">
        <span className="inline-flex size-9 shrink-0 items-center justify-center rounded-full border bg-background">
          <Compass className="size-4" aria-hidden />
        </span>
        <div className="min-w-0">
          <h2 className="truncate text-base font-semibold tracking-tight">
            Set up your first cycle
          </h2>
          <p className="text-xs text-muted-foreground">Step {step + 1} of 3</p>
        </div>
      </div>

      <ol className="mt-4 flex gap-1.5" aria-label="Onboarding progress">
        {[0, 1, 2].map((i) => (
          <li
            key={i}
            aria-current={i === step ? "step" : undefined}
            className={cn(
              "h-1 flex-1 rounded-full transition-colors",
              i <= step ? "bg-foreground" : "bg-muted",
            )}
          />
        ))}
      </ol>

      <div className="mt-5 space-y-4">
        {step === 0 ? (
          <>
            <Intro>
              Start with what you are actually aiming at. In Maltz&apos;s framing the
              mind works toward a target you can picture, so keep it concrete
              enough that you would know it happened.
            </Intro>
            <Field
              label="What is the abundance objective you want this cycle to move you toward?"
              help="Something specific enough to be true or false, not a feeling."
              value={draft.objective}
              onChange={set("objective")}
              placeholder="The outcome you are aiming at over the next 21 days."
              rows={3}
            />
          </>
        ) : null}

        {step === 1 ? (
          <>
            <Intro>
              Now the thing that pulls you back. In Maltz&apos;s language this is the
              old self image talking. Naming it plainly is what lets you rehearse
              a correction instead of being surprised by it.
            </Intro>
            <Field
              label="What is the limiting pattern you have caught yourself repeating?"
              help="The reflex or the story, in your own words. No need to explain why."
              value={draft.pattern}
              onChange={set("pattern")}
              placeholder="The pattern you have noticed getting in the way."
              rows={3}
            />
          </>
        ) : null}

        {step === 2 ? (
          <>
            <Intro>
              Last, the identity you are trying on for 21 days. Write it as
              something you could be seen doing, not a trait you claim. Then
              pressure test it, so you find where it cracks now rather than in
              the moment.
            </Intro>
            <Field
              label="Who are you practising being for the next 21 days?"
              help="Phrased as behaviour someone could watch you do. It is an experiment, not a permanent claim."
              value={draft.identity}
              onChange={set("identity")}
              placeholder="The operating identity you are trying on."
              rows={3}
            />
            <div className="rounded-xl border border-dashed p-3 sm:p-4">
              <p className="text-sm font-medium">Pressure test</p>
              <p className="mt-0.5 text-xs text-muted-foreground">
                All three are optional, and they are for you, not a score.
              </p>
              <div className="mt-3 space-y-3">
                <Field
                  label="Where would this identity crack under pressure?"
                  value={draft.fear}
                  onChange={set("fear")}
                  placeholder="The situation this week that would test it hardest."
                />
                <Field
                  label="What part of it do you not fully believe yet?"
                  value={draft.doubt}
                  onChange={set("doubt")}
                  placeholder="The honest doubt, kept as data rather than a verdict."
                />
                <Field
                  label="What identity did you almost choose instead?"
                  value={draft.alternate}
                  onChange={set("alternate")}
                  placeholder="The alternative framing, worth keeping for the cycle review."
                />
              </div>
            </div>
          </>
        ) : null}

        {error ? (
          <p className="text-sm text-danger" role="alert">
            {error}
          </p>
        ) : null}
      </div>

      <div className="mt-5 flex flex-col gap-2 sm:flex-row sm:justify-end">
        {step > 0 ? (
          <Button
            variant="ghost"
            onClick={() => setStep((s) => s - 1)}
            disabled={saving}
            className="sm:w-auto"
          >
            <ArrowLeft className="size-4" aria-hidden />
            Back
          </Button>
        ) : null}
        {step < 2 ? (
          <Button onClick={() => setStep((s) => s + 1)} disabled={!stepValid} className="sm:w-auto">
            Continue
            <ArrowRight className="size-4" aria-hidden />
          </Button>
        ) : (
          <Button onClick={() => void finish()} disabled={!stepValid || saving} className="sm:w-auto">
            {saving ? <Loader2 className="size-4 animate-spin" aria-hidden /> : null}
            {saving ? "Saving" : "Start day 1"}
          </Button>
        )}
      </div>
    </section>
  );
}

function Intro({ children }: { children: React.ReactNode }) {
  return <p className="text-sm leading-relaxed text-muted-foreground">{children}</p>;
}
