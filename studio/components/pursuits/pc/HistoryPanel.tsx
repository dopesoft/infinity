"use client";

import { PageSectionHeader } from "@/components/ui/page-tabs";
import { EmptyNote, PCCard } from "./PCPrimitives";
import { EvidenceRow } from "./TodayPanel";
import type { PCCockpit, PCSession } from "@/lib/pursuits/pc/types";

/* Patterns and history - the record the coach reasons over.
 *
 * Read-only on purpose. Everything here was written somewhere else (a session,
 * a capture, a review), so offering an edit here would create a second way to
 * say the same thing and a second place for the two to disagree.
 */

/** The answer keys worth surfacing, in the order the day produced them.
 *  Anything else in the blob is deliberately not rendered rather than dumped
 *  raw: a session card should read like a day, not like JSON. */
const ANSWER_LABELS: Array<{ key: string; label: string }> = [
  { key: "rehearsal", label: "Rehearsed" },
  { key: "proof_pledge", label: "Pledged" },
  { key: "evidence", label: "Evidence" },
  { key: "resistance", label: "Resistance" },
  { key: "fact", label: "Fact" },
  { key: "interpretation", label: "Interpretation" },
  { key: "lesson", label: "Lesson" },
  { key: "correction", label: "Correction" },
  { key: "reason", label: "What pulled you away" },
  { key: "smallest_next_step", label: "Smallest next step" },
  { key: "objective", label: "Objective" },
  { key: "limiting_pattern", label: "Limiting pattern" },
  { key: "identity", label: "Identity" },
];

export function HistoryPanel({ cockpit }: { cockpit: PCCockpit }) {
  const earlierEvidence = cockpit.recent_evidence.filter(
    (e) => !cockpit.today_evidence.some((t) => t.id === e.id),
  );
  const pledged = cockpit.recent_proofs.length;
  const taken = cockpit.recent_proofs.filter((p) => p.taken).length;

  return (
    <div className="space-y-4">
      <PCCard>
        <PageSectionHeader title="proof actions" />
        {pledged === 0 ? (
          <EmptyNote className="mt-3">No proof actions pledged yet.</EmptyNote>
        ) : (
          <>
            <p className="mt-3 text-sm text-foreground">
              <span className="font-mono text-lg font-semibold">
                {taken}/{pledged}
              </span>{" "}
              <span className="text-muted-foreground">taken across this programme.</span>
            </p>
            <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
              If the ratio is slipping, the action is too big rather than the identity being wrong.
            </p>
          </>
        )}
      </PCCard>

      <PCCard>
        <PageSectionHeader title="corrections" count={cockpit.corrections.length} />
        {cockpit.corrections.length === 0 ? (
          <EmptyNote className="mt-3">
            No corrections logged yet. They come out of the evening question.
          </EmptyNote>
        ) : (
          <ul className="mt-3 space-y-2">
            {cockpit.corrections.map((p) => (
              <li key={p.id} className="min-w-0 break-words text-sm text-foreground">
                <span className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
                  day {p.day_in_cycle}
                </span>{" "}
                {p.body}
              </li>
            ))}
          </ul>
        )}
      </PCCard>

      <PCCard>
        <PageSectionHeader title="patterns" count={cockpit.patterns.length} />
        {cockpit.patterns.length === 0 ? (
          <EmptyNote className="mt-3">Nothing logged yet.</EmptyNote>
        ) : (
          <ul className="mt-3 space-y-2">
            {cockpit.patterns.map((p) => (
              <li key={p.id} className="min-w-0 break-words text-sm text-foreground">
                <span className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
                  {p.kind}
                </span>{" "}
                {p.body}
              </li>
            ))}
          </ul>
        )}
      </PCCard>

      <PCCard>
        <PageSectionHeader title="earlier captures" count={earlierEvidence.length} />
        {earlierEvidence.length === 0 ? (
          <EmptyNote className="mt-3">Nothing captured on earlier days yet.</EmptyNote>
        ) : (
          <ul className="mt-3 space-y-1.5">
            {earlierEvidence.map((e) => (
              <EvidenceRow key={e.id} evidence={e} />
            ))}
          </ul>
        )}
      </PCCard>

      <PCCard>
        <PageSectionHeader title="sessions" count={cockpit.recent_sessions.length} />
        {cockpit.recent_sessions.length === 0 ? (
          <EmptyNote className="mt-3">No sessions logged yet.</EmptyNote>
        ) : (
          <ul className="mt-3 space-y-3">
            {cockpit.recent_sessions.map((s) => (
              <SessionRow key={s.id} session={s} />
            ))}
          </ul>
        )}
      </PCCard>

      {cockpit.cycle_reviews.length > 0 ? (
        <PCCard>
          <PageSectionHeader title="cycle reviews" count={cockpit.cycle_reviews.length} />
          <ul className="mt-3 space-y-3">
            {cockpit.cycle_reviews.map((r) => (
              <li key={r.id} className="min-w-0 border-b border-dashed pb-3 last:border-0 last:pb-0">
                <p className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
                  cycle {r.cycle_number}
                </p>
                {r.wins.trim() ? (
                  <p className="mt-1 whitespace-pre-wrap break-words text-sm text-foreground">
                    <span className="text-muted-foreground">Wins: </span>
                    {r.wins}
                  </p>
                ) : null}
                {r.misses.trim() ? (
                  <p className="mt-1 whitespace-pre-wrap break-words text-sm text-foreground">
                    <span className="text-muted-foreground">Misses: </span>
                    {r.misses}
                  </p>
                ) : null}
              </li>
            ))}
          </ul>
        </PCCard>
      ) : null}
    </div>
  );
}

function SessionRow({ session }: { session: PCSession }) {
  const lines = ANSWER_LABELS.map(({ key, label }) => {
    const raw = session.answers?.[key];
    const value = typeof raw === "string" ? raw.trim() : "";
    return value ? { label, value } : null;
  }).filter((l): l is { label: string; value: string } => l !== null);

  return (
    <li className="min-w-0 border-b border-dashed pb-3 last:border-0 last:pb-0">
      <p className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
        {session.kind} · day {session.day_in_cycle} · cycle {session.cycle_number}
      </p>
      {lines.length === 0 ? (
        <EmptyNote className="mt-1">No answers recorded.</EmptyNote>
      ) : (
        <dl className="mt-1 space-y-1">
          {lines.map((l) => (
            <div key={l.label} className="min-w-0">
              <dt className="inline text-sm text-muted-foreground">{l.label}: </dt>
              <dd className="inline whitespace-pre-wrap break-words text-sm text-foreground">
                {l.value}
              </dd>
            </div>
          ))}
        </dl>
      )}
    </li>
  );
}
