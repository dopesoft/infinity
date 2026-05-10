import { cn } from "@/lib/utils";
import type { ChatMessage } from "@/hooks/useChat";

function formatMs(ms?: number) {
  if (!ms) return "";
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

export function ChatBubble({ message }: { message: ChatMessage }) {
  if (message.role === "tool" || message.role === "thinking") return null; // ToolCallCard / ThinkingBlock render these
  const isUser = message.role === "user";

  if (message.error) {
    return (
      <div className="rounded-md border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-danger">
        {message.error}
      </div>
    );
  }

  return (
    <div className={cn("flex w-full gap-2", isUser ? "justify-end" : "justify-start")}>
      <div
        className={cn(
          "max-w-[88%] rounded-2xl px-3 py-2 text-[15px] leading-relaxed sm:max-w-[78%] sm:text-base",
          isUser
            ? "rounded-tr-sm bg-primary text-primary-foreground"
            : "rounded-tl-sm bg-muted text-foreground",
        )}
      >
        <div className="whitespace-pre-wrap break-words">
          {message.text}
          {message.pending && (
            <span className="ml-0.5 inline-block size-2 animate-pulse rounded-full bg-current align-middle opacity-60" />
          )}
        </div>
        {!isUser && !message.pending && (message.outputTokens || message.latencyMs) ? (
          <div className="mt-1 flex justify-end gap-2 text-[11px] text-muted-foreground">
            {message.outputTokens ? <span>{message.outputTokens} tok</span> : null}
            {message.latencyMs ? <span>{formatMs(message.latencyMs)}</span> : null}
          </div>
        ) : null}
      </div>
    </div>
  );
}
