"use client";

import * as React from "react";
import { ChevronDown } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * Chip + ChipGroup - the toolbar control primitive.
 *
 * THE SHAPE. A recessed track (`bg-muted`) with a lifted chip sitting in it.
 * It started life as the Chat / Split / Build switch and is now the one shape
 * every small header control uses, because seven hand-rolled variants of it
 * had drifted to four heights, four text sizes, three border treatments and
 * three private copies of the same tone map.
 *
 * THE MODEL. A single control is a ChipGroup with ONE Chip in it. A segmented
 * control is a ChipGroup with several. That is the whole reason the status
 * chip and the layout switch line up to the pixel instead of merely looking
 * close: they are the same object.
 *
 * THE RULES, in order of how often they get broken:
 *
 *   1. Lifted means live or chosen. `raised` marks the current answer - the
 *      selected segment, the bridge actually in use, the state right now.
 *      Everything else rests flat until you touch it.
 *
 *   2. Colour rides the dot. The track and the chip stay neutral in every
 *      state, so a row of these reads as one instrument panel. `tone` colours
 *      the dot and nothing else.
 *
 *   3. The label only shouts when it should. `loud` moves the tone onto the
 *      text as well, and is for the three states that want you: thinking,
 *      needs you, and broken. Healthy and idle stay in ink. That restraint is
 *      what makes red mean something.
 *
 *   4. A dot never travels alone. The dot qualifies a word, it never replaces
 *      one. When space runs out the LABEL shortens; a chip must never collapse
 *      to a bare coloured light, which is a signal nobody can read.
 *
 * TOUCH. Expansion is vertical only (`before:inset-x-0 before:h-11`), matching
 * the convention in dashboard/Section.tsx. A chip therefore never steals the
 * tap of the chip beside it in the same track, which a centred `size-11`
 * target would.
 */

export type ChipTone = "neutral" | "success" | "info" | "warning" | "danger" | "brand";
export type ChipSize = "sm" | "md";

/** Dot colour per tone. Exported so status legends elsewhere reuse it rather
 *  than re-deriving a fourth private copy of the same map. */
export const chipDotClass: Record<ChipTone, string> = {
  neutral: "bg-muted-foreground/50",
  success: "bg-success",
  info: "bg-info",
  warning: "bg-warning",
  danger: "bg-danger",
  brand: "bg-brand",
};

/** Text colour per tone, applied only when `loud`. */
export const chipTextClass: Record<ChipTone, string> = {
  neutral: "text-foreground",
  success: "text-success",
  info: "text-info",
  warning: "text-warning",
  danger: "text-danger",
  brand: "text-brand",
};

const groupSize: Record<ChipSize, string> = {
  sm: "h-7 p-0.5",
  md: "h-8 p-0.5",
};

const chipSize: Record<ChipSize, string> = {
  sm: "h-6 gap-1.5 px-2 text-[11px] [&_svg]:size-3",
  md: "h-7 gap-1.5 px-2.5 text-[12px] [&_svg]:size-3.5",
};

const iconOnlySize: Record<ChipSize, string> = {
  sm: "w-7 justify-center px-0",
  md: "w-8 justify-center px-0",
};

// Size flows from the group so consumers set it once, not per chip.
const SizeContext = React.createContext<ChipSize>("sm");

// ---------------------------------------------------------------------------
// ChipGroup - the recess.
// ---------------------------------------------------------------------------

export interface ChipGroupProps extends React.HTMLAttributes<HTMLSpanElement> {
  size?: ChipSize;
  /**
   * Filter rows: the track wraps to as many lines as it needs instead of
   * running off the edge. Chrome clusters are bounded and stay single-line.
   */
  wrap?: boolean;
  /** Corner dot: this control is still deciding for itself. */
  pip?: boolean;
  pipTitle?: string;
  /** Corner count: how many things are waiting. 0 renders nothing. */
  count?: number;
  countTone?: Extract<ChipTone, "warning" | "danger" | "info" | "brand">;
  countLabel?: string;
}

const countToneClass: Record<string, string> = {
  warning: "bg-warning text-warning-foreground",
  danger: "bg-danger text-danger-foreground",
  info: "bg-info text-info-foreground",
  brand: "bg-brand text-brand-foreground",
};

export const ChipGroup = React.forwardRef<HTMLSpanElement, ChipGroupProps>(
  (
    {
      size = "sm",
      wrap,
      pip,
      pipTitle,
      count = 0,
      countTone = "warning",
      countLabel,
      className,
      children,
      ...props
    },
    ref,
  ) => (
    <SizeContext.Provider value={size}>
      <span
        ref={ref}
        className={cn(
          "relative inline-flex w-fit items-center gap-px rounded-lg bg-muted",
          wrap ? "h-auto min-h-7 max-w-full flex-wrap gap-0.5" : "shrink-0",
          !wrap && groupSize[size],
          wrap && "p-0.5",
          className,
        )}
        {...props}
      >
        {pip ? (
          <span
            title={pipTitle}
            aria-label={pipTitle}
            className="absolute -right-0.5 -top-0.5 z-10 size-1.5 rounded-full bg-brand ring-2 ring-background"
          />
        ) : null}
        {count > 0 ? (
          <span
            aria-label={countLabel}
            className={cn(
              "absolute -right-1 -top-1 z-10 grid min-w-4 place-items-center rounded-full px-1 font-mono text-[9px] leading-4 tabular-nums ring-2 ring-background",
              countToneClass[countTone],
            )}
          >
            {count > 99 ? "99+" : count}
          </span>
        ) : null}
        {children}
      </span>
    </SizeContext.Provider>
  ),
);
ChipGroup.displayName = "ChipGroup";

// ---------------------------------------------------------------------------
// ChipDot - the status light. Never rendered on its own (rule 4).
// ---------------------------------------------------------------------------

export function ChipDot({
  tone = "neutral",
  pulse,
  ping,
  className,
}: {
  tone?: ChipTone;
  /** Breathing: something is ongoing and fine. */
  pulse?: boolean;
  /** Ping ring: something is happening right now and wants the eye. */
  ping?: boolean;
  className?: string;
}) {
  if (ping) {
    return (
      <span className={cn("relative inline-flex size-1.5 shrink-0", className)} aria-hidden>
        <span
          className={cn(
            "absolute inset-0 inline-flex animate-ping rounded-full opacity-70 motion-reduce:animate-none",
            chipDotClass[tone],
          )}
        />
        <span className={cn("relative inline-flex size-1.5 rounded-full", chipDotClass[tone])} />
      </span>
    );
  }
  return (
    <span
      aria-hidden
      className={cn(
        "size-1.5 shrink-0 rounded-full",
        chipDotClass[tone],
        pulse && "animate-pulse motion-reduce:animate-none",
        className,
      )}
    />
  );
}

// ---------------------------------------------------------------------------
// Chip - the thing in the recess.
// ---------------------------------------------------------------------------

type ChipOwnProps = {
  /** This chip is the current answer. */
  raised?: boolean;
  /** Colours the dot. Add `loud` to colour the label too. */
  tone?: ChipTone;
  loud?: boolean;
  /** Leading status dot. "pulse" breathes, "ping" rings. */
  dot?: boolean | "pulse" | "ping";
  /** Label in Geist Mono - ids, machine names, model names. */
  mono?: boolean;
  /** Leading lucide node, auto-sized by the group. */
  icon?: React.ReactNode;
  /** Tints the leading icon without tinting the label. */
  iconTone?: ChipTone;
  /** Trailing disclosure caret for chips that open a picker. */
  chevron?: boolean;
  /** Square chip carrying only an icon. Still needs an aria-label. */
  iconOnly?: boolean;
  /** Renders a span instead of a button, for a chip that only reports state. */
  interactive?: boolean;
  /**
   * Label collapses below `sm`, leaving the icon alone in an icon-only
   * chip. Owned here so consumers stop hand-rolling `hidden sm:inline`,
   * which left a phantom gap where the label used to be.
   */
  responsiveLabel?: boolean;
  size?: ChipSize;
};

export type ChipProps = ChipOwnProps &
  Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, "children"> & {
    children?: React.ReactNode;
  };

export const Chip = React.forwardRef<HTMLButtonElement, ChipProps>(
  (
    {
      raised,
      tone = "neutral",
      loud,
      dot,
      mono,
      icon,
      iconTone,
      chevron,
      iconOnly,
      interactive = true,
      responsiveLabel,
      size,
      className,
      children,
      disabled,
      ...props
    },
    ref,
  ) => {
    const ctxSize = React.useContext(SizeContext);
    const s = size ?? ctxSize;

    const body = (
      <>
        {dot ? (
          <ChipDot tone={tone} pulse={dot === "pulse"} ping={dot === "ping"} />
        ) : null}
        {icon ? (
          <span className={cn("flex shrink-0", iconTone && chipTextClass[iconTone])} aria-hidden>
            {icon}
          </span>
        ) : null}
        {children != null ? (
          <span
            className={cn(
              "min-w-0 truncate",
              mono && "font-mono tracking-tight",
              responsiveLabel && "hidden sm:inline",
            )}
          >
            {children}
          </span>
        ) : null}
        {chevron ? <ChevronDown className="shrink-0 opacity-60" aria-hidden /> : null}
      </>
    );

    const classes = cn(
      "relative inline-flex shrink-0 items-center whitespace-nowrap rounded-md font-medium leading-none",
      "transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:ring-offset-background",
      // The ring is drawn OUTSIDE the chip's box, and chips sit a hairline
      // apart in a group, so the chip after this one painted over the ring's
      // right edge and the outline looked sliced off. Only the last chip in a
      // group escaped it. Lift the chip above its neighbours while it is
      // focused or its menu is open, which is the only time the ring exists.
      "focus-visible:z-10 data-[state=open]:z-10",
      chipSize[s],
      iconOnly && iconOnlySize[s],
      // Below sm the label is gone, so the chip takes the icon-only geometry
      // rather than keeping a gap and padding around nothing.
      responsiveLabel && (s === "sm" ? "max-sm:w-7 max-sm:justify-center max-sm:gap-0 max-sm:px-0" : "max-sm:w-8 max-sm:justify-center max-sm:gap-0 max-sm:px-0"),
      raised
        ? "bg-background text-foreground shadow-sm dark:bg-secondary"
        : "text-quiet",
      // Tone on the label is opt-in, and overrides the two above on purpose.
      loud && chipTextClass[tone],
      interactive && !disabled && !raised && "hover:text-foreground",
      interactive && !disabled && "cursor-pointer",
      // Vertical-only touch expansion: 44px to a thumb, no horizontal bleed
      // into the neighbouring chip's tap area.
      interactive &&
        "before:absolute before:inset-x-0 before:top-1/2 before:h-11 before:-translate-y-1/2 before:content-['']",
      disabled && "pointer-events-none opacity-50",
      className,
    );

    if (!interactive) {
      const { onClick: _onClick, type: _type, ...rest } = props;
      void _onClick;
      void _type;
      return (
        <span
          className={classes}
          {...(rest as React.HTMLAttributes<HTMLSpanElement>)}
        >
          {body}
        </span>
      );
    }

    return (
      <button ref={ref} type="button" disabled={disabled} className={classes} {...props}>
        {body}
      </button>
    );
  },
);
Chip.displayName = "Chip";

/**
 * ChipTrigger - wraps a Chip that opens a modal owned by a parent component.
 * `display: contents` so the wrapper never becomes a box inside the track and
 * never disturbs the group's gap.
 */
export function ChipTrigger({
  onOpen,
  children,
}: {
  onOpen: () => void;
  children: React.ReactNode;
}) {
  return (
    <span className="contents" onClick={onOpen}>
      {children}
    </span>
  );
}
