"use client";

import * as React from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";

/* shadcn-style wrapper around Radix Dialog. Used as the desktop sibling
 * of <Drawer> — the global convention is: bottom-sheet on touch surfaces,
 * centered Dialog on lg+. The <ResponsiveModal> helper composes both. */

const Dialog = DialogPrimitive.Root;
const DialogTrigger = DialogPrimitive.Trigger;
const DialogPortal = DialogPrimitive.Portal;
const DialogClose = DialogPrimitive.Close;

const DialogOverlay = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Overlay>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Overlay
    ref={ref}
    className={cn(
      // Plain fade: opacity goes 0 → 1 over 100ms. No Tailwind animate-in
      // because tailwindcss-animate sneaks in a slide-from-right default
      // when only fade-* is specified, which was making every Studio
      // modal feel like it was creeping in from the side.
      "fixed inset-0 z-50 bg-black/60 backdrop-blur-sm transition-opacity duration-100",
      "data-[state=closed]:opacity-0 data-[state=open]:opacity-100",
      className,
    )}
    {...props}
  />
));
DialogOverlay.displayName = DialogPrimitive.Overlay.displayName;

const DialogContent = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content>
>(({ className, children, "aria-describedby": ariaDescribedBy, ...props }, ref) => (
  <DialogPortal>
    <DialogOverlay />
    <DialogPrimitive.Content
      ref={ref}
      // Radix warns when no aria-describedby and no <DialogDescription> is
      // present. Most Studio dialogs are simple (title + body) and don't
      // carry a description. Default to undefined to opt out of the
      // warning per Radix's documented escape hatch; callers that DO have
      // a description pass aria-describedby explicitly and it overrides.
      aria-describedby={ariaDescribedBy ?? undefined}
      className={cn(
        // Canonical Studio modal:
        //   • centered, fixed width
        //   • backdrop dims everything behind it (DialogOverlay)
        //   • motion = plain opacity fade only. No slide, no zoom, no scale.
        //   • `overflow-hidden` + `min-w-0` are NON-NEGOTIABLE defaults so
        //     no descendant (wide pre, long URL, JSON dump) can push the
        //     dialog past `max-w-lg` and the viewport.
        // The translate-1/2's are *layout*, not animation — they center
        // the content. Pure opacity transition handles the entrance.
        "fixed left-1/2 top-1/2 z-50 w-full min-w-0 max-w-lg -translate-x-1/2 -translate-y-1/2 overflow-hidden rounded-xl border bg-popover p-0 text-popover-foreground shadow-lg",
        "transition-opacity duration-100",
        "data-[state=closed]:opacity-0 data-[state=open]:opacity-100",
        className,
      )}
      {...props}
    >
      {children}
      <DialogPrimitive.Close className="absolute right-3 top-3 rounded-md p-1.5 text-muted-foreground opacity-70 ring-offset-background transition-opacity hover:opacity-100 focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 disabled:pointer-events-none">
        <X className="size-4" />
        <span className="sr-only">Close</span>
      </DialogPrimitive.Close>
    </DialogPrimitive.Content>
  </DialogPortal>
));
DialogContent.displayName = DialogPrimitive.Content.displayName;

function DialogHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("flex flex-col gap-1.5 p-4 text-left", className)} {...props} />;
}

const DialogTitle = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Title>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Title>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Title
    ref={ref}
    className={cn("text-lg font-semibold leading-none tracking-tight", className)}
    {...props}
  />
));
DialogTitle.displayName = DialogPrimitive.Title.displayName;

const DialogDescription = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Description>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Description>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Description
    ref={ref}
    className={cn("text-sm text-muted-foreground", className)}
    {...props}
  />
));
DialogDescription.displayName = DialogPrimitive.Description.displayName;

export {
  Dialog,
  DialogPortal,
  DialogOverlay,
  DialogTrigger,
  DialogClose,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
};
