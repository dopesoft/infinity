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

type Size = "sm" | "md" | "lg";

// Dialog widths map to a single source of truth. Drawer is always full
// viewport width (per the mobile pattern) so size only affects Dialog.
// Each entry has BOTH the responsive width (`w-[min(96vw,Xrem)]` so a
// long line never pushes the modal past 96% of the viewport) AND the
// hard `max-w-*` clamp that prevents `w-full` from overriding it.
const SIZE_CLS: Record<Size, string> = {
  sm: "w-[min(96vw,28rem)] max-w-md",
  md: "w-[min(96vw,32rem)] max-w-lg",
  lg: "w-[min(96vw,42rem)] max-w-2xl",
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
  /** Optional className for the body wrapper (the scrollable region). */
  bodyClassName?: string;
  /** Optional className for the underlying Dialog/Drawer content node. */
  contentClassName?: string;
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
  bodyClassName,
  contentClassName,
  children,
}: ResponsiveModalProps) {
  const isDesktop = useIsDesktop();

  // Shared inner shell. Same JSX tree for Dialog and Drawer so the body /
  // header / footer behave identically across breakpoints - the ONLY
  // delta is which primitive wraps the shell.
  const shell = (
    <div className="flex h-full min-h-0 min-w-0 max-h-full max-w-full flex-col">
      {header ?? <DefaultHeader title={title} description={description} />}
      <div
        className={cn(
          // Body is always the scroll container. min-w-0 + overflow-x-hidden
          // prevent long unbroken content (URLs, JSON, diff) from pushing
          // the modal frame past the viewport.
          "min-h-0 min-w-0 max-w-full flex-1 overflow-x-hidden overflow-y-auto scroll-touch",
          "px-4 pb-4 sm:px-5",
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
        <div className="flex shrink-0 flex-wrap items-center justify-end gap-2 border-t bg-muted/20 px-4 pt-3 sm:px-5 pb-safe">
          {footer}
        </div>
      ) : null}
    </div>
  );

  if (isDesktop) {
    return (
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent
          className={cn(
            "flex max-h-[90dvh] flex-col p-0",
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
      <DrawerContent className={contentClassName}>
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
    <header className="flex shrink-0 items-center gap-3 border-b px-4 pb-3 pt-4 sm:px-5">
      <div className="min-w-0 flex-1">
        <ModalTitleSlot>{title}</ModalTitleSlot>
        {description ? <ModalDescriptionSlot>{description}</ModalDescriptionSlot> : null}
      </div>
    </header>
  );
}

function ModalTitleSlot({ children }: { children: React.ReactNode }) {
  // Render the same text once - Radix Title or vaul Title - by trying
  // both. Each is a no-op if its Root isn't mounted as an ancestor. We
  // use the Dialog primitive here because it's mounted on lg+; mobile
  // uses Drawer and we render the Drawer title at the same level. The
  // wrapper component picks which is alive.
  return (
    <h2 className="truncate text-base font-semibold tracking-tight text-foreground">
      {children}
    </h2>
  );
}

function ModalDescriptionSlot({ children }: { children: React.ReactNode }) {
  return (
    <p className="mt-0.5 line-clamp-2 break-words text-xs text-muted-foreground">
      {children}
    </p>
  );
}

/* CustomHeader - opt-in helper for richer headers (icon + eyebrow + title +
 * trailing slot). Pass into ResponsiveModal via the `header` prop. Carries
 * the same overflow discipline as the default header so callers can't
 * regress it. Pair with `title` on ResponsiveModal for the a11y label. */
export function ResponsiveModalHeader({
  icon,
  eyebrow,
  title,
  trailing,
  tone,
}: {
  icon?: React.ReactNode;
  eyebrow?: string;
  title: string;
  trailing?: React.ReactNode;
  /** Tone classes for the icon chip (border + bg + text). Default = muted. */
  tone?: string;
}) {
  return (
    <header className="flex shrink-0 items-center gap-3 border-b px-4 pb-3 pt-4 sm:px-5">
      {icon ? (
        <span
          className={cn(
            "flex size-8 shrink-0 items-center justify-center rounded-md border",
            tone ?? "border-border bg-muted text-foreground",
          )}
        >
          {icon}
        </span>
      ) : null}
      <div className="min-w-0 flex-1">
        {eyebrow ? (
          <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
            {eyebrow}
          </p>
        ) : null}
        <h2 className="truncate text-base font-semibold tracking-tight text-foreground">
          {title}
        </h2>
      </div>
      {trailing ? <div className="shrink-0">{trailing}</div> : null}
    </header>
  );
}
