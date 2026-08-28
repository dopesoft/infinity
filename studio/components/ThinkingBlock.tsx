"use client";

import { ActivityStepFor } from "@/components/chat/ActivityStep";
import type { ChatMessage } from "@/hooks/useChat";

/**
 * ThinkingBlock — compatibility shim over the activity ledger (MAJORDOMO §6).
 *
 * A thought is a line in the ledger now, the same as any other step. The
 * behaviour the old block owned is preserved in
 * `components/chat/ActivityStep.tsx`: the trace streams with the preview
 * pinned to its own bottom, it wears the fade mask while live, it opens
 * itself while pending and collapses when the agent moves on (the row's own
 * elapsed is the "Thought for 11s" readout), and a thought that ended with no
 * content renders nothing at all.
 */
export function ThinkingBlock({ message }: { message: ChatMessage }) {
  return <ActivityStepFor message={message} />;
}
