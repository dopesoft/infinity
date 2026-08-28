"use client";

import { useEffect, useState } from "react";
import { Loader2, Pencil, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PageSectionHeader } from "@/components/ui/page-tabs";
import { writeCockpit } from "@/lib/pursuits/pc/api";
import { Field } from "./CoachCard";
import { EmptyNote, PCCard } from "./PCPrimitives";
import type { PCCockpit } from "@/lib/pursuits/pc/types";

/* Identity - read by default, editable on demand.
 *
 * The identity is an experiment, so it has to be revisable mid-cycle without
 * closing the cycle. Nothing here is ever prefilled with a suggestion: an
 * identity written by the app would not carry him through a hard afternoon.
 * Blank fields are left untouched server-side, so editing one line never
 * silently blanks the others.
 */
export function IdentityPanel({
  cockpit,
  onUpdated,
}: {
  cockpit: PCCockpit;
  onUpdated: (next: PCCockpit) => void;
}) {
  const { state } = cockpit;
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [draft, setDraft] = useState({
    identity: state.current_identity,
    objective: state.current_objective,
    pattern: state.current_limiting_pattern,
    fear: state.pressure_test?.fear ?? "",
    doubt: state.pressure_test?.doubt ?? "",
    alternate: state.pressure_test?.alternate ?? "",
  });

  // Re-seed the draft whenever the saved state moves (another surface wrote,
  // or a coaching chat did), so the editor never opens on a stale copy.
  useEffect(() => {
    setDraft({
      identity: state.current_identity,
      objective: state.current_objective,
      pattern: state.current_limiting_pattern,
      fear: state.pressure_test?.fear ?? "",
      doubt: state.pressure_test?.doubt ?? "",
      alternate: state.pressure_test?.alternate ?? "",
    });
  }, [
    state.current_identity,
    state.current_objective,
    state.current_limiting_pattern,
    state.pressure_test?.fear,
    state.pressure_test?.doubt,
    state.pressure_test?.alternate,
  ]);

  const set = (k: keyof typeof draft) => (v: string) => setDraft((p) => ({ ...p, [k]: v }));

  async function save() {
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
      setEditing(false);
      onUpdated(next);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save that.");
    } finally {
      setSaving(false);
    }
  }

  if (editing) {
    return (
      <PCCard>
        <PageSectionHeader title="editing identity">
          <Button variant="ghost" size="sm" onClick={() => setEditing(false)} disabled={saving}>
            <X className="size-4" aria-hidden />
            Cancel
          </Button>
        </PageSectionHeader>

        <div className="mt-4 space-y-4">
          <Field
            label="Operating identity"
            help="Phrased as behaviour someone could watch you do."
            value={draft.identity}
            onChange={set("identity")}
            rows={3}
          />
          <Field
            label="Abundance objective"
            help="Specific enough to be true or false."
            value={draft.objective}
            onChange={set("objective")}
            rows={3}
          />
          <Field
            label="Limiting pattern"
            help="The reflex or story you keep catching."
            value={draft.pattern}
            onChange={set("pattern")}
            rows={2}
          />

          <div className="rounded-xl border border-dashed p-3 sm:p-4">
            <p className="text-sm font-medium">Pressure test</p>
            <p className="mt-0.5 text-xs text-muted-foreground">
              Where the identity would crack, what you doubt, and the version you almost chose.
            </p>
            <div className="mt-3 space-y-3">
              <Field label="Where it cracks" value={draft.fear} onChange={set("fear")} />
              <Field label="The doubt" value={draft.doubt} onChange={set("doubt")} />
              <Field label="The alternative" value={draft.alternate} onChange={set("alternate")} />
            </div>
          </div>

          {error ? (
            <p className="text-sm text-danger" role="alert">
              {error}
            </p>
          ) : null}

          <Button onClick={() => void save()} disabled={saving} className="w-full sm:w-auto">
            {saving ? <Loader2 className="size-4 animate-spin" aria-hidden /> : null}
            {saving ? "Saving" : "Save identity"}
          </Button>
        </div>
      </PCCard>
    );
  }

  const pressure = [
    { label: "Where it cracks", value: state.pressure_test?.fear ?? "" },
    { label: "The doubt", value: state.pressure_test?.doubt ?? "" },
    { label: "The alternative", value: state.pressure_test?.alternate ?? "" },
  ].filter((p) => p.value.trim().length > 0);

  return (
    <div className="space-y-4">
      <PCCard>
        <PageSectionHeader title="identity">
          <Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
            <Pencil className="size-4" aria-hidden />
            Edit
          </Button>
        </PageSectionHeader>

        <div className="mt-3 space-y-4">
          <ReadField label="Operating identity" value={state.current_identity} />
          <ReadField label="Abundance objective" value={state.current_objective} />
          <ReadField label="Limiting pattern" value={state.current_limiting_pattern} />
        </div>
      </PCCard>

      <PCCard>
        <PageSectionHeader title="pressure test" />
        {pressure.length === 0 ? (
          <EmptyNote className="mt-3">
            The identity has not been pressure tested yet. Edit above to name where it would crack,
            what you do not fully believe, and the version you almost chose instead.
          </EmptyNote>
        ) : (
          <dl className="mt-3 space-y-3">
            {pressure.map((p) => (
              <div key={p.label} className="min-w-0">
                <dt className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
                  {p.label}
                </dt>
                <dd className="mt-0.5 whitespace-pre-wrap break-words text-sm text-foreground">
                  {p.value}
                </dd>
              </div>
            ))}
          </dl>
        )}
      </PCCard>
    </div>
  );
}

function ReadField({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <p className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
        {label}
      </p>
      {value.trim() ? (
        <p className="mt-1 whitespace-pre-wrap break-words text-sm text-foreground">{value}</p>
      ) : (
        <EmptyNote className="mt-1">Not set yet.</EmptyNote>
      )}
    </div>
  );
}
