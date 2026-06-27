"use client";

import { useEffect, useState } from "react";
import { ConversationStream } from "@/components/ConversationStream";
import { CodingSessionBanner } from "@/components/CodingSessionBanner";
import { BackgroundJobDock } from "@/components/BackgroundJobDock";
import { PendingApprovalsDock } from "@/components/PendingApprovalsDock";
import { PromptInputBox } from "@/components/ui/ai-prompt-box";
import { useGlobalModel } from "@/lib/use-model";
import type { useChat } from "@/hooks/useChat";

type ChatHook = ReturnType<typeof useChat>;

/**
 * WorkspaceChatColumn - the chat surface in the unified /live workspace.
 *
 * Layout: coding-session banner (one-shot dismissable) at the top, conversation
 * stream filling the middle, AI prompt box pinned to the bottom inside a
 * keyboard-safe container. The model chip is wired through `useGlobalModel`
 * so cycling on the chip writes to Core's settings store (single source of
 * truth) and the Settings page stays synchronized in real time.
 */
export function WorkspaceChatColumn({
  chat,
  minimalComposer = false,
}: {
  chat: ChatHook;
  minimalComposer?: boolean;
}) {
  const { setting, setModel } = useGlobalModel();

  // steal C: the boss's effort pin ("auto" = let Jarvis size each turn, or a
  // level). Persisted to localStorage so it survives reload / navigation on this
  // device; "auto" by default so nothing costs more out of the box. Hydrated
  // after mount to keep SSR markup deterministic.
  const [effort, setEffort] = useState<string>("auto");
  useEffect(() => {
    try {
      const stored = window.localStorage.getItem("infinity:effort");
      if (stored) setEffort(stored);
    } catch {
      /* private mode / quota — best-effort only */
    }
  }, []);
  const changeEffort = (level: string) => {
    setEffort(level);
    try {
      window.localStorage.setItem("infinity:effort", level);
    } catch {
      /* best-effort */
    }
  };

  return (
    // overflow-x-hidden here is the page-level guard: even if a child
    // somewhere has runaway intrinsic width (a tool result with an
    // unwrapped long string, a code block, an over-eager textarea),
    // the chat column clips it instead of pushing the whole page
    // horizontally and exposing the chat input / conversation as
    // "shifted right" on mobile. The descendants that need to scroll
    // their own content (the conversation, code blocks) handle that
    // inside themselves.
    <div className="flex h-full min-h-0 min-w-0 flex-col overflow-x-hidden">
      <CodingSessionBanner sessionId={chat.sessionId} />
      {/* Outer wrapper must be a flex container so ConversationStream's
          empty state (which uses flex-1 to vertically center its content)
          can actually expand. Without flex here, flex-1 has no effect and
          the empty state collapsed to the top edge. */}
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
        {/* onQuickReply routes the "Approve & fix" action on heartbeat
            finding cards through chat.send, so the agent acts on the
            finding in this same session. */}
        <ConversationStream messages={chat.messages} onQuickReply={chat.send} working={chat.isStreaming} />
      </div>
      {/* Pinned plan status — the chat's live window onto the active plan
          (the same one the dashboard Agent Work board shows), plus any
          background_build telemetry. Sits above the composer so the boss can
          keep chatting and still watch progress. Renders nothing when no plan
          is in flight and no background job is running. */}
      <PendingApprovalsDock />
      <BackgroundJobDock sessionId={chat.sessionId} />
      <div className="min-w-0 shrink-0 border-t bg-background/95 px-3 pt-2 backdrop-blur supports-[backdrop-filter]:bg-background/80 sm:px-4 keyboard-safe-bottom">
        <PromptInputBox
          onSend={(text, files) => {
            const t = text.trim();
            if (!t && (!files || files.length === 0)) return;
            // `send` itself decides whether to start a new turn or
            // queue a steer based on chat.isStreaming. The model the
            // turn runs against is resolved server-side from the
            // settings store (driven by this chip + the Settings page),
            // so the WS frame stays a plain {type, session, content}.
            void chat.send(t, files, { effort });
          }}
          onSlash={(cmd) => {
            const c = cmd.toLowerCase();
            if (c === "/new") {
              chat.newSession();
              return true;
            }
            if (c === "/clear") {
              chat.clear();
              return true;
            }
            return false;
          }}
          onStop={chat.interrupt}
          isLoading={chat.isStreaming}
          disabled={chat.status !== "connected"}
          placeholder="ask me anything.."
          modelId={setting?.model ?? ""}
          vendorId={setting?.provider ?? ""}
          effort={effort}
          appliedEffort={chat.appliedEffort}
          onEffortChange={changeEffort}
          sessionId={chat.sessionId}
          onModelChange={(nextId) => {
            // The chip pushes back a full model id; we PUT it straight
            // through to Core's settings store. The hook broadcasts the
            // change so the Settings page reflects it instantly without
            // a roundtrip on its end.
            void setModel(nextId);
          }}
          onVoiceSend={(t) => void chat.send(t, undefined, { voice: true, effort })}
          minimal={minimalComposer}
        />
      </div>
    </div>
  );
}
