"use client";

import { useCallback, useEffect, useState } from "react";
import {
  CheckCircle2,
  Circle,
  ClipboardCheck,
  ShieldCheck,
  Target,
  XCircle,
} from "lucide-react";
import { Section } from "./Section";
import { ScrollList } from "./ScrollList";
import { cn } from "@/lib/utils";
import { useRealtime } from "@/lib/realtime/provider";
import { fetchMandates, type MandateDTO } from "@/lib/api";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { ModalSection, ModalDl } from "@/components/ui/modal-content";

/* Mandates — the active definitions of done. Each card row is a task Jarvis has
 * committed to with binary acceptance criteria; the progress bar shows how many
 * are verified. Tapping opens the full criteria + the cross-vendor crosscheck
 * verdict. Self-fetches active mandates and live-updates via mem_mandates.
 *
 * This renders the Mandate / Crosscheck primitives — the "loop until verified"
 * upgrade — without a bespoke widget per producer: any agent/cron mandate shows
 * here automatically.
 */
export function MandatesCard() {
  const [mandates, setMandates] = useState<MandateDTO[]>([]);
  const [open, setOpen] = useState<MandateDTO | null>(null);

  const load = useCallback(async () => {
    const res = await fetchMandates({ active: true, limit: 20 });
    if (res) setMandates(res);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  useRealtime("mem_mandates", load);

  if (mandates.length === 0) return null;

  return (
    <>
      <Section title="Mandates" Icon={Target} delay={0.12} badge={mandates.length}>
        <ScrollList max={4}>
          <ul className="space-y-2">
            {mandates.map((m) => (
              <MandateRow key={m.id} m={m} onOpen={() => setOpen(m)} />
            ))}
          </ul>
        </ScrollList>
      </Section>
      <MandateModal mandate={open} onClose={() => setOpen(null)} />
    </>
  );
}

function passedCount(m: MandateDTO): number {
  return (m.criteria ?? []).filter((c) => c.status === "pass").length;
}

function MandateRow({ m, onOpen }: { m: MandateDTO; onOpen: () => void }) {
  const total = (m.criteria ?? []).length;
  const passed = passedCount(m);
  const pct = total > 0 ? Math.round((passed / total) * 100) : 0;
  return (
    <li>
      <button
        type="button"
        onClick={onOpen}
        className="group w-full rounded-xl border border-border bg-card/40 p-3 text-left transition-all hover:-translate-y-px hover:border-foreground/20 min-w-0"
      >
        <div className="flex items-center justify-between gap-2">
          <span className="truncate text-sm font-medium">{m.title}</span>
          <div className="flex shrink-0 items-center gap-1.5">
            {m.high_stakes ? (
              <ShieldCheck
                className={cn(
                  "size-3.5",
                  m.verified_at ? "text-emerald-500" : "text-amber-400",
                )}
                aria-label={m.verified_at ? "crosschecked" : "needs crosscheck"}
              />
            ) : null}
            <span className="text-xs tabular-nums text-muted-foreground">
              {passed}/{total}
            </span>
          </div>
        </div>
        <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-muted">
          <div
            className={cn(
              "h-full rounded-full transition-all",
              pct === 100 ? "bg-emerald-500" : "bg-foreground/50",
            )}
            style={{ width: `${pct}%` }}
          />
        </div>
      </button>
    </li>
  );
}

function MandateModal({ mandate, onClose }: { mandate: MandateDTO | null; onClose: () => void }) {
  const m = mandate;
  const cross = m?.crosscheck;
  return (
    <ResponsiveModal
      open={!!m}
      onOpenChange={(o) => {
        if (!o) onClose();
      }}
      title={m?.title ?? "Mandate"}
      description={m?.summary || undefined}
      size="md"
    >
      {m ? (
        <div className="space-y-4">
          <ModalSection label="Definition of done">
            <ul className="space-y-2">
              {(m.criteria ?? []).map((c) => (
                <li key={c.id} className="flex items-start gap-2 text-sm min-w-0">
                  {c.status === "pass" ? (
                    <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-emerald-500" />
                  ) : c.status === "fail" ? (
                    <XCircle className="mt-0.5 size-4 shrink-0 text-rose-500" />
                  ) : (
                    <Circle className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                  )}
                  <div className="min-w-0">
                    <span className="break-words">{c.text}</span>
                    {c.evidence ? (
                      <span className="mt-0.5 block break-words text-xs text-muted-foreground">
                        {c.evidence}
                      </span>
                    ) : null}
                  </div>
                </li>
              ))}
            </ul>
          </ModalSection>

          {m.high_stakes ? (
            <ModalSection
              label="Verification"
              icon={<ClipboardCheck className="size-3.5" />}
              tone={cross?.overall === "pass" ? "success" : cross?.overall === "fail" ? "error" : "default"}
            >
              {cross?.overall ? (
                <ModalDl
                  entries={[
                    { k: "Verdict", v: cross.overall === "pass" ? "Passed" : "Failed" },
                    { k: "Verified by", v: cross.auditor ?? "—" },
                    {
                      k: "Confidence",
                      v:
                        typeof cross.confidence === "number"
                          ? `${Math.round(cross.confidence * 100)}%`
                          : "—",
                    },
                    ...(cross.notes ? [{ k: "Notes", v: cross.notes }] : []),
                  ]}
                />
              ) : (
                <p className="text-sm text-muted-foreground">
                  High-stakes — awaiting an independent verification pass before this can close.
                </p>
              )}
            </ModalSection>
          ) : null}
        </div>
      ) : null}
    </ResponsiveModal>
  );
}
