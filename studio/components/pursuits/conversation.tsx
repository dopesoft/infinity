"use client";

import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useRef,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { ArrowUp } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { useMediaQuery } from "@/lib/use-media-query";
import { cn } from "@/lib/utils";
import type { PursuitLiveMessage } from "@/lib/pursuits/usePursuitSession";

/* The shape of a conversation inside a pursuit cockpit.
 *
 * Two surfaces hold one now — the coaching session and the job hunt board —
 * and a third will hold one the day another experience lands. The transcript
 * register, the composer's behaviour and the scroll discipline are decisions
 * that belong to ONE component, not to each screen: when they lived in the
 * consumer, the second consumer copied a version of them and the drift shipped
 * (the primitives law in CLAUDE.md).
 *
 * What lives here is everything both surfaces must agree on: Jarvis speaks on
 * the page ground rather than in a bubble, the boss's own words sit right in
 * the chrome register, Enter sends and Shift+Enter breaks the line, the input
 * grows with what is being written, and the send target is a 44px square that
 * turns into a spinner while a turn is in flight.
 *
 * What does NOT live here is anything either surface decides for itself: the
 * coaching script's choices and question labels, the job hunt's selected-role
 * line, the quiet actions under the composer. Those sit above and below the
 * composer in the consumer, where they can differ honestly.
 */

/* ── Transcript ────────────────────────────────────────────────────────── */

/** Jarvis speaks on the page ground, never in a bubble or a card. */
export function AgentLine({ text }: { text: string }) {
  return (
    <p className="min-w-0 whitespace-pre-wrap break-words font-voice text-[15.5px] leading-[1.6] text-foreground">
      {text}
    </p>
  );
}

/** The boss's own words: chrome register, right-aligned, one level of tone. */
export function BossLine({ text }: { text: string }) {
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

/** One live turn from the real agent.
 *
 *  A failed turn is rendered as a failure and never as a reply: an error shown
 *  in Jarvis's own register would read as something he said. A reply that has
 *  not produced a token yet renders nothing at all, because the thinking line
 *  above the composer is already saying so. */
export function LiveLine({ message }: { message: PursuitLiveMessage }) {
  if (message.role === "boss") return <BossLine text={message.text} />;
  if (message.error) {
    return (
      <p
        className="min-w-0 font-voice text-[15.5px] leading-[1.6] text-danger"
        role="alert"
      >
        {message.error}
      </p>
    );
  }
  if (!message.text.trim()) return null;
  return <AgentLine text={message.text} />;
}

/** The line that says a turn is in flight. One wording, one place, so a
 *  cockpit can never invent its own way of saying Jarvis is working. */
export function ThinkingLine({ label = "Jarvis is thinking" }: { label?: string }) {
  return (
    <p className="thinking-shimmer font-voice text-[15.5px] leading-[1.55] text-quiet">
      {label}
    </p>
  );
}

/* ── The scroller ──────────────────────────────────────────────────────── */

/** The transcript's scroll region.
 *
 *  `justify-end` + `min-h-full` keeps the opening line down by the composer
 *  rather than stranded at the top of an empty screen, then lets the
 *  conversation grow upward the way a chat does.
 *
 *  `follow` is what the surface changes when the transcript changes. It is a
 *  dependency list rather than a count because a streaming reply grows its text
 *  without adding a message, and a scroller keyed on the count would sit still
 *  through the whole answer. */
export function ConversationScroll({
  follow,
  children,
}: {
  follow: readonly unknown[];
  children: ReactNode;
}) {
  const reduceMotion = useMediaQuery("(prefers-reduced-motion: reduce)");
  const ref = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.scrollTo({
      top: el.scrollHeight,
      behavior: reduceMotion ? "auto" : "smooth",
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reduceMotion, ...follow]);

  return (
    <div
      ref={ref}
      className="min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto scroll-touch [overflow-anchor:none]"
    >
      <div
        className="mx-auto flex min-h-full w-full min-w-0 max-w-[38rem] flex-col justify-end gap-5 px-4 py-6 sm:px-6 sm:py-8"
        aria-live="polite"
      >
        {children}
      </div>
    </div>
  );
}

/* ── The composer ──────────────────────────────────────────────────────── */

export type ConversationComposerHandle = { focus: () => void };

/** The input row, and only the input row.
 *
 *  Anything a surface wants above it (a question label, the role being talked
 *  about) or below it (quiet actions, a connection warning) is the surface's
 *  own markup. This owns the parts that must be identical everywhere: the
 *  growth, the key contract, the 44px target, and the busy state. */
export const ConversationComposer = forwardRef<
  ConversationComposerHandle,
  {
    id?: string;
    value: string;
    onChange: (value: string) => void;
    onSubmit: () => void;
    placeholder: string;
    ariaLabel: string;
    /** The send target does nothing. Distinct from `busy`, which means a turn
     *  is already in flight. */
    disabled: boolean;
    busy: boolean;
    sendLabel: string;
  }
>(function ConversationComposer(
  { id, value, onChange, onSubmit, placeholder, ariaLabel, disabled, busy, sendLabel },
  ref,
) {
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  useImperativeHandle(ref, () => ({ focus: () => inputRef.current?.focus() }), []);

  /* Grow the composer with what is being written. A paragraph typed into a
   * 44px box that scrolls internally is where people stop writing. This is the
   * same sanctioned imperative-height exception the main Composer uses: it
   * sets a calculated value, it is not a styling concern. */
  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 160)}px`;
  }, [value]);

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    // Enter sends, Shift+Enter breaks the line: the same contract as the main
    // composer, so the muscle memory carries over.
    if (e.key !== "Enter" || e.shiftKey) return;
    e.preventDefault();
    if (!disabled) onSubmit();
  }

  return (
    <div className="flex items-end gap-2 pb-3">
      <Textarea
        id={id}
        ref={inputRef}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={onKeyDown}
        rows={1}
        inputMode="text"
        aria-label={ariaLabel}
        placeholder={placeholder}
        className="min-h-11 min-w-0 flex-1 py-2.5 text-base sm:text-sm"
      />
      <Button
        size="icon"
        className="size-11 shrink-0"
        disabled={disabled}
        onClick={onSubmit}
        aria-label={sendLabel}
      >
        {busy ? (
          <Spinner className="size-4" aria-hidden />
        ) : (
          <ArrowUp className="size-4" aria-hidden />
        )}
      </Button>
    </div>
  );
});

/** A low-emphasis action under the composer. Shared so "Continue in the
 *  workspace" is the same target on every cockpit that offers it. */
export function QuietAction({
  onClick,
  children,
}: {
  onClick: () => void;
  children: ReactNode;
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

/** The row those quiet actions sit in. */
export function QuietRow({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 pb-3 text-[12.5px] text-quiet">
      {children}
    </div>
  );
}

/** The bar the composer and its quiet actions live in, pinned under the
 *  transcript with the safe-area inset the phone needs. */
export function ConversationFooter({ children }: { children: ReactNode }) {
  return (
    <div className="shrink-0 border-t border-hairline bg-background px-4 pt-3 sm:px-6 pb-safe">
      <div className="mx-auto w-full min-w-0 max-w-[38rem]">{children}</div>
    </div>
  );
}
