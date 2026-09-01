"use client";

import { useEffect, useState } from "react";
import { Check, Pencil, Plus, Sparkles, X } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Inset } from "@/components/ui/inset";
import { GroupLabel, ListRow, StatusDot, type RowTone } from "@/components/ui/list-row";
import { MetricRow } from "@/components/ui/metric-row";
import { Textarea } from "@/components/ui/textarea";
import { Chip, ChipGroup } from "@/components/ui/chip";
import { cn } from "@/lib/utils";
import { writeCockpit } from "@/lib/pursuits/pc/api";
import type {
  PCCockpit,
  PCEvidence,
  PCPattern,
  PCProof,
  PCSession,
} from "@/lib/pursuits/pc/types";

/* The programme: everything the coaching session is built on, one tap away.
 *
 * This is the secondary surface, so it is a ledger, not a dashboard. Groups are
 * separated by a mono label and a hairline (Majordomo §5), never by a card, and
 * nothing here duplicates a decision the conversation makes: which memory gets
 * rehearsed today and which phase is due are the server's calls, shown here as
 * facts rather than offered as controls.
 *
 * Every write goes through the same `writeCockpit` chokepoint the conversation
 * uses, and each one returns the refreshed cockpit, so the two surfaces cannot
 * drift apart mid-session.
 */

/** The answer keys worth surfacing in history, in the order a day produces
 *  them. Anything else in the blob is deliberately not rendered: a session
 *  should read like a day, not like JSON. */
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

export function ProgrammePanel({
  title,
  cockpit,
  onUpdated,
}: {
  title: string;
  cockpit: PCCockpit;
  onUpdated: (next: PCCockpit) => void;
}) {
  return (
    <div className="min-w-0 max-w-full px-4 pb-8 pt-2 sm:px-5">
      <h2 className="pt-2 font-voice text-[18px] font-medium tracking-tight text-foreground">
        {title}
      </h2>
      <IdentityGroup cockpit={cockpit} onUpdated={onUpdated} />
      <ProgressGroup cockpit={cockpit} />
      <ProofsGroup cockpit={cockpit} onUpdated={onUpdated} />
      <CaptureGroup cockpit={cockpit} onUpdated={onUpdated} />
      <MemoriesGroup cockpit={cockpit} onUpdated={onUpdated} />
      <PatternsGroup cockpit={cockpit} />
      <HistoryGroup cockpit={cockpit} />
    </div>
  );
}

/* ── Identity ──────────────────────────────────────────────────────────── */

function IdentityGroup({
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
  const [draft, setDraft] = useState(() => draftFrom(cockpit));

  // Re-seed whenever the saved state moves, so the editor never opens stale
  // after the conversation (or a coaching chat) wrote something.
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

  const set = (k: keyof ReturnType<typeof draftFrom>) => (v: string) =>
    setDraft((p) => ({ ...p, [k]: v }));

  async function save() {
    setSaving(true);
    setError(null);
    try {
      onUpdated(
        await writeCockpit(cockpit.pursuit.id, "identity", {
          identity: draft.identity.trim(),
          objective: draft.objective.trim(),
          pattern: draft.pattern.trim(),
          pressure_test: {
            fear: draft.fear.trim(),
            doubt: draft.doubt.trim(),
            alternate: draft.alternate.trim(),
          },
        }),
      );
      setEditing(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save that.");
    } finally {
      setSaving(false);
    }
  }

  if (editing) {
    return (
      <section className="min-w-0">
        <GroupLabel
          label="identity"
          trailing={
            <Button variant="ghost" size="sm" onClick={() => setEditing(false)} disabled={saving}>
              <X className="size-4" aria-hidden />
              Cancel
            </Button>
          }
        />
        <div className="space-y-4 pb-2 pt-1">
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
          <Field label="Where it cracks" value={draft.fear} onChange={set("fear")} />
          <Field label="The doubt" value={draft.doubt} onChange={set("doubt")} />
          <Field label="The alternative" value={draft.alternate} onChange={set("alternate")} />

          {error ? (
            <p className="text-[13.5px] text-danger" role="alert">
              {error}
            </p>
          ) : null}

          <Button onClick={() => void save()} disabled={saving} className="w-full sm:w-auto">
            {saving ? <Spinner className="size-4" aria-hidden /> : null}
            {saving ? "Saving" : "Save identity"}
          </Button>
        </div>
      </section>
    );
  }

  const pressure = [
    { label: "Where it cracks", value: state.pressure_test?.fear ?? "" },
    { label: "The doubt", value: state.pressure_test?.doubt ?? "" },
    { label: "The alternative", value: state.pressure_test?.alternate ?? "" },
  ].filter((p) => p.value.trim().length > 0);

  return (
    <section className="min-w-0">
      <GroupLabel
        label="identity"
        trailing={
          <Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
            <Pencil className="size-4" aria-hidden />
            Edit
          </Button>
        }
      />
      <ReadField label="Operating identity" value={state.current_identity} />
      <ReadField label="Abundance objective" value={state.current_objective} />
      <ReadField label="Limiting pattern" value={state.current_limiting_pattern} />
      {pressure.length > 0 ? (
        <div className="pt-3">
          <KeyValues items={pressure} />
        </div>
      ) : (
        <Quiet className="pt-2">
          Not pressure tested yet. Edit above to name where it would crack.
        </Quiet>
      )}
    </section>
  );
}

function draftFrom(cockpit: PCCockpit) {
  const { state } = cockpit;
  return {
    identity: state.current_identity,
    objective: state.current_objective,
    pattern: state.current_limiting_pattern,
    fear: state.pressure_test?.fear ?? "",
    doubt: state.pressure_test?.doubt ?? "",
    alternate: state.pressure_test?.alternate ?? "",
  };
}

/* ── Progress ──────────────────────────────────────────────────────────── */

function ProgressGroup({ cockpit }: { cockpit: PCCockpit }) {
  const { state } = cockpit;
  const pledged = cockpit.recent_proofs.length;
  const taken = cockpit.recent_proofs.filter((p) => p.taken).length;

  return (
    <section className="min-w-0">
      <GroupLabel label="progress" />
      <MetricRow label="Day" value={state.current_day} meta={`of ${state.cycle_length_days}`} />
      <MetricRow label="Cycle" value={state.cycle_number} />
      <MetricRow
        label="Proof actions taken"
        value={`${taken}/${pledged}`}
        tone={pledged > 0 && taken * 2 >= pledged ? "brand" : "default"}
      />
      <MetricRow label="Captured today" value={cockpit.today_evidence.length} />
      <MetricRow
        label="Days missed this cycle"
        value={state.missed_days_count}
        tone={state.missed_days_count > 0 ? "warning" : "quiet"}
      />
      {pledged > 0 && taken * 2 < pledged ? (
        <Quiet className="pt-2">
          If the ratio is slipping, the action is too big rather than the identity being
          wrong.
        </Quiet>
      ) : null}
    </section>
  );
}

/* ── Proofs ────────────────────────────────────────────────────────────── */

function ProofsGroup({
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

  return (
    <section className="min-w-0">
      <GroupLabel label="today's proof" count={cockpit.today_proofs.length} />
      {cockpit.today_proofs.length === 0 ? (
        <Quiet>
          Nothing pledged yet. One deliberate action that only makes sense if the identity is
          true, small enough that you are certain to do it.
        </Quiet>
      ) : (
        cockpit.today_proofs.map((p) => (
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
        ))
      )}

      <InlineForm
        value={label}
        onChange={setLabel}
        onSubmit={() =>
          void run("add", async () => {
            const next = await writeCockpit(cockpit.pursuit.id, "proof", {
              label: label.trim(),
            });
            setLabel("");
            return next;
          })
        }
        placeholder="Pledge another proof action for today"
        ariaLabel="New proof action"
        submitLabel="Pledge"
        icon={<Plus className="size-4" aria-hidden />}
        busy={busy === "add"}
      />
      {error ? <ErrorLine>{error}</ErrorLine> : null}
    </section>
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
    <ListRow
      leadingAction={
        <button
          type="button"
          onClick={onToggle}
          disabled={busy}
          aria-label={proof.taken ? "Mark proof not taken" : "Mark proof taken"}
          aria-pressed={proof.taken}
          className={cn(
            "inline-flex size-7 shrink-0 items-center justify-center rounded-full border transition-colors",
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
            proof.taken
              ? "border-success bg-success text-success-foreground"
              : "border-border bg-background hover:border-foreground/40",
          )}
        >
          {busy ? (
            <Spinner className="size-3.5" aria-hidden />
          ) : proof.taken ? (
            <Check className="size-3.5" aria-hidden />
          ) : null}
        </button>
      }
      title={
        <span className={cn(proof.taken && "text-quiet line-through")}>{proof.label}</span>
      }
      meta={proof.note.trim() || undefined}
      chevron={false}
    />
  );
}

/* ── Capture ───────────────────────────────────────────────────────────── */

function CaptureGroup({
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
    <section className="min-w-0">
      <GroupLabel
        label="captures today"
        count={cockpit.today_evidence.length}
        trailing={
          <ChipGroup role="group" aria-label="Capture kind">
            <KindToggle active={kind === "evidence"} onClick={() => setKind("evidence")}>
              evidence
            </KindToggle>
            <KindToggle active={kind === "resistance"} onClick={() => setKind("resistance")}>
              resistance
            </KindToggle>
          </ChipGroup>
        }
      />
      <Quiet>
        {kind === "evidence"
          ? "A moment the identity held, however small."
          : "A moment the old pattern ran. Data, not a score."}
      </Quiet>
      <InlineForm
        value={body}
        onChange={setBody}
        onSubmit={() => void save()}
        placeholder={kind === "evidence" ? "What happened?" : "Where did it show up?"}
        ariaLabel={`Capture ${kind}`}
        submitLabel="Capture"
        busy={saving}
      />
      {error ? <ErrorLine>{error}</ErrorLine> : null}
      {cockpit.today_evidence.length === 0 ? (
        <Quiet>Nothing captured yet today.</Quiet>
      ) : (
        cockpit.today_evidence.map((e) => <EvidenceRow key={e.id} evidence={e} />)
      )}
    </section>
  );
}

function EvidenceRow({ evidence }: { evidence: PCEvidence }) {
  return (
    <QuoteRow
      tone={evidence.kind === "resistance" ? "warning" : "success"}
      meta={`${evidence.kind} · day ${evidence.day_in_cycle}`}
      body={evidence.body}
    />
  );
}

/* A ledger row for the boss's OWN words.
 *
 * ListRow truncates by contract, which is what keeps a 375px viewport from
 * scrolling sideways and is exactly right for a filename or a session title.
 * It is wrong for a correction he wrote himself: a one-line clamp silently
 * eats the half of the sentence that mattered. So prose gets its own row
 * shape, still hairline-separated with no container (Majordomo §1.2), that
 * wraps instead of clipping. */
function QuoteRow({
  meta,
  title,
  body,
  tone,
  trailing,
}: {
  meta?: string;
  title?: string;
  body?: string;
  tone?: RowTone;
  trailing?: React.ReactNode;
}) {
  return (
    <div className="min-w-0 max-w-full border-b border-hairline py-2.5 last:border-b-0">
      {meta || trailing ? (
        <div className="flex min-w-0 items-center gap-2">
          {tone ? <StatusDot tone={tone} /> : null}
          {meta ? (
            <span className="min-w-0 truncate font-mono text-[11px] uppercase tracking-[0.08em] text-quiet">
              {meta}
            </span>
          ) : null}
          {trailing ? <span className="ml-auto shrink-0">{trailing}</span> : null}
        </div>
      ) : null}
      {title ? (
        <p className="mt-1 min-w-0 whitespace-pre-wrap break-words font-sans text-[13.5px] font-medium text-foreground">
          {title}
        </p>
      ) : null}
      {body?.trim() ? (
        <p className="mt-1 min-w-0 whitespace-pre-wrap break-words font-voice text-[15.5px] leading-[1.55] text-foreground">
          {body}
        </p>
      ) : null}
    </div>
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
    <Chip
      mono
      raised={active}
      aria-pressed={active}
      onClick={onClick}
      className="uppercase tracking-wider"
    >
      {children}
    </Chip>
  );
}

/* ── Memories ──────────────────────────────────────────────────────────── */

function MemoriesGroup({
  cockpit,
  onUpdated,
}: {
  cockpit: PCCockpit;
  onUpdated: (next: PCCockpit) => void;
}) {
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const todaysId = cockpit.rehearsal_memory?.id;

  async function save() {
    const t = title.trim();
    if (!t) return;
    setSaving(true);
    setError(null);
    try {
      const next = await writeCockpit(cockpit.pursuit.id, "memory", {
        title: t,
        body: body.trim(),
      });
      setTitle("");
      setBody("");
      onUpdated(next);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save that.");
    } finally {
      setSaving(false);
    }
  }

  return (
    <section className="min-w-0">
      <GroupLabel label="banked memories" count={cockpit.memories.length} />
      {cockpit.memories.length === 0 ? (
        <Quiet>
          Nothing banked yet. The first one usually turns up the moment a proof action lands
          better than expected.
        </Quiet>
      ) : (
        cockpit.memories.map((m) => (
          <QuoteRow
            key={m.id}
            tone={m.id === todaysId ? "brand" : undefined}
            meta={m.id === todaysId ? "today's rehearsal" : undefined}
            trailing={
              m.id === todaysId ? (
                <Sparkles className="size-4 text-brand" aria-hidden />
              ) : undefined
            }
            title={m.title}
            body={m.body}
          />
        ))
      )}

      <form
        className="space-y-2 pt-3"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <Input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Something that genuinely worked, in a few words"
          inputMode="text"
          aria-label="New banked memory"
          className="min-w-0"
        />
        <Field
          label="What it was actually like"
          help="Optional. What you saw, heard and felt at the time is what the rehearsal has to work with."
          value={body}
          onChange={setBody}
          rows={2}
        />
        <Button
          type="submit"
          variant="secondary"
          disabled={!title.trim() || saving}
          className="w-full sm:w-auto"
        >
          {saving ? (
            <Spinner className="size-4" aria-hidden />
          ) : (
            <Plus className="size-4" aria-hidden />
          )}
          Bank it
        </Button>
      </form>
      {error ? <ErrorLine>{error}</ErrorLine> : null}
    </section>
  );
}

/* ── Patterns and history ──────────────────────────────────────────────── */

function PatternsGroup({ cockpit }: { cockpit: PCCockpit }) {
  return (
    <section className="min-w-0">
      <GroupLabel label="corrections" count={cockpit.corrections.length} />
      {cockpit.corrections.length === 0 ? (
        <Quiet>No corrections logged yet. They come out of the evening question.</Quiet>
      ) : (
        cockpit.corrections.map((p) => <PatternRow key={p.id} pattern={p} />)
      )}

      <GroupLabel label="patterns" count={cockpit.patterns.length} />
      {cockpit.patterns.length === 0 ? (
        <Quiet>Nothing logged yet.</Quiet>
      ) : (
        cockpit.patterns.map((p) => <PatternRow key={p.id} pattern={p} showKind />)
      )}
    </section>
  );
}

function PatternRow({ pattern, showKind }: { pattern: PCPattern; showKind?: boolean }) {
  return (
    <QuoteRow
      meta={
        showKind
          ? `${pattern.kind} · day ${pattern.day_in_cycle}`
          : `day ${pattern.day_in_cycle}`
      }
      body={pattern.body}
    />
  );
}

function HistoryGroup({ cockpit }: { cockpit: PCCockpit }) {
  const [openId, setOpenId] = useState<string | null>(null);
  const earlier = cockpit.recent_evidence.filter(
    (e) => !cockpit.today_evidence.some((t) => t.id === e.id),
  );

  return (
    <section className="min-w-0">
      <GroupLabel label="sessions" count={cockpit.recent_sessions.length} />
      {cockpit.recent_sessions.length === 0 ? (
        <Quiet>No sessions logged yet.</Quiet>
      ) : (
        cockpit.recent_sessions.map((s) => (
          <ListRow
            key={s.id}
            title={sessionTitle(s)}
            meta={`day ${s.day_in_cycle} · cycle ${s.cycle_number}`}
            onClick={() => setOpenId((id) => (id === s.id ? null : s.id))}
          >
            {openId === s.id ? <SessionDetail session={s} /> : null}
          </ListRow>
        ))
      )}

      <GroupLabel label="earlier captures" count={earlier.length} />
      {earlier.length === 0 ? (
        <Quiet>Nothing captured on earlier days yet.</Quiet>
      ) : (
        earlier.map((e) => <EvidenceRow key={e.id} evidence={e} />)
      )}

      {cockpit.cycle_reviews.length > 0 ? (
        <>
          <GroupLabel label="cycle reviews" count={cockpit.cycle_reviews.length} />
          {cockpit.cycle_reviews.map((r) => (
            <QuoteRow
              key={r.id}
              meta={`cycle ${r.cycle_number}`}
              title={r.misses.trim() ? `Missed: ${r.misses}` : undefined}
              body={r.wins.trim() || "Cycle closed."}
            />
          ))}
        </>
      ) : null}
    </section>
  );
}

function sessionTitle(session: PCSession): string {
  for (const { key } of ANSWER_LABELS) {
    const raw = session.answers?.[key];
    if (typeof raw === "string" && raw.trim()) return raw.trim();
  }
  return session.kind;
}

function SessionDetail({ session }: { session: PCSession }) {
  const items = ANSWER_LABELS.map(({ key, label }) => {
    const raw = session.answers?.[key];
    const value = typeof raw === "string" ? raw.trim() : "";
    return value ? { label, value } : null;
  }).filter((l): l is { label: string; value: string } => l !== null);

  if (items.length === 0) return <Inset variant="plain" text="No answers recorded." />;
  return <KeyValues items={items} />;
}

/* A labelled block of the boss's own answers.
 *
 * `Inset variant="kv"` puts the label in a fixed 92px column, which truncates
 * to "WHERE IT CR…" at the width of this panel. Stacking the label above the
 * value keeps the same one-level-of-tone inset while letting both wrap. */
function KeyValues({ items }: { items: Array<{ label: string; value: string }> }) {
  return (
    <dl className="min-w-0 max-w-full space-y-2.5 rounded-[10px] bg-muted px-3 py-2.5">
      {items.map((item) => (
        <div key={item.label} className="min-w-0">
          <dt className="font-mono text-[11px] uppercase tracking-[0.08em] text-quiet">
            {item.label}
          </dt>
          <dd className="mt-0.5 min-w-0 whitespace-pre-wrap break-words font-voice text-[15.5px] leading-[1.5] text-foreground">
            {item.value}
          </dd>
        </div>
      ))}
    </dl>
  );
}

/* ── Shared bits ───────────────────────────────────────────────────────── */

function InlineForm({
  value,
  onChange,
  onSubmit,
  placeholder,
  ariaLabel,
  submitLabel,
  icon,
  busy,
}: {
  value: string;
  onChange: (v: string) => void;
  onSubmit: () => void;
  placeholder: string;
  ariaLabel: string;
  submitLabel: string;
  icon?: React.ReactNode;
  busy: boolean;
}) {
  return (
    <form
      className="flex flex-col gap-2 pt-3 sm:flex-row"
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit();
      }}
    >
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        inputMode="text"
        aria-label={ariaLabel}
        className="min-w-0 flex-1"
      />
      <Button type="submit" variant="secondary" disabled={!value.trim() || busy}>
        {busy ? <Spinner className="size-4" aria-hidden /> : icon}
        {submitLabel}
      </Button>
    </form>
  );
}

function Field({
  label,
  help,
  value,
  onChange,
  rows = 2,
}: {
  label: string;
  help?: string;
  value: string;
  onChange: (v: string) => void;
  rows?: number;
}) {
  return (
    <label className="block min-w-0">
      <span className="block font-sans text-[13.5px] font-medium text-foreground">{label}</span>
      {help ? <span className="mt-0.5 block text-[12.5px] text-quiet">{help}</span> : null}
      <Textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        rows={rows}
        inputMode="text"
        className="mt-1.5 text-base sm:text-sm"
      />
    </label>
  );
}

function ReadField({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 border-b border-hairline py-2.5 last:border-b-0">
      <p className="font-mono text-[11px] uppercase tracking-[0.08em] text-quiet">{label}</p>
      {value.trim() ? (
        <p className="mt-1 whitespace-pre-wrap break-words font-voice text-[15.5px] leading-[1.55] text-foreground">
          {value}
        </p>
      ) : (
        <p className="mt-1 text-[13.5px] text-quiet">Not set yet.</p>
      )}
    </div>
  );
}

function Quiet({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <p className={cn("py-2 text-[13.5px] leading-relaxed text-quiet", className)}>{children}</p>
  );
}

function ErrorLine({ children }: { children: React.ReactNode }) {
  return (
    <p className="pt-2 text-[13.5px] text-danger" role="alert">
      {children}
    </p>
  );
}
