"use client";

import { useEffect, useState } from "react";
import { LayoutDashboard } from "lucide-react";
import type { ChatMessage } from "@/hooks/useChat";
import { FindingActions } from "@/components/FindingActions";

// The context block Discuss-with-Jarvis injects renders as this card —
// NOT a plain user bubble — so the transcript makes clear it's something
// the boss brought in from the dashboard to deliberate on, not something
// they typed. Mirrors the heartbeat bubble's info-accent treatment.

// The agent reads the seed text as its opening prompt; the trailing line
// is a call-to-action for the model and is noise inside a card the boss
// is already looking at, so it's stripped from the display.
const CTA_RE = /\n+I want to discuss this with Jarvis\.\s*$/;
const ARTIFACT_MARKER = "\n\nArtifact:\n";
const HEADING_RE = /^Dashboard context for discussion\n?/;

function formatTime(ms: number): string {
  if (!ms) return "";
  return new Date(ms).toLocaleTimeString(undefined, {
    hour: "numeric",
    minute: "2-digit",
  });
}

export function DashboardContextCard({
  message,
  onQuickReply,
}: {
  message: ChatMessage;
  onQuickReply?: (text: string) => void;
}) {
  // Resolved client-side post-mount so the locale-formatted time never
  // diverges between server and client render.
  const [time, setTime] = useState("");
  useEffect(() => {
    setTime(formatTime(message.createdAt));
  }, [message.createdAt]);

  // Split the seed text into a human summary and the raw artifact JSON so
  // the JSON can live in a real code block instead of bleeding into prose.
  const cleaned = (message.text || "").replace(CTA_RE, "").trim();
  const markerAt = cleaned.indexOf(ARTIFACT_MARKER);
  const summary =
    (markerAt >= 0 ? cleaned.slice(0, markerAt) : cleaned)
      .replace(HEADING_RE, "")
      .trim();
  const artifact =
    markerAt >= 0 ? cleaned.slice(markerAt + ARTIFACT_MARKER.length).trim() : "";

  const kindLabel = message.seedKind || "item";

  return (
    <div className="min-w-0 rounded-2xl rounded-tl-sm border border-info/40 bg-info/5 px-3 py-2">
      <div className="mb-1.5 flex items-center gap-1 text-[10px] font-medium uppercase tracking-wide text-info">
        <LayoutDashboard className="size-3" />
        <span>From dashboard · {kindLabel}</span>
      </div>

      {summary && (
        <div className="whitespace-pre-wrap break-words text-xs leading-relaxed text-muted-foreground">
          {summary}
        </div>
      )}

      {artifact && (
        <pre className="scroll-touch mt-2 max-h-72 overflow-auto rounded-md border border-border bg-muted/60 px-2.5 py-2 text-[11px] leading-relaxed text-foreground">
          <code className="break-words [overflow-wrap:anywhere]">{artifact}</code>
        </pre>
      )}

      <div
        className="mt-1.5 text-[11px] text-muted-foreground"
        suppressHydrationWarning
      >
        {time ? `${time} · ` : ""}brought in for discussion
      </div>

      {/* When the seeded item is an open curiosity finding, offer the
          one-tap "Approve & fix" — it tells the agent to act on the
          finding in this same conversation and confirm what it changed. */}
      {message.curiosityId && (
        <FindingActions
          curiosityId={message.curiosityId}
          onSend={onQuickReply}
        />
      )}
    </div>
  );
}
