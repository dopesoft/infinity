"use client";

import { motion } from "framer-motion";
import { ArrowRight, Brain, Quote } from "lucide-react";
import { Section } from "./Section";
import { relTime } from "@/lib/dashboard/format";
import type { DashboardItem, Reflection } from "@/lib/dashboard/types";

/* Reflection-of-the-day.
 *
 * One prominent card surfacing Jarvis's latest insight. Capped at one
 * per day so it feels meaningful, not noisy. Tapping anywhere on the
 * card opens the ObjectViewer with the full reflection + source
 * observations. The "discuss" CTA also opens the viewer — same target
 * but visually offered as the primary action.
 */
export function ReflectionCard({
  reflection,
  onOpen,
}: {
  reflection: Reflection;
  onOpen: (item: DashboardItem) => void;
}) {
  return (
    <Section title="Reflection · today" Icon={Brain} delay={0.2}>
      <motion.button
        type="button"
        onClick={() => onOpen({ kind: "reflection", data: reflection })}
        whileHover={{ y: -1 }}
        transition={{ duration: 0.15 }}
        className="group relative w-full overflow-hidden rounded-xl border bg-card text-left transition-shadow hover:shadow-[0_4px_24px_-8px_hsl(var(--foreground)/0.18)]"
      >
        {/* Decorative gradient sheen across top — pure visual, no semantic.
            Tier-procedural hue (purple) makes the card feel "thinking". */}
        <span
          aria-hidden
          className="absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-tier-procedural/60 to-transparent"
        />
        <div className="relative p-4 sm:p-5">
          <div className="mb-2 flex items-center gap-2 text-tier-procedural">
            <Quote className="size-3.5" aria-hidden />
            <span className="font-mono text-[10px] uppercase tracking-[0.16em]">
              Jarvis noticed
            </span>
            <span
              className="ml-auto font-mono text-[10px] text-muted-foreground"
              suppressHydrationWarning
            >
              {relTime(reflection.capturedAt)}
            </span>
          </div>
          <h3 className="text-base font-semibold tracking-tight text-foreground sm:text-lg">
            {reflection.title}
          </h3>
          <p className="mt-2 line-clamp-3 text-[13px] leading-relaxed text-foreground/85">
            {reflection.body}
          </p>
          <div className="mt-3 flex items-center justify-between">
            <span className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
              {reflection.evidenceCount} sources
            </span>
            <span className="inline-flex items-center gap-1 text-[12px] font-medium text-foreground transition-transform group-hover:translate-x-0.5">
              Discuss this
              <ArrowRight className="size-3.5" aria-hidden />
            </span>
          </div>
        </div>
      </motion.button>
    </Section>
  );
}
