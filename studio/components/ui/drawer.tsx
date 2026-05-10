"use client";

import * as React from "react";
import { Drawer as VaulPrimitive } from "vaul";
import { cn } from "@/lib/utils";

/* shadcn-style wrapper around vaul. Used for both:
 *   • the right-hand hamburger → mobile navigation drawer (MobileNav)
 *   • bottom-sheet replacements for desktop modals on mobile screens
 *
 * Convention: on touch surfaces every modal experience opens as a
 * <Drawer> from the bottom. Desktop layouts above lg can switch to a
 * <Dialog>; for now we use Drawer everywhere for a single mental model.
 */

const Drawer = ({
  shouldScaleBackground = true,
  ...props
}: React.ComponentProps<typeof VaulPrimitive.Root>) => (
  <VaulPrimitive.Root shouldScaleBackground={shouldScaleBackground} {...props} />
);
Drawer.displayName = "Drawer";

const DrawerTrigger = VaulPrimitive.Trigger;
const DrawerPortal = VaulPrimitive.Portal;
const DrawerClose = VaulPrimitive.Close;

const DrawerOverlay = React.forwardRef<
  React.ElementRef<typeof VaulPrimitive.Overlay>,
  React.ComponentPropsWithoutRef<typeof VaulPrimitive.Overlay>
>(({ className, ...props }, ref) => (
  <VaulPrimitive.Overlay
    ref={ref}
    className={cn("fixed inset-0 z-50 bg-black/60 backdrop-blur-sm", className)}
    {...props}
  />
));
DrawerOverlay.displayName = "DrawerOverlay";

const DrawerContent = React.forwardRef<
  React.ElementRef<typeof VaulPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof VaulPrimitive.Content>
>(({ className, children, ...props }, ref) => (
  <DrawerPortal>
    <DrawerOverlay />
    <VaulPrimitive.Content
      ref={ref}
      className={cn(
        "fixed inset-x-0 bottom-0 z-50 mt-24 flex h-auto max-h-[92dvh] flex-col rounded-t-2xl border-t bg-popover text-popover-foreground pb-safe",
        className,
      )}
      {...props}
    >
      <div className="mx-auto mt-2 h-1.5 w-12 shrink-0 rounded-full bg-muted-foreground/40" aria-hidden />
      {children}
    </VaulPrimitive.Content>
  </DrawerPortal>
));
DrawerContent.displayName = "DrawerContent";

function DrawerHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("grid gap-1.5 p-4 text-center sm:text-left", className)}
      {...props}
    />
  );
}
DrawerHeader.displayName = "DrawerHeader";

function DrawerFooter({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div className={cn("mt-auto flex flex-col gap-2 p-4", className)} {...props} />
  );
}
DrawerFooter.displayName = "DrawerFooter";

const DrawerTitle = React.forwardRef<
  React.ElementRef<typeof VaulPrimitive.Title>,
  React.ComponentPropsWithoutRef<typeof VaulPrimitive.Title>
>(({ className, ...props }, ref) => (
  <VaulPrimitive.Title
    ref={ref}
    className={cn("text-lg font-semibold leading-none tracking-tight", className)}
    {...props}
  />
));
DrawerTitle.displayName = "DrawerTitle";

const DrawerDescription = React.forwardRef<
  React.ElementRef<typeof VaulPrimitive.Description>,
  React.ComponentPropsWithoutRef<typeof VaulPrimitive.Description>
>(({ className, ...props }, ref) => (
  <VaulPrimitive.Description
    ref={ref}
    className={cn("text-sm text-muted-foreground", className)}
    {...props}
  />
));
DrawerDescription.displayName = "DrawerDescription";

export {
  Drawer,
  DrawerPortal,
  DrawerOverlay,
  DrawerTrigger,
  DrawerClose,
  DrawerContent,
  DrawerHeader,
  DrawerFooter,
  DrawerTitle,
  DrawerDescription,
};
