"use client";

import * as React from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Drawer,
  DrawerContent,
  DrawerDescription,
  DrawerTitle,
} from "@/components/ui/drawer";
import { useEffect, useRef, useState } from "react";
import { useIsDesktop } from "@/lib/use-media-query";
import { cn } from "@/lib/utils";

/* ResponsiveModal - THE canonical modal primitive for Studio.
 *
 * Every preview / info / action surface in the app uses THIS component,
 * not raw <Dialog> or <Drawer>. The component:
 *   • auto-picks Dialog on lg+ and Drawer on <lg (single mental model)
 *   • mounts only one primitive at a time (no double-overlay blur bug)
 *   • bakes in the full mobile-overflow discipline:
 *       - frame: `overflow-hidden min-w-0` (from primitives)
 *       - body : `min-w-0 max-w-full overflow-x-hidden overflow-y-auto`
 *       - footer pinned, `pb-safe`, never scrolls away from a long body
 *   • enforces an a11y `title` (required) and optional `description`
 *   • supports three width sizes (sm / md / lg). Drawer ignores width.
 *
 * IMPORTANT: New modal-style surfaces MUST use this component. Reaching
 * for `<Dialog>` or `<Drawer>` directly is a smell - it means each modal
 * is its own world, which is exactly the bug that kept reappearing on
 * mobile. The Dialog/Drawer primitives in `dialog.tsx` / `drawer.tsx`
 * are still exported (the global nav drawer, sessions drawer, etc. use
 * them) but content/preview/action modals route through here. */

type Size = "sm" | "md" | "lg" | "xl" | "full";

// Dialog widths map to a single source of truth. Drawer is always full
// viewport width (per the mobile pattern) so size only affects Dialog.
// Each entry has BOTH the responsive width (`w-[min(96vw,Xrem)]` so a
// long line never pushes the modal past 96% of the viewport) AND the
// hard `max-w-*` clamp that prevents `w-full` from overriding it.
const SIZE_CLS: Record<Size, string> = {
  sm: "w-[min(96vw,28rem)] max-w-md",
  md: "w-[min(96vw,32rem)] max-w-lg",
  lg: "w-[min(96vw,42rem)] max-w-2xl",
  xl: "w-[min(96vw,50rem)] max-w-[50rem]",
  // "full" is for a whole app-in-a-modal (a cockpit), not a preview: it takes
  // essentially the whole viewport so a multi-section surface has room to
  // breathe instead of scrolling in a letterbox.
  full: "w-[min(98vw,64rem)] max-w-[64rem]",
};

// Desktop height behaviour. "auto" sizes the Dialog to its content up to
// 90dvh (default - right for short/bounded modals). "tall" pins a fixed
// 85dvh so the modal never grows/shrinks as lazily-loaded content arrives
// (right for the email viewer, whose body ranges from a one-line note to a
// 86KB HTML email - the body scrolls instead of the frame jumping).
// "full" pins the tallest frame the viewport allows on BOTH breakpoints (the
// mobile Drawer included, which otherwise sizes to content up to 92dvh). Use
// it for cockpit-style surfaces whose sections must stay put while the body
// scrolls; "tall" and "auto" are unchanged for every existing consumer.
const HEIGHT_CLS: Record<"auto" | "tall" | "full", string> = {
  auto: "max-h-[90dvh]",
  tall: "h-[85dvh]",
  full: "h-[92dvh]",
};

export interface ResponsiveModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** a11y title - REQUIRED. Becomes the visible header unless `header` overrides. */
  title: string;
  /** Optional a11y description / sub-line under the title. */
  description?: string;
  /** Custom header node - when set, replaces the default title row. The visible header
   *  must still convey what `title` says (we keep the visually hidden Title for a11y). */
  header?: React.ReactNode;
  /** Pinned footer (action bar). Sits below the scrollable body. */
  footer?: React.ReactNode;
  /** Dialog max width on desktop. Default `md`. Drawer always spans full width. */
  size?: Size;
  /** Height behaviour. "auto" (default) sizes to content up to 90dvh; "tall"
   *  pins a fixed 85dvh on desktop so the frame doesn't grow as content loads.
   *  Both are desktop-only. The Drawer ignores them. "full" is the exception:
   *  it pins the tallest frame on BOTH breakpoints, for cockpit surfaces. */
  desktopHeight?: "auto" | "tall" | "full";
  /** Optional className for the body wrapper (the scrollable region). */
  bodyClassName?: string;
  /** Optional className for the underlying Dialog/Drawer content node. */
  contentClassName?: string;
  /** Optional className for the footer wrapper. Use to override the default
   *  `bg-muted/20` tint (e.g. event-style modals that want a lavender RSVP
   *  bar). Default classes still apply unless overridden by your tokens. */
  footerClassName?: string;
  /** When true (default), clicking outside the modal or pressing Escape
   *  closes it. For approval-style modals where every dismissal must be
   *  explicit (Approve / Close button), set this to false so casual
   *  taps and stray Escape presses can't discard the decision surface. */
  dismissOnOutsideClick?: boolean;
  children?: React.ReactNode;
}

export function ResponsiveModal({
  open,
  onOpenChange,
  title,
  description,
  header,
  footer,
  size = "md",
  desktopHeight = "auto",
  bodyClassName,
  contentClassName,
  footerClassName,
  dismissOnOutsideClick = true,
  children,
}: ResponsiveModalProps) {
  const isDesktop = useIsDesktop();
  const bodyRef = useRef<HTMLDivElement | null>(null);
  // LOCK the Dialog-vs-Drawer choice for the duration the modal is open.
  // Otherwise, when the window crosses the lg breakpoint mid-decision
  // (boss widens / narrows the chat column past 1024px), useIsDesktop
  // flips, the wrapper swaps Dialog<->Drawer, Radix fires
  // onOpenChange(false) during unmount, and the parent's open state
  // collapses to false - the approval dialog disappears mid-tap. Once
  // closed we release the lock so the next open picks the correct
  // primitive for the current viewport.
  const [lockedDesktop, setLockedDesktop] = useState<boolean | null>(null);
  useEffect(() => {
    if (open && lockedDesktop === null) {
      setLockedDesktop(isDesktop);
    } else if (!open && lockedDesktop !== null) {
      setLockedDesktop(null);
    }
  }, [open, isDesktop, lockedDesktop]);
  const effectiveIsDesktop = lockedDesktop ?? isDesktop;

  useEffect(() => {
    if (!open || effectiveIsDesktop) return;

    const resetBodyScroll = () => {
      if (bodyRef.current) {
        bodyRef.current.scrollTop = 0;
      }
    };

    resetBodyScroll();
    const frame = window.requestAnimationFrame(resetBodyScroll);
    const settleTimer = window.setTimeout(resetBodyScroll, 180);

    return () => {
      window.cancelAnimationFrame(frame);
      window.clearTimeout(settleTimer);
    };
  }, [open, effectiveIsDesktop]);

  // Should the shell GROW to fill its wrapper, or size to its own content?
  // - "tall" desktop dialogs pin a DEFINITE height (`h-[85dvh]`) — the shell
  //   must fill it (`flex-1`) or its footer floats in the middle.
  // - Everything else (mobile Drawer, "auto" dialog) is height:auto capped by
  //   max-h (92/90dvh). There the shell must size to CONTENT so a short form
  //   makes a short sheet — never a 92dvh balloon with dead space above the
  //   keyboard. It still SCROLLS when content exceeds the cap, because the
  //   body is `flex-1 min-h-0 overflow-y-auto` inside a shrinkable shell.
  // Never `h-full`: a percentage height against an indefinite parent collapses
  // to auto and breaks the flex chain when the iOS keyboard shrinks the
  // viewport (the original blank-drawer bug).
  const fillHeight =
    desktopHeight === "full" || (effectiveIsDesktop && desktopHeight === "tall");

  // Shared inner shell. Same JSX tree for Dialog and Drawer so the body /
  // header / footer behave identically across breakpoints - the ONLY
  // delta is which primitive wraps the shell.
  const shell = (
    <div
      className={cn(
        "flex min-h-0 min-w-0 max-h-full max-w-full flex-col",
        fillHeight && "flex-1",
      )}
    >
      {header ?? <DefaultHeader title={title} description={description} />}
      <div
        ref={bodyRef}
        className={cn(
          // Body is always the scroll container. min-w-0 + overflow-x-hidden
          // prevent long unbroken content (URLs, JSON, diff) from pushing
          // the modal frame past the viewport. pt-4 gives content breathing
          // room under the header — without it every modal's first row hugs
          // the header border.
          "min-h-0 min-w-0 max-w-full flex-1 overflow-x-hidden overflow-y-auto scroll-touch [overflow-anchor:none]",
          "px-4 pb-4 pt-4 sm:px-5",
          bodyClassName,
        )}
      >
        {children}
      </div>
      {footer ? (
        // Pinned action bar. `pt-3` is the always-on top breathing
        // room above the buttons; `pb-safe` is now sane (max(safe,
        // 0.75rem)) so the buttons get matching bottom space on every
        // viewport - no more buttons glued to the modal's bottom
        // border on desktop. `gap-2` separates stacked actions when
        // they wrap on a narrow viewport.
        // Majordomo §7: the footer is a hairline, not a tinted box. The
        // `bg-muted/20` tint that used to live here made the action bar read
        // as a third stacked container under the body. Consumers that WANT a
        // tint (the event RSVP bar) still pass one via footerClassName.
        <div className={cn(
          "flex shrink-0 flex-wrap items-center justify-end gap-2 border-t px-4 pt-3 sm:px-5 pb-safe",
          footerClassName,
        )}>
          {footer}
        </div>
      ) : null}
    </div>
  );

  // Block outside-click / Escape dismissals when the caller asked for
  // explicit-only closure. Approve / Close buttons remain the only exits.
  const blockDismiss = !dismissOnOutsideClick
    ? (e: Event) => e.preventDefault()
    : undefined;

  if (effectiveIsDesktop) {
    return (
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent
          onPointerDownOutside={blockDismiss}
          onEscapeKeyDown={blockDismiss}
          onInteractOutside={blockDismiss}
          className={cn(
            "flex flex-col p-0",
            HEIGHT_CLS[desktopHeight],
            SIZE_CLS[size],
            contentClassName,
          )}
        >
          {/* a11y - always render a Title + Description (sr-only when a
              custom header is provided) so Radix doesn't warn and screen
              readers always have an announcement. */}
          {header ? (
            <>
              <DialogTitle className="sr-only">{title}</DialogTitle>
              {description ? (
                <DialogDescription className="sr-only">{description}</DialogDescription>
              ) : null}
            </>
          ) : null}
          {shell}
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <Drawer open={open} onOpenChange={onOpenChange}>
      <DrawerContent
        className={cn(desktopHeight === "full" && "h-[92dvh]", contentClassName)}
      >
        {header ? (
          <>
            <DrawerTitle className="sr-only">{title}</DrawerTitle>
            {description ? (
              <DrawerDescription className="sr-only">{description}</DrawerDescription>
            ) : null}
          </>
        ) : null}
        {shell}
      </DrawerContent>
    </Drawer>
  );
}

/* ── Modal header (Majordomo §7) ───────────────────────────────────────────
 *
 * A modal header is exactly three things: the TITLE in the voice face, ONE
 * quiet context line, and the close affordance (owned by the Dialog
 * primitive, which pins its own X at top-right - hence the `pr-12` gutter).
 *
 * What is deliberately GONE: the icon chip, the mono uppercase eyebrow above
 * the title, and the second stacked sub-header. Those made every modal open
 * with "a header with a title, some subtext, then inside that another header
 * with more subtext". One title per surface (§1.3); anything that used to
 * live in an eyebrow belongs in the context line beside the rest of the meta.
 */

/** Shared header chrome so DefaultHeader and ResponsiveModalHeader can never
 *  drift apart. `pr-12` keeps the title clear of the Dialog's pinned close. */
const HEADER_CLS =
  "flex shrink-0 items-start gap-3 border-b px-4 pb-3 pr-12 pt-4 sm:px-5 sm:pr-14";

/* DefaultHeader - the standard title row used when callers don't supply a
 * custom `header`. Renders the visible <DialogTitle>/<DrawerTitle> directly
 * so a11y and visuals agree. */
function DefaultHeader({
  title,
  description,
}: {
  title: string;
  description?: string;
}) {
  // The ResponsiveModal wrapper renders either Dialog or Drawer. The
  // <DialogTitle>/<DrawerTitle> primitives are themed identically, and
  // Radix/vaul both accept being mounted inside any descendant of their
  // Root - so it's safe to render both flavors here unconditionally and
  // let the inactive one no-op. In practice only one ancestor exists at a
  // time, so only one of these mounts. Keeps the header isomorphic.
  return (
    <header className={HEADER_CLS}>
      <div className="min-w-0 flex-1">
        <ModalTitleSlot>{title}</ModalTitleSlot>
        {description ? <ModalDescriptionSlot>{description}</ModalDescriptionSlot> : null}
      </div>
    </header>
  );
}

function ModalTitleSlot({ children }: { children: React.ReactNode }) {
  // Voice register, 19px medium, tracking-tight (§4 scale: one step under a
  // section title). line-clamp-2 rather than truncate so a long email subject
  // stays readable at 375px instead of being cut mid-word.
  return (
    <h2 className="line-clamp-2 break-words font-voice text-[19px] font-medium leading-snug tracking-tight text-foreground">
      {children}
    </h2>
  );
}

function ModalDescriptionSlot({ children }: { children: React.ReactNode }) {
  // The ONE context line. Quiet ink, chrome register, never a second title.
  return (
    <p className="mt-1 line-clamp-2 break-words text-[12.5px] leading-snug text-quiet">
      {children}
    </p>
  );
}

/** joinContext - folds whatever meta a consumer passed (an old `eyebrow`, a
 *  `subtitle`, or both) into the single interpunct-joined context line. This
 *  is why dropping the eyebrow loses no information: "Webhook sentinel" +
 *  "fired 3× · cooldown 60s" becomes one line, not two stacked ones. */
function joinContext(parts: React.ReactNode[]): React.ReactNode {
  const kept = parts.filter(
    (p) => p !== null && p !== undefined && p !== false && p !== "",
  );
  if (kept.length === 0) return null;
  return kept.map((p, i) => (
    <React.Fragment key={i}>
      {i > 0 ? <span aria-hidden> · </span> : null}
      {p}
    </React.Fragment>
  ));
}

/* ResponsiveModalHeader - the opt-in header for callers that need to build
 * their own context line (or hang a status badge off the right). Pass into
 * ResponsiveModal via the `header` prop; pair with `title` on ResponsiveModal
 * for the a11y label.
 *
 * Majordomo §7 collapsed this to the SAME shape as DefaultHeader: title +
 * one context line + close. The export and every prop name survive so no
 * consumer had to change, but `icon`, `tone`, `titleSize` and `titleClamp`
 * are now accepted-and-ignored (see the note on each). `eyebrow` is NOT
 * dropped - it folds into the context line ahead of `subtitle`, so a caller
 * that only ever passed an eyebrow still shows its words. */
export function ResponsiveModalHeader({
  icon,
  eyebrow,
  title,
  subtitle,
  trailing,
  tone,
  titleSize,
  titleClamp,
}: {
  /** IGNORED (Majordomo §7: no icon chip in a modal header). Accepted so the
   *  ~7 existing callers keep compiling; a bordered 32px tile beside the
   *  title was the first of the "boxes inside boxes" the language removes. */
  icon?: React.ReactNode;
  /** Folded into the context line as its FIRST segment, no longer rendered as
   *  a mono uppercase kicker ABOVE the title (§1.6: eyebrows never sit above
   *  a title). Nothing is lost - "Webhook sentinel · fired 3×" is one line. */
  eyebrow?: string;
  title: string;
  /** The context line (e.g. "Follow-up · email · 21m ago"). When `eyebrow` is
   *  also present the two are joined with an interpunct. */
  subtitle?: React.ReactNode;
  /** Right-hand slot. Still rendered: it carries STATUS (the cron/sentinel
   *  enabled badge), not chrome, and dropping it would delete information. */
  trailing?: React.ReactNode;
  /** IGNORED - tone classes existed only to tint the icon chip. */
  tone?: string;
  /** IGNORED - there is one title size now (voice 19). The old "default"/"lg"
   *  split existed because some headers had to shout over an eyebrow. */
  titleSize?: "default" | "lg";
  /** IGNORED - the title always clamps at 2 lines: one line cut long email
   *  subjects mid-word on a phone, three let a title become the whole header. */
  titleClamp?: 1 | 2 | 3;
}) {
  // Explicitly consumed so lint doesn't flag the accepted-and-ignored props,
  // and so the reason they're inert is legible at the point of use.
  void icon;
  void tone;
  void titleSize;
  void titleClamp;

  const context = joinContext([eyebrow, subtitle]);
  return (
    <header className={HEADER_CLS}>
      <div className="min-w-0 flex-1">
        <ModalTitleSlot>{title}</ModalTitleSlot>
        {context ? <ModalDescriptionSlot>{context}</ModalDescriptionSlot> : null}
      </div>
      {trailing ? <div className="shrink-0">{trailing}</div> : null}
    </header>
  );
}

/**
 * SheetTabs — tabs INSIDE a sheet, for one object with genuinely different
 * faces.
 *
 * The same tab-vs-chip test applies here as anywhere: a tab is a different
 * KIND of content, not a subset. A job has *what happened* (a narrative),
 * *output* (a wall of log lines) and *the code it changed* (a diff). Three
 * shapes, one object, so tabs are right. Three filters over one list would
 * be chips.
 *
 * The plain answer is always the default tab and it is the only one most
 * people ever need; raw output and diffs are one tap away rather than dumped
 * underneath, which is what used to make these sheets a wall. The count in a
 * tab is what tells you whether the other faces are even worth opening — a
 * face with nothing in it should not be offered at all, so pass only the
 * tabs that have content.
 *
 * MOBILE: the strip scrolls sideways rather than wrapping, so the body below
 * never shifts down as you switch.
 */
export function SheetTabs({
  tabs,
  active,
  onChange,
  className,
}: {
  tabs: { id: string; label: string; count?: number | string }[];
  active: string;
  onChange: (id: string) => void;
  className?: string;
}) {
  if (tabs.length < 2) return null;
  return (
    <div
      role="tablist"
      className={cn(
        "-mx-1 flex min-w-0 gap-1.5 overflow-x-auto scroll-touch no-scrollbar border-b border-hairline px-1 pb-2",
        className,
      )}
    >
      {tabs.map((tab) => {
        const on = tab.id === active;
        return (
          <button
            key={tab.id}
            role="tab"
            aria-selected={on}
            onClick={() => onChange(tab.id)}
            className={cn(
              "inline-flex h-7 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full px-3 text-[12px] transition-colors",
              on
                ? "bg-foreground text-background"
                : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
            )}
          >
            <span>{tab.label}</span>
            {tab.count !== undefined && tab.count !== "" ? (
              <span
                className={cn(
                  "font-mono text-[10px] tabular-nums",
                  on ? "text-background/65" : "text-quiet",
                )}
              >
                {tab.count}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}
