"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { postSurfaceAction } from "@/lib/api";
import type { SurfaceAction } from "@/lib/dashboard/types";

/* Shared surface-action controls — the SURFACE RETURN-PATH UI.
 *
 * One source of truth for "render a tappable action on a surfaced item",
 * used by BOTH the dashboard card (SurfaceCard) and the ObjectViewer modal
 * (followups + surface items). Tapping POSTs to /api/surface/action, which
 * seeds an autonomous agent turn from the action's server-side intent; the
 * durable Working…/Done state shows via mem_runs/RunIndicator on the relevant
 * surface, so this only needs a momentary double-tap guard. */

export function SurfaceActionButton({
  itemId,
  action,
}: {
  itemId: string;
  action: SurfaceAction;
}) {
  const [firing, setFiring] = useState(false);
  const variant =
    action.style === "primary"
      ? "default"
      : action.style === "danger"
        ? "destructive"
        : "outline";
  return (
    <Button
      size="sm"
      variant={variant}
      disabled={firing}
      onClick={async (e) => {
        e.stopPropagation();
        setFiring(true);
        try {
          await postSurfaceAction(itemId, action.id);
        } finally {
          setFiring(false);
        }
      }}
    >
      {action.label}
    </Button>
  );
}

/** A wrapped row of action buttons. Renders nothing when there are no
 *  actions, so callers can drop it in unconditionally. */
export function SurfaceActionRow({
  itemId,
  actions,
  className,
}: {
  itemId: string;
  actions?: SurfaceAction[];
  className?: string;
}) {
  if (!actions || actions.length === 0) return null;
  return (
    <div className={className ?? "flex flex-wrap gap-2"}>
      {actions.map((a) => (
        <SurfaceActionButton key={a.id} itemId={itemId} action={a} />
      ))}
    </div>
  );
}
