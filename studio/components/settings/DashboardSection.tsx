"use client";

import { RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { GroupLabel } from "@/components/ui/list-row";
import { SettingsPanel } from "@/components/settings/SettingsPanel";
import { SettingRow } from "@/components/ui/setting-row";
import { Switch } from "@/components/ui/switch";
import {
  SECTION_LABELS,
  SECTION_ORDER,
  useDashboardPrefs,
} from "@/lib/dashboard/preferences";

/* Dashboard settings - visibility toggles per section.
 *
 * Stored in localStorage for now (see preferences.ts for the schema).
 * Toggling here broadcasts to any open Dashboard tab in the same
 * browser so changes feel synchronous.
 *
 * Majordomo sweep: the section's own `<h2>` + explanatory paragraph are now
 * the page's `Section` title + count (the nav rail already said "Dashboard",
 * §1.3), the "N of M visible" bordered strip is that count, and each toggle
 * is a `SettingRow` + the `Switch` primitive instead of a bordered label
 * wrapping a hand-rolled toggle (§1.2 bans bordered list rows). Every
 * per-section description survives — on a setting row the grey line is a
 * decision aid, which §1.5 explicitly keeps.
 */
export function DashboardSettings() {
  const { prefs, toggleSection, reset } = useDashboardPrefs();
  const visible = SECTION_ORDER.filter((k) => prefs.sections[k]).length;

  return (
    <SettingsPanel
    >
      {/* The count and the Reset sit on the list they act on, not in a panel
          header only some sections would have. */}
      <GroupLabel
        label="Cards on your home screen"
        count={visible}
        trailing={
          <Button size="sm" variant="ghost" onClick={reset} className="gap-1.5">
            <RotateCcw className="size-3.5" aria-hidden />
            Reset
          </Button>
        }
      />
      {SECTION_ORDER.map((key) => {
        const meta = SECTION_LABELS[key];
        const on = prefs.sections[key];
        return (
          <SettingRow
            key={key}
            label={meta.title}
            description={meta.description}
            disabled={!on}
            control={
              <Switch
                checked={on}
                onCheckedChange={() => toggleSection(key)}
                aria-label={`Show ${meta.title} on the dashboard`}
              />
            }
          />
        );
      })}
    </SettingsPanel>
  );
}
