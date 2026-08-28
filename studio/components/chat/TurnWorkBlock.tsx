"use client";

import type { ReactNode } from "react";

import { ActivityLedger } from "@/components/chat/ActivityLedger";
import { activityIsLive, coalesce } from "@/lib/chat/activity";
import type { ChatMessage } from "@/hooks/useChat";

/**
 * TurnWorkBlock — compatibility shim over `ActivityLedger` (MAJORDOMO §6).
 *
 * The turn-folding UI now lives in `components/chat/ActivityLedger.tsx`, which
 * builds its own rows from `lib/chat/activity.ts` instead of being handed a
 * render function. `ConversationStream` imports the ledger directly; this file
 * stays so any other caller keeps compiling and gets the new surface rather
 * than the old stack of cards.
 *
 * `renderItem` is accepted and ignored: the ledger decides how a step renders,
 * because that decision is the whole point of the vocabulary layer. Dropping
 * the prop would break callers for no gain.
 */
export function TurnWorkBlock(props: {
  items: ChatMessage[];
  renderItem?: (m: ChatMessage) => ReactNode;
}) {
  return <ActivityLedger items={props.items} />;
}

/** True while any step in the turn is still working. */
export function isWorkLive(items: ChatMessage[]): boolean {
  return activityIsLive(coalesce(items));
}
