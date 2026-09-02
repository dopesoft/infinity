"use client";

import { useRef, useState } from "react";
import { useAppRouter } from "@/lib/loading";
import { Spinner } from "@/components/ui/spinner";
import {
  AgentLine,
  ConversationComposer,
  ConversationFooter,
  ConversationScroll,
  LiveLine,
  QuietAction,
  QuietRow,
  ThinkingLine,
  type ConversationComposerHandle,
} from "@/components/pursuits/conversation";
import { stageLabel } from "@/lib/pursuits/jh/labels";
import { useJobHuntSession } from "@/lib/pursuits/jh/useJobHuntSession";
import type { JHRole } from "@/lib/pursuits/jh/types";

/* Talking to Jarvis about the board, without leaving the board.
 *
 * The sibling of CoachConversation, and deliberately the thinner of the two.
 * The coaching programme runs a deterministic beat script because its
 * mechanics (which phase, which memory, which day) are decided in Go; a job
 * hunt has no script at all. Every word the boss types here goes to the real
 * agent over the seeded `pursuit_jh` session, which arrives already holding
 * the whole pipeline (jh.FormatChatContext, sessions_seed_api.go), so there is
 * nothing for this surface to answer locally and it never pretends to.
 *
 * The register, the scroller, the composer and the quiet row are NOT decided
 * here: they come from components/pursuits/conversation, the same primitives
 * the coaching session wears, so the two cockpits cannot drift into two chat
 * treatments.
 *
 * What IS decided here is the one thing that is genuinely job-hunt specific:
 * the conversation carries what he is LOOKING AT. When a role is open on the
 * board, the turn reaches Jarvis anchored to that role, so "tailor my resume
 * for this one" needs no further explanation. When nothing is open, the
 * subject is the board as a whole. That anchoring is a MECHANIC, so it lives
 * in code and is stamped on the wire by `ask`, never as an instruction the
 * model has to remember and never as a machine-written preamble printed back
 * at him in the transcript.
 */

/** What the wire is told he is looking at. Re-sent only when the anchor
 *  CHANGES, so a long conversation about one role does not repeat itself, and
 *  switching roles mid-thread re-anchors without him having to say so. */
function anchorLine(role: JHRole | null): string {
  if (!role) return "I am asking about the job hunt board as a whole.";
  const stage = stageLabel(role.stage);
  return `I am looking at this role on the board: ${role.company}, ${role.role_title}, currently at: ${stage}.`;
}

/** The opening line. Jarvis says what he can see, which is the honest version
 *  of an empty transcript: the seed really does carry the whole board. */
function opener(role: JHRole | null): string {
  if (!role) {
    return "I have the whole board in front of me. Ask me about any of it, or open a role and I will follow you to it.";
  }
  return `I have ${role.company}, ${role.role_title} in front of me, along with the rest of the board. Ask me to tailor something for it, read the posting, or find a way in.`;
}

export function JobHuntConversation({
  pursuitId,
  role,
  onClearRole,
  onLeave,
}: {
  pursuitId: string;
  /** The role he is looking at, or null for the board as a whole. */
  role: JHRole | null;
  /** Drop back to talking about the board. */
  onClearRole: () => void;
  /** Close the cockpit before navigating away, so the modal never unmounts
   *  mid-navigation and leaves the page with a locked scroll. */
  onLeave: () => void;
}) {
  const router = useAppRouter();
  const live = useJobHuntSession(pursuitId);
  const [draft, setDraft] = useState("");
  const [handingOff, setHandingOff] = useState(false);
  const inputRef = useRef<ConversationComposerHandle | null>(null);

  // The anchor as the agent last heard it. `undefined` means it has never been
  // told, which is why it is not simply `null` (the board is a real anchor).
  const toldRef = useRef<string | null | undefined>(undefined);

  async function submit() {
    const text = draft.trim();
    if (!text || live.busy) return;
    setDraft("");

    const anchor = role?.id ?? null;
    const changed = toldRef.current !== anchor;
    toldRef.current = anchor;

    await live.ask(text, changed ? { send: `${anchorLine(role)}\n\n${text}` } : undefined);
  }

  /* Hand off to the full workspace on the SAME session, so the conversation
   * continues rather than restarting. Minting it here when he never spoke is
   * the same seed the dashboard's Discuss-with-Jarvis performs. */
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

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <ConversationScroll follow={[live.messages, role?.id ?? null]}>
        {live.messages.length === 0 ? <AgentLine text={opener(role)} /> : null}
        {live.messages.map((m) => (
          <LiveLine key={m.id} message={m} />
        ))}
        {live.busy ? <ThinkingLine /> : null}
      </ConversationScroll>

      <ConversationFooter>
        {role ? (
          <p className="pb-2 font-mono text-[11px] uppercase tracking-[0.08em] text-quiet">
            <span className="sr-only">Talking about </span>
            {role.company} · {role.role_title}
          </p>
        ) : null}

        <ConversationComposer
          id="jh-chat-input"
          ref={inputRef}
          value={draft}
          onChange={setDraft}
          onSubmit={() => void submit()}
          ariaLabel={
            role
              ? `Ask Jarvis about ${role.company}, ${role.role_title}`
              : "Ask Jarvis about the board"
          }
          placeholder={role ? "Ask about this role" : "Ask about the board"}
          disabled={live.busy || draft.trim().length === 0}
          busy={live.busy}
          sendLabel="Ask Jarvis"
        />

        <QuietRow>
          {role ? (
            <QuietAction
              onClick={() => {
                onClearRole();
                inputRef.current?.focus();
              }}
            >
              Ask about the whole board
            </QuietAction>
          ) : null}
          <QuietAction onClick={() => void continueInWorkspace()}>
            {handingOff ? <Spinner className="size-3.5" aria-hidden /> : null}
            Continue in the workspace
          </QuietAction>
          {!live.connected ? (
            <span className="text-warning">
              I am not connected right now, so nothing you send will reach me. Your board is
              untouched.
            </span>
          ) : null}
        </QuietRow>
      </ConversationFooter>
    </div>
  );
}
