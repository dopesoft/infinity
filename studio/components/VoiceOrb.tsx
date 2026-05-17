"use client";

import { cn } from "@/lib/utils";
import type { VoiceStatus } from "@/lib/voice/client";

/**
 * VoiceOrb is the in-composer indicator while voice mode is live. It
 * pulses with the smoothed mic+output audio level and tints based on
 * the realtime status:
 *
 *   listening          → info  (calm blue)
 *   user-speaking      → info  (brighter blue, larger pulse)
 *   assistant-speaking → warning (amber)
 *   tool-running       → success (green)
 *   connecting         → muted (gray)
 *   error              → danger (red)
 *
 * The element sits inline at the start of the composer textarea row, so
 * the conversation stream above remains fully visible - voice mode just
 * swaps what's inside the input bar, never overlays it.
 */
export function VoiceOrb({
  status,
  level,
  className,
}: {
  status: VoiceStatus;
  level: number; // 0..1
  className?: string;
}) {
  const tone = toneFor(status);
  // Scale the pulse from a baseline so the orb never collapses to a
  // dot even when level is zero. Capped at 1.35 to keep the composer
  // height stable.
  const scale = 1 + Math.min(0.35, level * 0.5);
  const opacity = 0.65 + Math.min(0.35, level * 0.6);

  return (
    <span
      className={cn(
        "relative inline-flex h-6 w-6 items-center justify-center",
        className,
      )}
      aria-hidden
    >
      {/* Halo - softer, larger, follows the level peak. */}
      <span
        className={cn(
          "absolute inset-0 rounded-full transition-opacity duration-150",
          tone.halo,
        )}
        style={{ transform: `scale(${1.1 * scale})`, opacity: opacity * 0.55 }}
      />
      {/* Core dot - solid color, scales tighter. */}
      <span
        className={cn(
          "h-3 w-3 rounded-full transition-transform duration-150",
          tone.core,
          status === "connecting" && "animate-pulse",
        )}
        style={{ transform: `scale(${scale})`, opacity }}
      />
    </span>
  );
}

function toneFor(status: VoiceStatus): { core: string; halo: string } {
  switch (status) {
    case "user-speaking":
    case "listening":
      return { core: "bg-info", halo: "bg-info/30" };
    case "assistant-speaking":
      return { core: "bg-warning", halo: "bg-warning/30" };
    case "tool-running":
      return { core: "bg-success", halo: "bg-success/30" };
    case "error":
      return { core: "bg-danger", halo: "bg-danger/30" };
    case "connecting":
    case "requesting-permission":
      return { core: "bg-muted-foreground", halo: "bg-muted-foreground/30" };
    case "idle":
    case "closed":
    default:
      return { core: "bg-muted-foreground/60", halo: "bg-muted-foreground/15" };
  }
}
