"use client";

import { useEffect, useMemo, useState } from "react";
import { Loader2, Sunrise, Sun, Moon, LifeBuoy, RefreshCw, Check } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import { writeCockpit } from "@/lib/pursuits/pc/api";
import type { PCCockpit } from "@/lib/pursuits/pc/types";

/* The coach card - the action-first hero of the cockpit.
 *
 * The whole screen is built around this: the server's deterministic coach
 * decides which phase is due and hands back a headline, a body, and the
 * questions to ask. This component renders whatever it is given, so a new
 * coaching phase added in Go appears here with no change to this file.
 *
 * The one place phase matters on the client is where the answers land: a
 * review closes the cycle, everything else logs a session. Every derived side
 * effect (a morning pledge becoming a tracked proof, a midday capture becoming
 * evidence and resistance rows, an evening correction filing to the pattern
 * history) is enforced inside pc.Store.Apply and is deliberately NOT duplicated
 * here. Re-implementing any of them client-side is how the cockpit and a
 * coaching chat would start disagreeing about the same day.
 */

/** Which answer key the phase's primary question writes into. Mirrors the
 *  keys core/internal/pursuits/pc/coach.go asks for. */
const PRIMARY_KEY: Record<string, string> = {
  morning: "rehearsal",
  midday: "evidence",
  evening: "fact",
  recovery: "reason",
  review: "wins",
  onboarding: "objective",
  adjustment: "smaller_proof",
};

const PHASE_ICON: Record<string, typeof Sunrise> = {
  morning: Sunrise,
  midday: Sun,
  evening: Moon,
  recovery: LifeBuoy,
  review: RefreshCw,
  idle: Check,
};

export function CoachCard({
  cockpit,
  onUpdated,
}: {
  cockpit: PCCockpit;
  onUpdated: (next: PCCockpit) => void;
}) {
  const { guidance, pursuit } = cockpit;
  const primaryKey = PRIMARY_KEY[guidance.phase] ?? "answer";

  const fields = useMemo(
    () => [
      { key: primaryKey, label: "", placeholder: "", help: "", primary: true },
      ...(guidance.secondary_prompts ?? []).map((p) => ({
        key: p.key,
        label: p.label,
        placeholder: p.placeholder,
        help: p.help ?? "",
        primary: false,
      })),
    ],
    [guidance.secondary_prompts, primaryKey],
  );

  const [values, setValues] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Reset the form whenever the coach moves to a different phase, so answers
  // from the morning never leak into the evening's inputs.
  useEffect(() => {
    setValues({});
    setError(null);
  }, [guidance.phase, cockpit.state.current_day, cockpit.state.cycle_number]);

  const primaryValue = (values[primaryKey] ?? "").trim();
  const canSubmit = primaryValue.length > 0 && !saving;
  const Icon = PHASE_ICON[guidance.phase] ?? Sunrise;

  // Nothing is due. Show the coach's closing note without a form so the
  // cockpit reads "today is complete" rather than an empty prompt.
  if (guidance.phase === "idle") {
    return (
      <section className="min-w-0 max-w-full overflow-hidden rounded-2xl border bg-card p-4 sm:p-5">
        <Header Icon={Icon} headline={guidance.headline} hints={guidance.hints} />
        <p className="mt-3 text-sm leading-relaxed text-muted-foreground">{guidance.body}</p>
      </section>
    );
  }

  async function submit() {
    if (!canSubmit) return;
    setSaving(true);
    setError(null);
    try {
      const answers: Record<string, string> = {};
      for (const f of fields) {
        const v = (values[f.key] ?? "").trim();
        if (v) answers[f.key] = v;
      }

      let next: PCCockpit;
      if (guidance.phase === "review") {
        next = await writeCockpit(pursuit.id, "review", {
          wins: answers.wins ?? "",
          misses: answers.misses ?? "",
          next_identity: answers.next_identity ?? "",
          next_objective: answers.next_objective ?? "",
          next_pattern: answers.next_pattern ?? "",
        });
      } else {
        next = await writeCockpit(pursuit.id, "session", {
          kind: guidance.phase,
          answers,
        });
      }
      setValues({});
      onUpdated(next);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save that.");
    } finally {
      setSaving(false);
    }
  }

  return (
    <section className="min-w-0 max-w-full overflow-hidden rounded-2xl border bg-card p-4 sm:p-5">
      <Header Icon={Icon} headline={guidance.headline} hints={guidance.hints} />

      <p className="mt-3 text-sm leading-relaxed text-muted-foreground">{guidance.body}</p>

      <form
        className="mt-4 space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <Field
          label={guidance.prompt}
          value={values[primaryKey] ?? ""}
          onChange={(v) => setValues((p) => ({ ...p, [primaryKey]: v }))}
          placeholder="Your answer, in your own words."
          rows={3}
        />

        {(guidance.secondary_prompts ?? []).map((p) => (
          <Field
            key={p.key}
            label={p.label}
            help={p.help}
            value={values[p.key] ?? ""}
            onChange={(v) => setValues((prev) => ({ ...prev, [p.key]: v }))}
            placeholder={p.placeholder}
            rows={2}
          />
        ))}

        {error ? (
          <p className="text-sm text-danger" role="alert">
            {error}
          </p>
        ) : null}

        <Button type="submit" disabled={!canSubmit} className="w-full sm:w-auto">
          {saving ? <Loader2 className="size-4 animate-spin" aria-hidden /> : null}
          {saving ? "Saving" : guidance.headline}
        </Button>
      </form>
    </section>
  );
}

function Header({
  Icon,
  headline,
  hints,
}: {
  Icon: typeof Sunrise;
  headline: string;
  hints: string[];
}) {
  return (
    <div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex min-w-0 items-center gap-2.5">
        <span className="inline-flex size-9 shrink-0 items-center justify-center rounded-full border bg-background">
          <Icon className="size-4" aria-hidden />
        </span>
        <h2 className="min-w-0 truncate text-base font-semibold tracking-tight">{headline}</h2>
      </div>
      {hints.length > 0 ? (
        <ul className="flex flex-wrap gap-1.5">
          {hints.map((h) => (
            <li
              key={h}
              className="rounded-full border px-2 py-0.5 font-mono text-[10px] uppercase tracking-wider text-muted-foreground"
            >
              {h}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

export function Field({
  label,
  help,
  value,
  onChange,
  placeholder,
  rows = 2,
  className,
}: {
  label: string;
  help?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  rows?: number;
  className?: string;
}) {
  return (
    <label className={cn("block min-w-0", className)}>
      <span className="block text-sm font-medium text-foreground">{label}</span>
      {help ? <span className="mt-0.5 block text-xs text-muted-foreground">{help}</span> : null}
      <Textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        rows={rows}
        inputMode="text"
        className="mt-1.5 text-base sm:text-sm"
      />
    </label>
  );
}
