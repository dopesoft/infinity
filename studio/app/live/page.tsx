"use client";

import { TabFrame } from "@/components/TabFrame";
import { SessionHeader } from "@/components/SessionHeader";
import { ConversationStream } from "@/components/ConversationStream";
import { Composer } from "@/components/Composer";
import { useChat } from "@/hooks/useChat";

export default function LivePage() {
  const chat = useChat();

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <SessionHeader sessionId={chat.sessionId} onNew={chat.newSession} onClear={chat.clear} />
        <ConversationStream messages={chat.messages} />
        <Composer
          onSend={chat.send}
          onSlash={(cmd) => (cmd === "new" ? chat.newSession() : chat.clear())}
          disabled={chat.isStreaming || chat.status !== "connected"}
        />
      </div>
    </TabFrame>
  );
}
