"use client";

import { LayoutDashboard, RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  SECTION_LABELS,
  SECTION_ORDER,
  useDashboardPrefs,
} from "@/lib/dashboard/preferences";

/* Dashboard settings — visibility toggles per section.
 *
 * Stored in localStorage for now (see preferences.ts for the schema).
 * Toggling here broadcasts to any open Dashboard tab in the same
 * browser so changes feel synchronous.
 */
export function DashboardSettings() {
  const { prefs, toggleSection, reset } = useDashboardPrefs();
  const visible = SECTION_ORDER.filter((k) => prefs.sections[k]).length;

  return (
    <div className="space-y-4">
      <div className="space-y-1">
        <div className="flex items-center gap-2">
          <LayoutDashboard className="size-4 text-muted-foreground" aria-hidden />
          <h2 className="text-base font-semibold tracking-tight">Dashboard</h2>
        </div>
        <p className="text-xs text-muted-foreground">
          Pick which sections show up on the dashboard at <code className="font-mono">/</code>.
          Changes persist in this browser and apply immediately.
        </p>
      </div>

      <div className="flex items-center justify-between gap-2 rounded-md border bg-muted/30 px-3 py-2">
        <span className="text-[11px] font-mono uppercase tracking-wider text-muted-foreground">
          {visible} of {SECTION_ORDER.length} sections visible
        </span>
        <Button size="sm" variant="ghost" onClick={reset} className="gap-1.5">
          <RotateCcw className="size-3.5" aria-hidden />
          reset
        </Button>
      </div>

      <ul className="space-y-1.5">
        {SECTION_ORDER.map((key) => {
          const meta = SECTION_LABELS[key];
          const on = prefs.sections[key];
          return (
            <li key={key}>
              <label
                className={cn(
                  "flex cursor-pointer items-start gap-3 rounded-md border bg-background p-3 transition-colors",
                  on ? "border-foreground/15 hover:border-foreground/30" : "border-border/60 opacity-70 hover:opacity-100",
                )}
              >
                <Toggle on={on} onChange={() => toggleSection(key)} />
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-foreground">{meta.title}</p>
                  <p className="text-[11px] leading-relaxed text-muted-foreground">
                    {meta.description}
                  </p>
                </div>
              </label>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function Toggle({ on, onChange }: { on: boolean; onChange: () => void }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      onClick={onChange}
      className={cn(
        "relative mt-0.5 inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors",
        on ? "bg-foreground" : "bg-muted",
      )}
    >
      <span
        className={cn(
          "inline-block size-4 rounded-full bg-background shadow transition-transform",
          on ? "translate-x-[18px]" : "translate-x-0.5",
        )}
      />
    </button>
  );
}
