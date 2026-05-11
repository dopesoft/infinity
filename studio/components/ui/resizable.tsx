"use client";

/**
 * Resizable — thin wrapper around react-resizable-panels matching the shadcn
 * primitive shape (PanelGroup / Panel / Handle). We render the handle as a
 * 6px hit target with a 1px visible line, so it's easy to grab on touch
 * without taking up real estate. Keyboard-accessible via Radix focus styles.
 */

import { GripVertical, GripHorizontal } from "lucide-react";
import {
  Panel,
  PanelGroup,
  PanelResizeHandle,
  type PanelGroupProps,
  type PanelResizeHandleProps,
} from "react-resizable-panels";
import { cn } from "@/lib/utils";

function ResizablePanelGroup({ className, ...props }: PanelGroupProps) {
  return (
    <PanelGroup
      className={cn("flex h-full w-full data-[panel-group-direction=vertical]:flex-col", className)}
      {...props}
    />
  );
}

const ResizablePanel = Panel;

function ResizableHandle({
  withHandle = true,
  className,
  ...props
}: PanelResizeHandleProps & { withHandle?: boolean }) {
  return (
    <PanelResizeHandle
      className={cn(
        "relative flex w-px items-center justify-center bg-border transition-colors hover:bg-accent-foreground/30 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
        "data-[panel-group-direction=vertical]:h-px data-[panel-group-direction=vertical]:w-full",
        // Wider hit target on touch — visible line stays 1px but the
        // grab area is 12px wide via the absolute overlay below.
        "after:absolute after:inset-y-0 after:left-1/2 after:w-3 after:-translate-x-1/2",
        "data-[panel-group-direction=vertical]:after:left-0 data-[panel-group-direction=vertical]:after:h-3 data-[panel-group-direction=vertical]:after:w-full data-[panel-group-direction=vertical]:after:translate-x-0 data-[panel-group-direction=vertical]:after:-translate-y-1/2 data-[panel-group-direction=vertical]:after:top-1/2",
        className,
      )}
      {...props}
    >
      {withHandle && (
        <div className="z-10 flex h-6 w-3 items-center justify-center rounded-sm border bg-background opacity-60 transition-opacity hover:opacity-100 data-[panel-group-direction=vertical]:h-3 data-[panel-group-direction=vertical]:w-6">
          <GripVertical className="size-2.5 data-[panel-group-direction=vertical]:hidden" aria-hidden />
          <GripHorizontal className="hidden size-2.5 data-[panel-group-direction=vertical]:block" aria-hidden />
        </div>
      )}
    </PanelResizeHandle>
  );
}

export { ResizablePanelGroup, ResizablePanel, ResizableHandle };
