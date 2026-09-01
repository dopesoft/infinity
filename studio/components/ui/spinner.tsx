"use client";

import { LoaderPinwheel } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * Spinner — the ONE "something is working" mark in Studio.
 *
 * Every busy state in the app used to draw its own: `Loader2` at four
 * different sizes, a `RefreshCw` rotating on one screen, a bare
 * `animate-spin` on another. Same fact, four shapes, so nothing about a wait
 * looked like anything else about a wait. This is the shape now, and it is
 * the shape the app mark takes while a screen is loading — see <AppMark>.
 *
 * `size-4` is the default because that is the size it sits at inside a
 * button and inside the rail's mark. Override with `className="size-3.5"`
 * for a dense row; do NOT reach past this for another spinning glyph.
 *
 * NEVER put an enter animation (`animate-in`, `fade-in`, `zoom-in`) on this
 * element or pass one through `className`. `animate-in` sets
 * `animation-name: enter` and lands LATER in the stylesheet than the rotation,
 * so the glyph appears, fades, and never turns — which is exactly the "it
 * just flashes, it doesn't even spin" bug. One CSS animation per element:
 * fade the WRAPPER, spin the glyph.
 *
 * `aria-hidden` by default: a spinner is decoration, and the sentence that
 * says what is happening belongs next to it (or in the `aria-live` region
 * its owner renders). Pass `aria-hidden={false}` with a label only when the
 * spinner is genuinely the only thing on screen.
 */
export function Spinner({
  className,
  ...props
}: React.ComponentProps<typeof LoaderPinwheel>) {
  return (
    <LoaderPinwheel
      aria-hidden
      className={cn("size-4 shrink-0 animate-pinwheel", className)}
      {...props}
    />
  );
}
