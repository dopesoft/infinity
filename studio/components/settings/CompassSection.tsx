"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { Check } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { SettingsPanel } from "@/components/settings/SettingsPanel";
import { SettingRow } from "@/components/ui/setting-row";
import { readableName } from "@/components/cron/cronMeta";
import { useRealtime } from "@/lib/realtime/provider";
import {
  fetchCompass,
  putCompassSection,
  type CompassSection as CompassSectionDTO,
} from "@/lib/api";

// CompassSection — the boss authors his north-star here: mission, goals,
// challenges, principles, active fronts. Every section is injected into every
// turn by compass.Provider on the Go side, so this is the highest-signal
// context surface in the app. One Textarea per section; saves on blur or via
// the Save button. Live across devices via the mem_compass realtime publication.
const PLACEHOLDERS: Record<string, string> = {
  mission: "What you're ultimately building toward. The one-paragraph why.",
  goals: "Your top 3-5 goals right now. What 'winning' looks like this quarter.",
  challenges: "The real obstacles in the way. What's hard, what's blocked.",
  principles: "How you want Jarvis to operate. Standing rules, taste, non-negotiables.",
 fronts: "The active fronts you're pushing on, projects, deals, threads in flight.",
};

export function CompassSection() {
  const [sections, setSections] = useState<CompassSectionDTO[]>([]);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState<string | null>(null);
  const [savedAt, setSavedAt] = useState<Record<string, number>>({});
  const [loaded, setLoaded] = useState(false);

  const load = useCallback(async () => {
    const res = await fetchCompass();
    if (res) {
      setSections(res);
      setDrafts((prev) => {
        // Don't clobber a section the boss is mid-edit on (dirty draft wins).
        const next = { ...prev };
        for (const s of res) {
          if (next[s.section] === undefined) next[s.section] = s.content;
        }
        return next;
      });
    }
    setLoaded(true);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  useRealtime("mem_compass", load);

  const dirty = useMemo(() => {
    const d: Record<string, boolean> = {};
    for (const s of sections) d[s.section] = (drafts[s.section] ?? "") !== s.content;
    return d;
  }, [sections, drafts]);

  async function save(section: string, position: number) {
    setSaving(section);
    const ok = await putCompassSection(section, drafts[section] ?? "", position);
    setSaving(null);
    if (ok) {
      setSections((prev) =>
        prev.map((s) => (s.section === section ? { ...s, content: drafts[section] ?? "" } : s)),
      );
      setSavedAt((prev) => ({ ...prev, [section]: Date.now() }));
    }
  }

  return (
    // What each field is FOR shows as the textarea's own placeholder, a
    // decision aid at the point of decision. No title: Memory renders this
    // under "What you are aiming at", and "Compass" is our codename for it.
    <SettingsPanel>
      {!loaded ? (
        <div className="flex items-center gap-2 py-8 text-sm text-muted-foreground">
          <Spinner className="size-4" /> Loading…
        </div>
      ) : (
        <div className="min-w-0">
          {sections.map((s) => (
            <SettingRow
              key={s.section}
              label={s.label || readableName(s.section)}
              control={
                <>
                  {savedAt[s.section] && !dirty[s.section] ? (
                    <span className="flex items-center gap-1 text-[12px] text-brand">
                      <Check className="size-3" /> Saved
                    </span>
                  ) : null}
                  <Button
                    size="sm"
                    variant={dirty[s.section] ? "default" : "ghost"}
                    disabled={!dirty[s.section] || saving === s.section}
                    onClick={() => save(s.section, s.position)}
                  >
                    {saving === s.section ? <Spinner className="size-3.5" /> : "Save"}
                  </Button>
                </>
              }
            >
              <Textarea
                value={drafts[s.section] ?? ""}
                onChange={(e) =>
                  setDrafts((prev) => ({ ...prev, [s.section]: e.target.value }))
                }
                onBlur={() => {
                  if (dirty[s.section]) void save(s.section, s.position);
                }}
                placeholder={PLACEHOLDERS[s.section] ?? ""}
                rows={3}
                className="min-h-[80px] resize-y"
                aria-label={s.label || readableName(s.section)}
              />
            </SettingRow>
          ))}
        </div>
      )}
    </SettingsPanel>
  );
}
