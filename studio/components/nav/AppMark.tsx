"use client";

import Link from "next/link";
import { Infinity as InfinityIcon } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { PRESS_ICON } from "@/components/ui/press";
import { useAppLoading } from "@/lib/loading";
import { cn } from "@/lib/utils";

/**
 * AppMark — the Infinity mark, and the app's loading state, in one glyph.
 *
 * WHY THE LOGO AND NOT A BAR
 *
 * The complaint this answers: "when I click something, sometimes it takes a
 * sec to actually go there so I click it again." He needs an answer to "did
 * that register?" in the place his eye already is, and the mark is the one
 * fixed point on every screen — top-left of the rail on the desktop, top-left
 * of the bar on a phone. A progress bar under the header would be a second
 * piece of chrome saying what this already says.
 *
 * Both breakpoints route through this component rather than each drawing an
 * <InfinityIcon> of their own, because the moment two screens own the same
 * decision they drift: one would spin, one would not, and which one would
 * depend on which file someone edited last.
 *
 * The two variants are the two frames it sits in, and they are a REQUIRED
 * discriminator, not an options bag — the rail's mark is an inverted 28px
 * chip, the phone bar's is a plain 36px touch target, and there is no third
 * shape to invent.
 *
 * It never spins for the agent. Jarvis thinking, a tool running, a reply
 * streaming — all of that has its own surface. See `useAppLoading`.
 */
export function AppMark({ variant }: { variant: "rail" | "bar" }) {
  const loading = useAppLoading();
  const rail = variant === "rail";

  return (
    <>
      <Link
        href="/"
        aria-label="Infinity home"
        className={cn(
          "grid shrink-0 place-items-center rounded-lg",
          PRESS_ICON,
          rail ? "mb-2.5 size-7" : "size-9",
          // The rail's mark is an inverted chip; the SPINNER is not. A black
          // tile with a small light glyph turning inside it reads as a solid
          // block that flickers, and it is the loudest shape on a screen made
          // of hairlines. While it turns, the chip comes off and the pinwheel
          // is just ink, so the movement is the whole of what you see.
          loading
            ? "text-foreground"
            : rail
              ? "bg-foreground text-background hover:opacity-80"
              : "text-foreground",
        )}
      >
        {/* The fade lives on this wrapper, NEVER on the glyph inside it.
            `animate-in` and the rotation both set `animation`, and the enter
            animation wins the cascade, so a spinner wearing both fades in and
            then sits perfectly still. That was the "it just flashes, it
            doesn't even spin" bug. */}
        <span className="grid animate-in fade-in duration-200 place-items-center">
          {loading ? (
            <Spinner className={rail ? "size-5" : "size-[22px]"} />
          ) : (
            <InfinityIcon aria-hidden className={rail ? "size-4" : "size-5"} />
          )}
        </span>
      </Link>

      {/* Says out loud what the glyph says visually, once, for a screen
          reader — and stays out of the link's accessible name, which is why
          it is a sibling and not a child. */}
      <span role="status" aria-live="polite" className="sr-only">
        {loading ? "Loading" : ""}
      </span>
    </>
  );
}
