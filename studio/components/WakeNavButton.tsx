"use client";

import { useEffect, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { Ear } from "lucide-react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useWakeWord } from "@/lib/voice/use-wake-word";
import { emitWakeDetected, onVoiceActive } from "@/lib/voice/wake-bus";
import { cn } from "@/lib/utils";

/* WakeNavButton - the global "hey Jarvis" toggle in the header. Renders as
 * the FIRST item of the header's right cluster, which places it left of the
 * kebab menu on desktop and left of the hamburger on mobile (the desktop
 * span between them is display-hidden there).
 *
 * Exactly ONE instance may mount: it owns the wake-word engine (mic stream
 * + ONNX sessions), so duplicating it would double inference and fight over
 * the mic. Detection is global - on any page, "hey Jarvis" routes to
 * /live?voice=1 (the composer auto-starts listening on arrival); if the
 * composer is already mounted, the jarvis:wake event starts the session in
 * place without a navigation. */

export function WakeNavButton() {
  const router = useRouter();
  const pathname = usePathname();
  // Suspend while the composer's realtime session holds the mic.
  const [voiceActive, setVoiceActive] = useState(false);
  useEffect(() => onVoiceActive(setVoiceActive), []);

  const wake = useWakeWord({
    suspended: voiceActive,
    onWake: () => {
      // Composer mounted (on /live) → start the session in place.
      emitWakeDetected();
      // Elsewhere → land on /live in auto-listen mode.
      if (!pathname?.startsWith("/live")) router.push("/live?voice=1");
    },
  });

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          onClick={wake.toggle}
          className={cn(
            "inline-flex size-11 items-center justify-center rounded-md transition-colors lg:size-9",
            wake.status === "listening" || wake.status === "paused"
              ? "bg-info/15 text-info"
              : wake.status === "error"
                ? "text-danger hover:bg-danger/10"
                : "text-muted-foreground hover:bg-accent hover:text-foreground",
          )}
          aria-label={
            wake.enabled
              ? "Disable wake word"
              : "Enable wake word - say 'hey Jarvis' to start talking"
          }
          aria-pressed={wake.enabled}
        >
          <Ear
            className={cn("size-5 lg:size-4", wake.status === "listening" && "animate-pulse")}
          />
        </button>
      </TooltipTrigger>
      <TooltipContent side="bottom">
        {wake.status === "listening"
          ? "Listening for “hey Jarvis” - tap to disable"
          : wake.status === "paused"
            ? "Wake word paused while voice is live"
            : wake.status === "error"
              ? `Wake word failed: ${wake.error ?? "unknown"} - tap to turn off, tap again to retry`
              : wake.status === "loading"
                ? "Arming wake word…"
                : "Say “hey Jarvis” hands-free - tap to enable"}
      </TooltipContent>
    </Tooltip>
  );
}
