"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useAppRouter } from "@/lib/loading";
import { MessageSquare, RotateCcw } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import { Inset } from "@/components/ui/inset";
import { useMediaQuery } from "@/lib/use-media-query";
import {
  AgentLine,
  BossLine,
  ConversationComposer,
  ConversationFooter,
  ConversationScroll,
  LiveLine,
  QuietAction,
  QuietRow,
  ThinkingLine,
  type ConversationComposerHandle,
} from "@/components/pursuits/conversation";
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
 *
 * The transcript register, the scroller and the composer row are NOT decided
 * here: they come from components/pursuits/conversation, shared with the job
 * hunt cockpit, so the two surfaces cannot drift into two chat treatments.
 * What stays local is genuinely coaching-specific — the beat choices, the
 * question label above the composer, and the quoted memory.
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
  const inputRef = useRef<ConversationComposerHandle | null>(null);
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
      <ConversationScroll follow={[entries, live.messages]}>
        {entries.map((entry) => (
          <TranscriptEntry key={entry.key} entry={entry} live={live.messages} />
        ))}

        {(speaking || saving || live.busy) && !failed ? (
          <ThinkingLine label={saving ? "Writing that down" : undefined} />
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
      </ConversationScroll>

      <ConversationFooter>
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

        <ConversationComposer
          id="pc-coach-input"
          ref={inputRef}
          value={draft}
          onChange={setDraft}
          onSubmit={() => (answering ? submitAnswer() : void submitAsk())}
          ariaLabel={answering && activeCompose ? activeCompose.label : "Say something to Jarvis"}
          placeholder={
            answering && activeCompose ? activeCompose.placeholder : "Say something to Jarvis"
          }
          disabled={sendDisabled}
          busy={live.busy}
          sendLabel={answering ? "Send answer" : "Ask Jarvis"}
        />

        <QuietRow>
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
            {handingOff ? <Spinner className="size-3.5" aria-hidden /> : null}
            Continue in the workspace
          </QuietAction>
          {!live.connected ? (
            <span className="text-warning">
              I am not connected, so I cannot answer questions right now. Your programme
              still saves.
            </span>
          ) : null}
        </QuietRow>
      </ConversationFooter>
    </div>
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
        <AgentLine text={entry.text} />
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
  return <LiveLine message={message} />;
}
