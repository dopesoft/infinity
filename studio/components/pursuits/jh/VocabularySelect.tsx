"use client";

import { useState } from "react";
import { NativeSelect } from "@/components/ui/native-select";
import { Spinner } from "@/components/ui/spinner";
import { cn } from "@/lib/utils";

/* The one control that moves a Job Hunt value along its ladder.
 *
 * Three ladders exist on this board — a role's stage, a contact's outreach, a
 * document's approval — and they are the same interaction three times: pick a
 * value the server said was legal, send it, adopt the board that comes back.
 * Written once so the three cannot drift into three heights, three spinners
 * and three different ways of reporting a refusal.
 *
 * The options are always the server's own vocabulary, never a list held here,
 * so a value the store would reject can never be offered.
 *
 * It never patches anything locally: `onSelect` writes and the caller adopts
 * the refreshed cockpit, so a failed write leaves the control showing the
 * value that is actually stored rather than the one that was clicked.
 */
export function VocabularySelect({
  value,
  options,
  labelFor,
  ariaLabel,
  onSelect,
  className,
}: {
  value: string;
  options: string[];
  labelFor: (value: string) => string;
  ariaLabel: string;
  onSelect: (next: string) => Promise<void>;
  className?: string;
}) {
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function choose(next: string) {
    if (next === value || saving) return;
    setSaving(true);
    setError(null);
    try {
      await onSelect(next);
    } catch (e) {
      setError(e instanceof Error ? e.message : "something went wrong at my end.");
    } finally {
      setSaving(false);
    }
  }

  // A value the vocabulary no longer carries would otherwise vanish from the
  // control and read as if the row had silently changed. Show it, so what is
  // stored is always what is displayed.
  const choices = options.includes(value) ? options : [value, ...options];

  return (
    <div className={cn("flex min-w-0 max-w-full flex-col gap-1.5", className)}>
      <div className="flex min-w-0 items-center gap-2">
        <NativeSelect
          value={value}
          onValueChange={(next) => void choose(next)}
          aria-label={ariaLabel}
          disabled={saving}
          className="min-w-0 flex-1"
        >
          {choices.map((option) => (
            <option key={option} value={option}>
              {labelFor(option)}
            </option>
          ))}
        </NativeSelect>
        {saving ? <Spinner className="size-4 shrink-0 text-quiet" /> : null}
      </div>
      {error ? (
        <p className="font-voice text-[13.5px] leading-[1.55] text-danger">
          I could not save that. {error}
        </p>
      ) : null}
    </div>
  );
}
