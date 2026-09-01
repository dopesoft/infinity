"use client";

import Link from "next/link";
import { Infinity as InfinityIcon } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
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
          "grid shrink-0 place-items-center rounded-lg transition-opacity",
          rail
            ? "mb-2.5 size-7 bg-foreground text-background hover:opacity-80"
            : "size-9 text-foreground",
        )}
      >
        {loading ? (
          <Spinner className={cn("animate-in fade-in duration-200", rail ? "size-4":"size-5")} />
        ) : (
          <InfinityIcon
            aria-hidden
            className={cn("animate-in fade-in duration-200", rail ? "size-4" : "size-5")}
          />
        )}
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
