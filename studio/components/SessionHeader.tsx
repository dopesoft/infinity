"use client";

import { IconPlus, IconEraser } from "@tabler/icons-react";
import { Button } from "@/components/ui/button";

export function SessionHeader({
  sessionId,
  onNew,
  onClear,
}: {
  sessionId: string;
  onNew: () => void;
  onClear: () => void;
}) {
  return (
    <div className="flex items-center justify-between gap-2 border-b bg-background/95 px-3 py-2 sm:px-4">
      <div className="flex min-w-0 flex-col">
        <span className="text-[11px] uppercase tracking-wide text-muted-foreground">Session</span>
        <code className="truncate font-mono text-xs text-foreground">{sessionId}</code>
      </div>
      <div className="flex shrink-0 items-center gap-1">
        <Button variant="ghost" size="sm" onClick={onNew} aria-label="Start new session">
          <IconPlus className="size-4" /> <span className="hidden sm:inline">new</span>
        </Button>
        <Button variant="ghost" size="sm" onClick={onClear} aria-label="Clear visible history">
          <IconEraser className="size-4" /> <span className="hidden sm:inline">clear</span>
        </Button>
      </div>
    </div>
  );
}
