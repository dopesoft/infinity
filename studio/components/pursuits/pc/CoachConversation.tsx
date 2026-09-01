"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useAppRouter } from "@/lib/loading";
import { ArrowUp, MessageSquare, RotateCcw } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import { Inset } from "@/components/ui/inset";
import { Textarea } from "@/components/ui/textarea";
import { useMediaQuery } from "@/lib/use-media-query";
import { cn } from "@/lib/utils";
import { writeCockpit } from "@/lib/pursuits/pc/api";
import {
  adjustmentLine,
  buildCommitWrites,
  buildScript,
  type CoachBeat,
  type CoachCompose,
} from "@/lib/pursuits/pc/coaching";
import { useCoachSession, type CoachLiveMessage } from "@/lib/pursuits/pc/useCoachSession";
import type { PCCockpit } from "@/lib/pursuits/pc/types";

/* The coaching session.
 *
 * This is the whole surface: Jarvis speaks one line at a time, the boss taps a
 * reply or writes one, and the flow persists itself through the exact cockpit
 * writes the old form used. Nothing here is a card, a panel, or a tab. The
 * programme's data lives one tap away in the drawer; this is the conversation.
 *
 * Two kinds of turn share the transcript:
 *
 *   • GUIDED beats, built by lib/pursuits/pc/coaching.ts from cockpit state.
 *     Deterministic and instant, because the coaching mechanics (which phase,
 *     which memory, which day) are decided in Go and must not wait on a model.
 *   • LIVE turns from the real agent, over the app's existing WebSocket and a
 *     seeded `pursuit_pc` session (see useCoachSession). Anything the boss
 *     types that is not an answer to the current question goes there.
 *
 * The transcript is one ordered list of entries so the two interleave the way
 * the conversation actually happened, rather than sitting in separate regions.
 */

type Entry =
  | { key: string; kind: "coach"; text: string; quote?: { label: string; body: string } }
  | { key: string; kind: "boss"; text: string }
  | { key: string; kind: "note"; text: string }
  | { key: string; kind: "live"; id: string };

/** Omit that distributes over a union, so `append` accepts any ONE Entry
 *  variant rather than the (unsatisfiable) intersection of all of them. */
type DistributiveOmit<T, K extends PropertyKey> = T extends unknown ? Omit<T, K> : never;
type NewEntry = DistributiveOmit<Entry, "key">;

type CommitStatus = "running" | "done" | "error";

const LINE_DELAY_MS = 760;
const FIRST_LINE_DELAY_MS = 220;

export function CoachConversation({
  cockpit,
  onUpdated,
  onLeave,
}: {
  cockpit: PCCockpit;
  onUpdated: (next: PCCockpit) => void;
  /** Close the surface before navigating away, so the modal never unmounts
   *  mid-navigation and leave the page with a locked scroll. */
  onLeave: () => void;
}) {
  const router = useAppRouter();
  const reduceMotion = useMediaQuery("(prefers-reduced-motion: reduce)");
  const live = useCoachSession(cockpit.pursuit.id);
  const [handingOff, setHandingOff] = useState(false);

  /* The script is built ONCE, from the cockpit as it stood when the session
   * opened. It is deliberately not recomputed on every cockpit refresh: the
   * phase moves forward AS A RESULT of this conversation (a logged morning
   * flips the server to midday), so rebuilding would wipe the exchange the
   * boss just had and drop him into a different flow mid-sentence. Reopening
   * the cockpit is what starts the next phase's session. */
  const [script] = useState(() => buildScript(cockpit));

  const [beatId, setBeatId] = useState(script.start);
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [entries, setEntries] = useState<Entry[]>([]);
  const [revealed, setRevealed] = useState(0);
  const [commits, setCommits] = useState<Record<string, CommitStatus>>({});
  const [commitError, setCommitError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [compose, setCompose] = useState<CoachCompose | null>(null);
  const [askMode, setAskMode] = useState(false);
  const [draft, setDraft] = useState("");

  const keyRef = useRef(0);
  const startedRef = useRef<Set<string>>(new Set());
  const seenLiveRef = useRef<Set<string>>(new Set());
  const scrollerRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const answersRef = useRef(answers);
  answersRef.current = answers;

  const beat: CoachBeat | undefined = script.beats[beatId];

  const append = useCallback((entry: NewEntry) => {
    keyRef.current += 1;
    setEntries((prev) => [...prev, { ...entry, key: `e${keyRef.current}` } as Entry]);
  }, []);

  /* Commit beats do their writes BEFORE they speak, so a confirmation line is
   * never spoken over a write that failed. `attempt` is what a retry bumps. */
  const commitStatus = beat?.commit ? commits[beat.id] : "done";
  const commitDone = !beat?.commit || commitStatus === "done";

  useEffect(() => {
    if (!beat?.commit) return;
    const guard = `${beat.id}#${attempt}`;
    if (startedRef.current.has(guard)) return;
    startedRef.current.add(guard);

    const beatKey = beat.id;
    const commit = beat.commit;
    let cancelled = false;

    setCommits((prev) => ({ ...prev, [beatKey]: "running" }));
    setCommitError(null);

    void (async () => {
      try {
        let latest = cockpit;
        for (const write of buildCommitWrites(commit, answersRef.current)) {
          latest = await writeCockpit(cockpit.pursuit.id, write.action, write.body);
        }
        if (cancelled) return;
        setCommits((prev) => ({ ...prev, [beatKey]: "done" }));
        onUpdated(latest);
      } catch (e) {
        if (cancelled) return;
        // A write that failed must never be reported back as a saved day.
        setCommits((prev) => ({ ...prev, [beatKey]: "error" }));
        setCommitError(
          e instanceof Error ? e.message : "The write did not go through.",
        );
      }
    })();

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [beat?.id, attempt]);

  /* Speak the beat, one line at a time. The adjustment note is folded into the
   * opening beat as another spoken line, never a warning box. */
  useEffect(() => {
    if (!beat || !commitDone) return;
    if (revealed >= beat.lines.length) return;

    const delay = reduceMotion ? 0 : revealed === 0 ? FIRST_LINE_DELAY_MS : LINE_DELAY_MS;
    const timer = window.setTimeout(() => {
      const isLast = revealed === beat.lines.length - 1;
      append({
        kind: "coach",
        text: beat.lines[revealed],
        quote: isLast ? beat.quote : undefined,
      });
      if (isLast && beat.id === script.start) {
        const note = adjustmentLine(cockpit);
        if (note) append({ kind: "coach", text: note });
      }
      setRevealed((r) => r + 1);
    }, delay);

    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [beat?.id, revealed, commitDone, reduceMotion]);

  /* Mirror live agent turns into the transcript in arrival order. */
  useEffect(() => {
    const unseen = live.messages.filter((m) => !seenLiveRef.current.has(m.id));
    if (unseen.length === 0) return;
    for (const m of unseen) seenLiveRef.current.add(m.id);
    setEntries((prev) => [
      ...prev,
      ...unseen.map((m) => ({ key: `l-${m.id}`, kind: "live" as const, id: m.id })),
    ]);
  }, [live.messages]);

  /* Follow the conversation. Jump rather than glide under reduced motion. */
  useEffect(() => {
    const el = scrollerRef.current;
    if (!el) return;
    el.scrollTo({ top: el.scrollHeight, behavior: reduceMotion ? "auto" : "smooth" });
  }, [entries, live.messages, reduceMotion]);

  /* Grow the composer with the answer. The evening question wants a paragraph,
   * and a 44px box that scrolls internally is where people stop writing. This
   * is the same sanctioned imperative-height exception the main Composer uses:
   * it sets a calculated value, it is not a styling concern. */
  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 160)}px`;
  }, [draft]);

  const speaking = Boolean(beat) && revealed < (beat?.lines.length ?? 0);
  const saving = commitStatus === "running";
  const failed = commitStatus === "error";
  const activeCompose = compose ?? beat?.compose ?? null;
  const answering = Boolean(activeCompose) && !askMode;
  const choices = beat?.choices ?? [];
  const showChoices = !speaking && commitDone && !compose && !askMode && choices.length > 0;

  function goTo(next: string) {
    setCompose(null);
    setAskMode(false);
    setDraft("");
    setRevealed(0);
    setBeatId(next);
  }

  function pick(choiceId: string) {
    const choice = choices.find((c) => c.id === choiceId);
    if (!choice) return;
    append({ kind: "boss", text: choice.label });
    if (choice.record) {
      const { key, value } = choice.record;
      setAnswers((prev) => ({ ...prev, [key]: value }));
    }
    if (choice.compose) {
      setCompose(choice.compose);
      setDraft(choice.compose.initialValue ?? "");
      setAskMode(false);
      window.setTimeout(() => inputRef.current?.focus(), 0);
      return;
    }
    if (choice.next) goTo(choice.next);
  }

  function submitAnswer() {
    if (!activeCompose) return;
    const value = draft.trim();
    if (!value && !activeCompose.optional) return;
    if (value) {
      append({ kind: "boss", text: value });
      setAnswers((prev) => ({ ...prev, [activeCompose.key]: value }));
    } else {
      append({ kind: "note", text: "Skipped" });
    }
    goTo(activeCompose.next);
  }

  async function submitAsk() {
    const value = draft.trim();
    if (!value) return;
    setDraft("");
    setAskMode(false);
    await live.ask(value);
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    // Enter sends, Shift+Enter breaks the line: the same contract as the main
    // composer, so the muscle memory carries over.
    if (e.key !== "Enter" || e.shiftKey) return;
    e.preventDefault();
    if (answering) submitAnswer();
    else void submitAsk();
  }

  /* Hand off to the full workspace on the SAME session, so the conversation
   * continues rather than restarting. Minting it here when the boss never
   * spoke is the same seed the dashboard's Discuss-with-Jarvis performs. */
  async function continueInWorkspace() {
    setHandingOff(true);
    try {
      const id = await live.open();
      onLeave();
      router.push(id ? `/live?session=${encodeURIComponent(id)}` : "/live");
    } finally {
      setHandingOff(false);
    }
  }

  const sendDisabled = live.busy
    ? true
    : answering
      ? !draft.trim() && !activeCompose?.optional
      : draft.trim().length === 0;

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div
        ref={scrollerRef}
        className="min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto scroll-touch [overflow-anchor:none]"
      >
        {/* `justify-end` + `min-h-full` keeps the opening line down by the
            composer rather than stranded at the top of an empty screen, then
            lets the conversation grow upward the way a chat does. */}
        <div
          className="mx-auto flex min-h-full w-full min-w-0 max-w-[38rem] flex-col justify-end gap-5 px-4 py-6 sm:px-6 sm:py-8"
          aria-live="polite"
        >
          {entries.map((entry) => (
            <TranscriptEntry key={entry.key} entry={entry} live={live.messages} />
          ))}

          {(speaking || saving || live.busy) && !failed ? (
            <p className="thinking-shimmer font-voice text-[15.5px] leading-[1.55] text-quiet">
              {saving ? "Writing that down" : "Jarvis is thinking"}
            </p>
          ) : null}

          {failed ? (
            <div className="min-w-0" role="alert">
              <p className="font-voice text-[15.5px] leading-[1.6] text-danger">
                I could not write that down, so nothing has been logged for this step yet.
                {commitError ? ` ${commitError}` : ""}
              </p>
              <Button
                variant="outline"
                size="sm"
                className="mt-3"
                onClick={() => setAttempt((n) => n + 1)}
              >
                <RotateCcw className="size-4" aria-hidden />
                Try saving again
              </Button>
            </div>
          ) : null}
        </div>
      </div>

      <div className="shrink-0 border-t border-hairline bg-background px-4 pt-3 sm:px-6 pb-safe">
        <div className="mx-auto w-full min-w-0 max-w-[38rem]">
          {showChoices ? (
            <div className="flex flex-wrap gap-2 pb-3">
              {choices.map((choice, i) => (
                <Button
                  key={choice.id}
                  variant={i === 0 ? "default" : "outline"}
                  size="sm"
                  onClick={() => pick(choice.id)}
                >
                  {choice.label}
                </Button>
              ))}
            </div>
          ) : null}

          {answering && activeCompose ? (
            <label htmlFor="pc-coach-input" className="block pb-2">
              <span className="font-sans text-[13.5px] font-medium text-foreground">
                {activeCompose.label}
              </span>
              {activeCompose.help ? (
                <span className="mt-0.5 block text-[12.5px] leading-snug text-quiet">
                  {activeCompose.help}
                </span>
              ) : null}
            </label>
          ) : null}

          <div className="flex items-end gap-2 pb-3">
            <Textarea
              id="pc-coach-input"
              ref={inputRef}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={onKeyDown}
              rows={1}
              inputMode="text"
              aria-label={
                answering && activeCompose ? activeCompose.label : "Say something to Jarvis"
              }
              placeholder={
                answering && activeCompose
                  ? activeCompose.placeholder
                  : "Say something to Jarvis"
              }
              className="min-h-11 min-w-0 flex-1 py-2.5 text-base sm:text-sm"
            />
            <Button
              size="icon"
              className="size-11 shrink-0"
              disabled={sendDisabled}
              onClick={() => (answering ? submitAnswer() : void submitAsk())}
              aria-label={answering ? "Send answer" : "Ask Jarvis"}
            >
              {live.busy ? (
                <Spinner className="size-4" aria-hidden />
              ) : (
                <ArrowUp className="size-4" aria-hidden />
              )}
            </Button>
          </div>

          <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 pb-3 text-[12.5px] text-quiet">
            {answering ? (
              <>
                {activeCompose?.optional ? (
                  <QuietAction onClick={submitAnswer}>Skip this</QuietAction>
                ) : null}
                <QuietAction
                  onClick={() => {
                    setAskMode(true);
                    setDraft("");
                    window.setTimeout(() => inputRef.current?.focus(), 0);
                  }}
                >
                  <MessageSquare className="size-3.5" aria-hidden />
                  Ask something instead
                </QuietAction>
              </>
            ) : askMode && activeCompose ? (
              <QuietAction
                onClick={() => {
                  setAskMode(false);
                  setDraft(activeCompose.initialValue ?? "");
                }}
              >
                Back to the question
              </QuietAction>
            ) : null}
            <QuietAction onClick={() => void continueInWorkspace()}>
              {handingOff ? (
                <Spinner className="size-3.5" aria-hidden />
              ) : null}
              Continue in the workspace
            </QuietAction>
            {!live.connected ? (
              <span className="text-warning">
                I am not connected, so I cannot answer questions right now. Your programme
                still saves.
              </span>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  );
}

function QuietAction({
  onClick,
  children,
}: {
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="inline-flex min-h-8 items-center gap-1 rounded-sm underline-offset-4 hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
    >
      {children}
    </button>
  );
}

/* ── Transcript rendering ──────────────────────────────────────────────── */

function TranscriptEntry({
  entry,
  live,
}: {
  entry: Entry;
  live: CoachLiveMessage[];
}) {
  if (entry.kind === "coach") {
    return (
      <div className="min-w-0">
        <CoachLine text={entry.text} />
        {entry.quote ? (
          <div className="mt-3 min-w-0">
            <p className="pb-1.5 font-mono text-[11px] uppercase tracking-[0.08em] text-quiet">
              {entry.quote.label}
            </p>
            <Inset variant="quote" text={entry.quote.body} />
          </div>
        ) : null}
      </div>
    );
  }

  if (entry.kind === "boss") return <BossLine text={entry.text} />;

  if (entry.kind === "note") {
    return (
      <p className="text-right font-mono text-[11px] uppercase tracking-[0.08em] text-quiet">
        {entry.text}
      </p>
    );
  }

  const message = live.find((m) => m.id === entry.id);
  if (!message) return null;
  if (message.role === "boss") return <BossLine text={message.text} />;
  if (message.error) {
    return (
      <p className="min-w-0 font-voice text-[15.5px] leading-[1.6] text-danger" role="alert">
        {message.error}
      </p>
    );
  }
  if (!message.text.trim()) return null;
  return <CoachLine text={message.text} />;
}

/** Jarvis speaks on the page ground, never in a bubble or a card. */
function CoachLine({ text }: { text: string }) {
  return (
    <p className="min-w-0 whitespace-pre-wrap break-words font-voice text-[15.5px] leading-[1.6] text-foreground">
      {text}
    </p>
  );
}

/** The boss's own words: chrome register, right-aligned, one level of tone. */
function BossLine({ text }: { text: string }) {
  if (!text.trim()) return null;
  return (
    <div className="flex min-w-0 justify-end">
      <p
        className={cn(
          "min-w-0 max-w-[85%] whitespace-pre-wrap break-words rounded-[10px] bg-muted px-3 py-2",
          "text-right font-sans text-[13.5px] leading-relaxed text-foreground",
        )}
      >
        {text}
      </p>
    </div>
  );
}
