"use client";

import { Toaster as Sonner } from "sonner";

type ToasterProps = React.ComponentProps<typeof Sonner>;

// Studio ships dark-only (per CLAUDE.md: "true black, no slate undertones"),
// so the toaster is hard-coded to dark theme. Tokens map onto the same
// popover surface tokens cards use, so toasts feel like a native part of
// the design system instead of a third-party drop-in.
const Toaster = ({ ...props }: ToasterProps) => (
  <Sonner
    theme="dark"
    position="bottom-right"
    className="toaster group"
    toastOptions={{
      classNames: {
        toast:
          "group toast group-[.toaster]:bg-popover group-[.toaster]:text-popover-foreground group-[.toaster]:border-border group-[.toaster]:shadow-lg group-[.toaster]:rounded-lg",
        description: "group-[.toast]:text-muted-foreground",
        actionButton:
          "group-[.toast]:bg-brand group-[.toast]:text-brand-foreground",
        cancelButton:
          "group-[.toast]:bg-muted group-[.toast]:text-muted-foreground",
        success: "group-[.toast]:text-foreground",
        error: "group-[.toast]:text-foreground",
      },
    }}
    {...props}
  />
);

export { Toaster };
export { toast } from "sonner";
