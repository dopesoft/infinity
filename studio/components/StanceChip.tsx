"use client";

import { Hammer, MessageCircle } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * StanceChip - a quiet read-back of what Jarvis understood the last message
 * to be: a conversation, or a work order. Not a mode switch (there is none);
 * it exists so a misread is visible the instant it happens instead of ten
 * minutes into a build the boss never asked for.
 */
export function StanceChip({ stance, reason }: { stance?: string; reason?: string }) {
  if (stance !== "discuss" && stance !== "work") return null;
  const talking = stance === "discuss";
  return (
    <div
      className={cn(
        "mb-2 inline-flex max-w-full items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11px] leading-none",
        talking
          ? "border-info/40 bg-info/10 text-info"
          : "border-border/60 bg-muted/60 text-muted-foreground",
      )}
      title={reason || undefined}
    >
      {talking ? <MessageCircle className="size-3" aria-hidden /> : <Hammer className="size-3" aria-hidden />}
      <span className="truncate">{talking ? "Talking it through, not building" : "Working on it"}</span>
    </div>
  );
}
