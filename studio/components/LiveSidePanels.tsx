"use client";

import { IntentStream } from "@/components/IntentStream";
import { ActiveSkillsPanel } from "@/components/ActiveSkillsPanel";
import { ContextBudget } from "@/components/ContextBudget";
import { LastHeartbeatPanel } from "@/components/LastHeartbeatPanel";
import { TrustQueuePanel } from "@/components/TrustQueuePanel";
import { SystemPanel } from "@/components/SystemPanel";
import type { ChatMessage } from "@/hooks/useChat";

export function LeftPanels({
  messages,
  usedTokens,
}: {
  messages: ChatMessage[];
  usedTokens: number;
}) {
  return (
    <div className="flex flex-col gap-3">
      <IntentStream />
      <ActiveSkillsPanel messages={messages} />
      <ContextBudget usedTokens={usedTokens} />
    </div>
  );
}

export function RightPanels({ wsConnected }: { wsConnected: boolean }) {
  return (
    <div className="flex flex-col gap-3">
      <LastHeartbeatPanel />
      <TrustQueuePanel />
      <SystemPanel wsConnected={wsConnected} />
    </div>
  );
}

export function MobileStatusStack({
  messages,
  usedTokens,
  wsConnected,
}: {
  messages: ChatMessage[];
  usedTokens: number;
  wsConnected: boolean;
}) {
  return (
    <div className="flex flex-col gap-3 px-3 pb-3 sm:px-4">
      <IntentStream />
      <ActiveSkillsPanel messages={messages} />
      <ContextBudget usedTokens={usedTokens} />
      <LastHeartbeatPanel />
      <TrustQueuePanel />
      <SystemPanel wsConnected={wsConnected} />
    </div>
  );
}
