"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { ArrowDown, Sparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ChatBubble } from "@/components/ChatBubble";
import { AgentTeamCard } from "@/components/AgentTeamCard";
import { SkillProposalCard } from "@/components/SkillProposalCard";
import { PlanProposalCard, PLAN_PROPOSAL_TOOLS } from "@/components/PlanProposalCard";
import { ActivityLedger } from "@/components/chat/ActivityLedger";
import { ActivityStepFor } from "@/components/chat/ActivityStep";
import { DashboardContextCard } from "@/components/DashboardContextCard";
import { WorkingIndicator } from "@/components/WorkingIndicator";
import { describeStep } from "@/lib/chat/activity";
import { workingLabel, type LivenessState } from "@/lib/chat/liveness";
import { useNow } from "@/lib/useNow";
import type { ChatMessage } from "@/hooks/useChat";

const SKILL_TOOL_NAMES = new Set(["skill_propose", "skill_optimize"]);

// workingState decides whether to show the persistent "working" row and what
// it should say. The row is the consistent activity cue for the gaps the
// per-message affordances don't cover (after a tool result, while the model
// reasons before the next step). It is suppressed when a live affordance is
// already on screen: a pending thinking block (owns its own shimmer) or a
// tool call parked awaiting the boss's approval (the agent is blocked, not
// working).
//
// What it SAYS comes from the server's own account of the turn when there is
// one (liveness: the phase, the tool in flight, the turn's real clock, whether
// we are reconnecting or stopping) - "Thinking · 2m 40s", "Running a command
// · 12s", "Reconnecting…" - and falls back to the shape of the transcript only
// against a core that does not report it.
function workingState(
  messages: ChatMessage[],
  working: boolean,
  liveness?: LivenessState,
  now = 0,
): { show: boolean; label: string } {
  if (!working) return { show: false, label: "" };
  const last = messages[messages.length - 1];
  if (last) {
    // The trailing message belongs to a ledger, and a live ledger's headline
    // IS the working row - it shimmers and it carries the one spinner (§6).
    // Rendering this row too would put two "still working" signals, and two
    // spinners, on the same screen.
    if (isFoldable(last)) return { show: false, label: "" };
    if (last.role === "tool" && last.pending && last.toolCall?.awaiting_approval) {
      return { show: false, label: "" };
    }
  }
  if (liveness && liveness.inFlight && liveness.phase) {
    return {
      show: true,
      label: workingLabel(liveness, now || Date.now(), (tool) => describeStep({ id: "", name: tool }).verb),
    };
  }
  if (last) {
    if (last.role === "tool" && last.pending) {
      // A decision card mid-flight (a plan being laid out, a team starting).
      // The label comes from the vocabulary, never from the tool id.
      return { show: true, label: describeStep(last.toolCall).verb };
    }
    if (last.role === "assistant" && last.pending) {
      return { show: true, label: "Responding…" };
    }
  }
  return { show: true, label: "Working…" };
}

// Decision cards the boss must always see at the top level (never folded).
function isDecisionCard(m: ChatMessage): boolean {
  const name = m.toolCall?.name ?? "";
  if (name === "agent_team_start" || SKILL_TOOL_NAMES.has(name)) return true;
  if (PLAN_PROPOSAL_TOOLS.has(name)) {
    const out = typeof m.toolResult?.output === "string" ? m.toolResult.output : "";
    return out.includes('"proposed"');
  }
  return false;
}

// Working churn: tool calls, thinking blocks and the interim narration that
// streams before a tool call. Folded into one ActivityLedger per run so the
// transcript reads message → what he did (one line) → reply, instead of a
// wall of narration and cards (2026-08-26: "zillions of messages").
function isFoldable(m: ChatMessage): boolean {
  if (m.role === "tool" && m.toolCall) return !isDecisionCard(m);
  if (m.role === "thinking") return true;
  return m.role === "assistant" && !!m.interim && !m.error;
}

function renderConversation(messages: ChatMessage[], onQuickReply?: (text: string) => void): ReactNode[] {
  const renderMessage = (m: ChatMessage): ReactNode => (
    <div key={m.id} className="min-w-0 max-w-full" data-message>
            {m.role === "tool" ? (
              // Skill-pipeline tool calls (skill_propose, skill_optimize)
              // render as a rich proposal card so "new skill proposed" is
              // glanceable. Everything else falls back to the generic
              // tool-call card.
              m.toolCall?.name === "agent_team_start" ? (
                <div className="flex justify-start">
                  <div className="w-full min-w-0 max-w-full sm:max-w-[80%]">
                    <AgentTeamCard message={m} />
                  </div>
                </div>
              ) : SKILL_TOOL_NAMES.has(m.toolCall?.name ?? "") ? (
                <div className="flex justify-start">
                  <div className="w-full min-w-0 max-w-full sm:max-w-[80%]">
                    <SkillProposalCard message={m} />
                  </div>
                </div>
              ) : PLAN_PROPOSAL_TOOLS.has(m.toolCall?.name ?? "") ? (
                // A plan laid out while the boss is talking it through is a
                // PROPOSAL: the card carries Go ahead / Not yet and persists
                // on reload (it rebuilds from the PostToolUse row), so the
                // read-out is always there to scroll back to.
                <div className="flex justify-start">
                  <div className="w-full min-w-0 max-w-full sm:max-w-[80%]">
                    <PlanProposalCard message={m} onQuickReply={onQuickReply} />
                  </div>
                </div>
              ) : (
                // A step that reached the top level on its own (a tool
                // message with nothing to fold it into). Same left-anchored
                // column as the ledger and the assistant bubble, so the
                // "agent voice" rail reads as one thing.
                <div className="flex justify-start">
                  {/* Full column, like the ledger: a step that reached the top
                      level on its own is still WORK, not a message. */}
                  <div className="w-full min-w-0 max-w-full">
                    <ActivityStepFor message={m} />
                  </div>
                </div>
              )
            ) : m.role === "thinking" ? (
              <div className="flex justify-start">
                {/* Work, not a message: full column (see ActivityLedger). */}
                <div className="w-full min-w-0 max-w-full">
                  <ActivityStepFor message={m} />
                </div>
              </div>
            ) : m.seeded ? (
              // Discuss-with-Jarvis context block - a left-anchored card
              // (same rhythm as the thinking / skill cards) so it reads as
              // "something the boss brought in", not a typed user message.
              <div className="flex justify-start">
                <div className="w-full min-w-0 max-w-full sm:max-w-[80%]">
                  <DashboardContextCard message={m} onQuickReply={onQuickReply} />
                </div>
              </div>
            ) : (
              <ChatBubble message={m} onQuickReply={onQuickReply} />
            )}
    </div>
  );
  const out: ReactNode[] = [];
  let group: ChatMessage[] = [];
  const flush = () => {
    if (group.length === 0) return;
    const fold = group.length >= 2 || group.some((m) => m.role === "tool");
    if (fold) {
      out.push(
        <div key={`work-${group[0].id}`} className="min-w-0 max-w-full" data-message>
          <ActivityLedger items={group} />
        </div>,
      );
    } else {
      out.push(...group.map(renderMessage));
    }
    group = [];
  };
  for (const m of messages) {
    if (isFoldable(m)) {
      group.push(m);
    } else {
      flush();
      out.push(renderMessage(m));
    }
  }
  flush();
  return out;
}

export function ConversationStream({
  messages,
  // onQuickReply sends a canned message into the current session. Used by
  // the "Approve & fix" action on heartbeat finding cards - it routes
  // through chat.send so the agent acts in this same conversation.
  onQuickReply,
  // working is the turn-in-flight flag (chat.isStreaming). Drives the
  // persistent WorkingIndicator so the boss always has a "still going" cue.
  working = false,
  // liveness is the server's account of the turn (chat.liveness): what it
  // is doing and for how long. The working row's words come from here.
  liveness,
}: {
  messages: ChatMessage[];
  onQuickReply?: (text: string) => void;
  working?: boolean;
  liveness?: LivenessState;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const contentRef = useRef<HTMLDivElement>(null);
  const [showJump, setShowJump] = useState(false);
  const stickToBottomRef = useRef(true);
  // The row's clock ticks once a second while a turn is in flight.
  const now = useNow(working && !!liveness?.inFlight);
  const work = workingState(messages, working, liveness, now);
  // Which branch renders. Load-bearing for the observer effect below.
  const hasMessages = messages.length > 0;

  // WHY A ResizeObserver AND NOT JUST AN EFFECT ON `messages`.
  //
  // The transcript's height changes constantly WITHOUT the message list
  // changing identity: a ledger row streams more output, a thinking trace
  // grows line by line, a row is expanded, and - the one that hurt most - the
  // plan dock above the composer opens and takes height away from this
  // scroller. None of those re-run an effect keyed on `messages`, so the
  // content grew below the fold, nothing re-pinned, and "Jump to latest" sat
  // there permanently while the boss had to scroll by hand to watch his own
  // agent work.
  //
  // Observing the CONTENT (it got taller) and the CONTAINER (it got shorter)
  // covers every one of those causes at once, including ones added later,
  // which an ever-growing dependency array never would.
  // ...and why this effect depends on `hasMessages`. It used to be `[]`, and
  // that quietly disabled the entire mechanism above.
  //
  // A session starts with no messages, so the FIRST render returns the
  // empty-state branch: there is no scroller and no content node yet, so this
  // effect observed a placeholder div and `contentRef.current` was still null.
  // The moment the first message arrived the component swapped to the real
  // scroller - new DOM nodes, new refs - but with `[]` the effect never re-ran,
  // so the observer spent the whole session watching a detached element that
  // could never resize. Nothing was left to re-pin except the message-keyed
  // effect below, which does not fire when a tool call streams its output into
  // a row that already exists or when a ledger row expands. Hence: new rows
  // scrolled, tool calls did not, and "Jump to latest" sat there through the
  // whole turn.
  //
  // `hasMessages` is the only condition that swaps the branch, so re-running on
  // it re-binds the observer to the nodes that actually exist.
  useEffect(() => {
    const el = scrollRef.current;
    const content = contentRef.current;
    if (!el) return;

    const pin = () => {
      if (!stickToBottomRef.current) return;
      // scrollTo with no behavior is instant, which is what we want while
      // content streams - a smooth scroll would never catch up.
      el.scrollTop = el.scrollHeight;
    };

    pin();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(pin);
    if (content) ro.observe(content);
    ro.observe(el);
    return () => ro.disconnect();
  }, [hasMessages]);

  // Message-driven re-pin as well: a brand-new row must land pinned even in
  // the frame before the observer fires.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el || !stickToBottomRef.current) return;
    el.scrollTop = el.scrollHeight;
  }, [messages, work.show, work.label]);

  function onScroll() {
    const el = scrollRef.current;
    if (!el) return;
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    // A generous threshold: while a ledger row is streaming, the content can
    // grow by more than a tight margin between two scroll events, and a
    // too-tight test would read that growth as "the boss scrolled up" and
    // unstick a view he never touched. That is the bug that made the jump
    // button appear on its own.
    const stick = distanceFromBottom < 160;
    stickToBottomRef.current = stick;
    setShowJump(!stick);
  }

  function jumpToBottom() {
    const el = scrollRef.current;
    if (!el) return;
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
    stickToBottomRef.current = true;
    setShowJump(false);
  }

  if (!hasMessages) {
    return (
      // The stream area excludes the composer, while Files / Preview
      // do not have a bottom composer taking space. Nudge the desktop
      // empty state down so the placeholders line up across columns.
      //
      // Deliberately NOT carrying `scrollRef`: this element does not scroll
      // and has no content to pin. It used to, which made the two branches
      // read as interchangeable and hid the fact that the observer above was
      // binding to this placeholder instead of the real scroller.
      <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center lg:translate-y-[6.5rem]">
        <span className="inline-flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
          <Sparkles className="size-5" aria-hidden />
        </span>
        <div className="max-w-md space-y-1">
          <h3 className="text-sm font-semibold">A fresh session</h3>
          <p className="text-xs leading-relaxed text-muted-foreground">
            Tell Jarvis what to build, write, or think through.
          </p>
        </div>
      </div>
    );
  }

  return (
    // min-w-0 on the flex column AND on the inner scroller is what stops
    // a long un-wrapping line (e.g. a voice transcript without natural
    // word breaks at the right place) from pushing the column wider than
    // its parent. Without it, the message bubbles' max-w-[%] computes
    // against an inflated parent and the bubble overflows the viewport.
    <div className="relative flex min-h-0 min-w-0 flex-1 flex-col">
      <div
        ref={scrollRef}
        onScroll={onScroll}
        // pb-8 gives the last message (often an error or thinking bubble) clear
        // breathing room before the composer - otherwise auto-scroll
        // pins it flush against the prompt input with zero padding.
        //
        // overflow-x-hidden is load-bearing, not cosmetic: a scroll region
        // declared `overflow-y-auto` with no explicit overflow-x computes
        // overflow-x to `auto` (CSS spec: a non-visible value on one axis
        // promotes `visible` on the other to `auto`). That silently makes
        // THIS element the chat's one horizontally-scrollable surface, so a
        // long URL/path/token whose min-content width briefly exceeds the
        // column - common mid-stream before a break opportunity lands -
        // scrolls the whole conversation sideways no matter how hardened the
        // message renderers are. Pinning overflow-x to hidden stops the
        // promotion: y scrolls, x clips. Inner scrollers that legitimately
        // need to pan (code blocks, wide tables) own their own
        // overflow-x-auto inside their own clipped wrappers, so this never
        // steals their internal scroll.
        className="flex-1 min-w-0 space-y-3 overflow-y-auto overflow-x-hidden px-3 pt-3 pb-8 scroll-touch sm:px-4"
      >
        {/* max-w-stream: the conversation is a column you read, not a band
            that stretches to whatever the window happens to be. The scroller
            above stays full width so the scrollbar sits at the edge; only the
            content is capped, and in Split/Build the column is already
            narrower than the cap so this costs nothing there. */}
        <div ref={contentRef} className="mx-auto min-w-0 w-full max-w-stream space-y-3">
          {renderConversation(messages, onQuickReply)}
          {work.show && (
            <div className="min-w-0 max-w-full" data-message>
              <div className="w-full min-w-0 max-w-full sm:max-w-[80%]">
                <WorkingIndicator label={work.label} />
              </div>
            </div>
          )}
        </div>
      </div>
      {showJump && (
        <div className="pointer-events-none absolute inset-x-0 bottom-2 flex justify-center">
          <Button
            size="sm"
            variant="secondary"
            className="pointer-events-auto shadow-sm"
            onClick={jumpToBottom}
          >
            <ArrowDown className="size-4" /> Jump to latest
          </Button>
        </div>
      )}
    </div>
  );
}
