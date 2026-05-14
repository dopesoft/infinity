"use client";

import { useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Infinity as InfinityIcon, ArrowLeft, ArrowRight, Check } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { setMeta, upsertProfileFact } from "@/lib/api";
import { cn } from "@/lib/utils";

/* First Run wizard. Collects identity context from the user once and writes
 * each answer to mem_memories(tier='semantic', project='_self') via the
 * profile API. Every fact lands in the always-on system prompt so the agent
 * starts every future session knowing who the boss is, what they work on,
 * how they want to be addressed, and when not to interrupt.
 *
 * The wizard is mobile-first: one question per screen, big touch targets,
 * a single primary action. Desktop gets the same layout centered in a
 * narrow column — no fancy multi-column form because identity questions
 * deserve focused attention, not data-entry density. */

type Step = {
  key: string;
  title: string;
  subtitle: string;
  placeholder: string;
  long?: boolean;
  optional?: boolean;
  /* importance maps to the mem_memories.importance score. 9 is the default for
   * boss-profile rows; we bump load-bearing facts (name, role) to 10 so they
   * float to the top of the system prompt and never drop on truncation. */
  importance: number;
};

const STEPS: Step[] = [
  {
    key: "Name",
    title: "What should I call you?",
    subtitle: "Your name, a nickname, or just 'boss' — whatever lands right.",
    placeholder: "e.g. Khaya, or boss",
    importance: 10,
  },
  {
    key: "Role",
    title: "What do you do?",
    subtitle:
      "One line about your work or role. This grounds the agent so it stops asking what you do every week.",
    placeholder: "e.g. Founder of Malabie Industries — I build AI products.",
    long: true,
    importance: 10,
  },
  {
    key: "Active projects",
    title: "What are you actively working on?",
    subtitle:
      "Projects in flight. One per line. The agent uses these to disambiguate when you say 'the deploy' or 'that bug'.",
    placeholder: "Infinity — the AGI companion\nMalabie — paint marketplace\nDirector — video tooling",
    long: true,
    importance: 9,
  },
  {
    key: "Communication style",
    title: "How do you want me to talk to you?",
    subtitle:
      "Terse? Verbose? Profane? Formal? Emojis welcome or not? Pick the texture — be specific.",
    placeholder: "Terse, profanity-friendly, zero emojis. Cite memory when relevant.",
    long: true,
    importance: 9,
  },
  {
    key: "Working hours",
    title: "When is it safe to interrupt?",
    subtitle:
      "Your usual working hours and the times you absolutely don't want pings. The proactive engine uses this.",
    placeholder: "Mon–Fri 9am–7pm PT. No interrupts after 9pm or on Sundays unless critical.",
    long: true,
    importance: 8,
  },
  {
    key: "Key relationships",
    title: "Who should I know about?",
    subtitle:
      "People who matter — co-founders, family, customers, key contacts. One per line with a short note.",
    placeholder: "Mom — call every Sunday\nBob — landlord, lease comes up in March\nMike — CTO at Stripe partner",
    long: true,
    optional: true,
    importance: 7,
  },
  {
    key: "Anything else",
    title: "Anything else I should know?",
    subtitle:
      "Health constraints, preferences, hard rules, recurring goals — whatever you want me to remember on day one.",
    placeholder: "I'm in a sober streak (no drinks, no nicotine). I read in the morning before screens.",
    long: true,
    optional: true,
    importance: 8,
  },
];

export default function OnboardingPage() {
  const router = useRouter();
  const [index, setIndex] = useState(0);
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);
  const [skipping, setSkipping] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const step = STEPS[index];
  const value = answers[step.key] ?? "";
  const isLast = index === STEPS.length - 1;
  const canAdvance = useMemo(() => {
    if (step.optional) return true;
    return value.trim().length > 0;
  }, [step, value]);

  function update(v: string) {
    setAnswers((prev) => ({ ...prev, [step.key]: v }));
  }

  async function finish() {
    setError(null);
    setSubmitting(true);
    /* Write every non-empty answer in parallel. Each becomes a row in
     * mem_memories under project='_self' tier='semantic' so they survive
     * compaction, pruning, and consolidation — they are the agent's
     * always-on primer. Importance differs per fact so the system prompt
     * ranks identity > projects > preferences > optional context. */
    const writes = STEPS.map(async (s) => {
      const content = (answers[s.key] ?? "").trim();
      if (!content) return null;
      return upsertProfileFact({
        title: s.key,
        content,
        importance: s.importance,
      });
    });
    const results = await Promise.all(writes);
    const failed = results.some((r, i) => {
      const expected = (answers[STEPS[i].key] ?? "").trim().length > 0;
      return expected && r === null;
    });
    if (failed) {
      setSubmitting(false);
      setError("One or more facts didn't save. Check your connection and try again.");
      return;
    }
    await setMeta("boss_onboarded", "true");
    try { localStorage.setItem("boss_onboarded", "true"); } catch {}
    setSubmitting(false);
    router.replace("/live");
  }

  async function skipAll() {
    setSkipping(true);
    /* Set the flag without writing any facts. Agent boots blank but the
     * wizard never reappears — the user can seed their profile later from
     * the Boss Profile panel in Memory. The localStorage write is the
     * escape hatch when Core is unreachable (local dev with no backend),
     * so Skip always works even if the API call silently fails. */
    await setMeta("boss_onboarded", "true");
    try { localStorage.setItem("boss_onboarded", "true"); } catch {}
    setSkipping(false);
    router.replace("/live");
  }

  return (
    <div className="flex h-app min-h-app flex-col bg-background pt-safe">
      <header className="flex h-14 items-center justify-between border-b px-3 sm:px-4">
        <div className="flex items-center gap-2 text-foreground">
          <InfinityIcon className="size-6" aria-hidden />
          <span className="text-sm font-semibold tracking-tight">infinity</span>
        </div>
        <button
          type="button"
          onClick={skipAll}
          disabled={skipping || submitting}
          className="text-xs text-muted-foreground hover:text-foreground disabled:opacity-50"
        >
          {skipping ? "Skipping…" : "Skip for now"}
        </button>
      </header>

      <div className="mx-auto flex w-full max-w-xl flex-1 flex-col justify-center px-4 pb-safe sm:px-6">
        <div className="flex items-center gap-1">
          {STEPS.map((_, i) => (
            <span
              key={i}
              className={cn(
                "h-1 flex-1 rounded-full transition-colors",
                i < index
                  ? "bg-foreground"
                  : i === index
                    ? "bg-foreground/70"
                    : "bg-muted",
              )}
              aria-hidden
            />
          ))}
        </div>
        <p className="pt-3 text-xs uppercase tracking-wider text-muted-foreground">
          Step {index + 1} of {STEPS.length}
          {step.optional && <span className="ml-2 normal-case">(optional)</span>}
        </p>

        <h1 className="pt-2 text-2xl font-semibold leading-tight sm:text-3xl">
          {step.title}
        </h1>
        <p className="pt-2 text-sm text-muted-foreground sm:text-base">
          {step.subtitle}
        </p>

        <div className="pt-6">
          {step.long ? (
            <Textarea
              autoFocus
              value={value}
              onChange={(e) => update(e.target.value)}
              placeholder={step.placeholder}
              rows={6}
              inputMode="text"
              className="min-h-[10rem] text-base"
            />
          ) : (
            <Input
              autoFocus
              value={value}
              onChange={(e) => update(e.target.value)}
              placeholder={step.placeholder}
              inputMode="text"
              className="text-base"
            />
          )}
        </div>

        {error && (
          <p className="pt-3 text-sm text-destructive" role="alert">
            {error}
          </p>
        )}

        <div className="flex items-center gap-2 pt-8">
          <Button
            type="button"
            variant="ghost"
            onClick={() => setIndex((i) => Math.max(0, i - 1))}
            disabled={index === 0 || submitting}
            className="gap-1.5"
          >
            <ArrowLeft className="size-4" aria-hidden />
            Back
          </Button>
          <div className="flex-1" />
          {isLast ? (
            <Button
              type="button"
              onClick={finish}
              disabled={!canAdvance || submitting}
              className="gap-1.5"
            >
              <Check className="size-4" aria-hidden />
              {submitting ? "Saving…" : "Finish"}
            </Button>
          ) : (
            <Button
              type="button"
              onClick={() => setIndex((i) => Math.min(STEPS.length - 1, i + 1))}
              disabled={!canAdvance}
              className="gap-1.5"
            >
              Next
              <ArrowRight className="size-4" aria-hidden />
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}
