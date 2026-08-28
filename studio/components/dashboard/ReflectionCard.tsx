"use client";

import { ArrowRight } from "lucide-react";
import { Section } from "./Section";
import { relTime } from "@/lib/dashboard/format";
import type { DashboardItem, Reflection } from "@/lib/dashboard/types";

/* Reflection-of-the-day.
 *
 * Jarvis's latest insight, capped at one a day so it stays meaningful. Tapping
 * anywhere opens the ObjectViewer with the full reflection and its source
 * observations; "Discuss" is the same target, offered as the explicit action.
 *
 * MAJORDOMO SWEEP: this was the worst offender for stacked headers - a
 * bordered `Section` with its own 48px header bar, containing a `rounded-xl
 * border bg-card` button, containing a SECOND header stack (a "Jarvis noticed"
 * eyebrow with a quote glyph, then an `<h3>`, then the body, then a footer).
 * Two headers and two boxes for one paragraph.
 *
 * It is now the one place on Home that earns `tone="card"` (§1.2: a card is
 * for ONE object you act on): section title "Reflection", the when and the
 * evidence count as its quiet meta, the reflection ITSELF in the voice face
 * because these are Jarvis's words, and one action. The eyebrow is gone -
 * §1.6, never an eyebrow above a title - and so is the decorative gradient
 * sheen and the hover lift.
 */
export function ReflectionCard({
  reflection,
  onOpen,
}: {
  reflection: Reflection;
  onOpen: (item: DashboardItem) => void;
}) {
  const open = () => onOpen({ kind: "reflection", data: reflection });

  return (
    <Section
      tone="card"
      title="Reflection"
      badge={
        <span suppressHydrationWarning>
          {relTime(reflection.capturedAt)} · {reflection.evidenceCount} sources
        </span>
      }
    >
      <div className="flex min-w-0 flex-col gap-3">
        <button
          type="button"
          onClick={open}
          className="group min-w-0 rounded-[10px] text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
        >
          <p className="font-voice text-[15.5px] font-medium leading-[1.4] tracking-tight text-foreground">
            {reflection.title}
          </p>
          <p className="mt-1.5 line-clamp-3 font-voice text-[15.5px] leading-[1.55] text-muted-foreground">
            {reflection.body}
          </p>
        </button>
        <button
          type="button"
          onClick={open}
          className="group inline-flex min-h-11 items-center gap-1 self-start text-[13.5px] font-medium text-foreground transition-colors hover:text-brand"
        >
          Discuss
          <ArrowRight
            className="size-3.5 transition-transform group-hover:translate-x-0.5"
            aria-hidden
          />
        </button>
      </div>
    </Section>
  );
}
