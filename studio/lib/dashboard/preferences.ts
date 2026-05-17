"use client";

import { useEffect, useState } from "react";

/* Dashboard preferences - local-first.
 *
 * Until we have a `mem_meta` row keyed on `dashboard.preferences`, these
 * toggles persist to localStorage. The shape mirrors what we'll eventually
 * write to Postgres so the migration to server-side prefs is a search-and-
 * replace in `useDashboardPrefs`.
 */

export type DashboardSectionKey =
  | "pursuits"
  | "todos"
  | "upcoming"
  | "reflection"
  | "approvals"
  | "followups"
  | "work"
  | "saved"
  | "activity"
  | "memoryFooter";

export type DashboardPreferences = {
  sections: Record<DashboardSectionKey, boolean>;
};

export const SECTION_LABELS: Record<DashboardSectionKey, { title: string; description: string }> = {
  pursuits: {
    title: "Pursuits",
    description: "Daily habits, weekly cadences, and long-term goals - merged with cadence tags.",
  },
  todos: {
    title: "Todos",
    description: "Your task list. Agent-created todos appear here automatically.",
  },
  upcoming: {
    title: "Upcoming",
    description: "Calendar feed up to 6 months out, with agent-flagged prep on each event.",
  },
  reflection: {
    title: "Reflection-of-the-day",
    description: "The latest insight from Jarvis's metacognition loop. One per day.",
  },
  approvals: {
    title: "Approvals",
    description: "Trust requests, code proposals, and curiosity questions waiting on you.",
  },
  followups: {
    title: "Follow-ups",
    description: "Emails, Slack mentions, iMessage threads - people waiting on you.",
  },
  work: {
    title: "Agent Work board",
    description: "Kanban of agent activity - what's queued, running, awaiting, and done today.",
  },
  saved: {
    title: "Saved",
    description: "Articles, links, notes, and quotes you've stashed for later.",
  },
  activity: {
    title: "Activity",
    description: "Rolling event stream - agent runs, memory ops, sentinel fires, reflections.",
  },
  memoryFooter: {
    title: "Memory telemetry footer",
    description: "Quiet status strip at the bottom - daily memory growth + streak count.",
  },
};

export const SECTION_ORDER: DashboardSectionKey[] = [
  "pursuits",
  "todos",
  "upcoming",
  "reflection",
  "approvals",
  "followups",
  "work",
  "saved",
  "activity",
  "memoryFooter",
];

export const DEFAULT_PREFS: DashboardPreferences = {
  sections: {
    pursuits: true,
    todos: true,
    upcoming: true,
    reflection: true,
    approvals: true,
    followups: true,
    work: true,
    saved: true,
    activity: true,
    memoryFooter: true,
  },
};

const STORAGE_KEY = "infinity.dashboard.prefs.v1";

export function loadPrefs(): DashboardPreferences {
  if (typeof window === "undefined") return DEFAULT_PREFS;
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return DEFAULT_PREFS;
    const parsed = JSON.parse(raw) as Partial<DashboardPreferences>;
    return {
      sections: { ...DEFAULT_PREFS.sections, ...(parsed.sections ?? {}) },
    };
  } catch {
    return DEFAULT_PREFS;
  }
}

export function savePrefs(prefs: DashboardPreferences): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(prefs));
    // Broadcast within the same tab so DashboardClient picks up changes
    // immediately when the Settings tab is open beside it.
    window.dispatchEvent(new CustomEvent("infinity:dashboard-prefs", { detail: prefs }));
  } catch {
    // localStorage is a best-effort surface - ignore quota / private-mode errors.
  }
}

/* useDashboardPrefs - subscribes to localStorage changes + same-tab
 * broadcasts. Renders the SSR-safe default until mount; after mount
 * always returns the persisted value. */
export function useDashboardPrefs(): {
  prefs: DashboardPreferences;
  setPrefs: (next: DashboardPreferences) => void;
  toggleSection: (key: DashboardSectionKey) => void;
  reset: () => void;
} {
  const [prefs, setPrefsState] = useState<DashboardPreferences>(DEFAULT_PREFS);

  useEffect(() => {
    setPrefsState(loadPrefs());
    function handleStorage(e: StorageEvent) {
      if (e.key === STORAGE_KEY) setPrefsState(loadPrefs());
    }
    function handleBroadcast(e: Event) {
      const detail = (e as CustomEvent<DashboardPreferences>).detail;
      if (detail) setPrefsState(detail);
    }
    window.addEventListener("storage", handleStorage);
    window.addEventListener("infinity:dashboard-prefs", handleBroadcast as EventListener);
    return () => {
      window.removeEventListener("storage", handleStorage);
      window.removeEventListener("infinity:dashboard-prefs", handleBroadcast as EventListener);
    };
  }, []);

  function setPrefs(next: DashboardPreferences) {
    setPrefsState(next);
    savePrefs(next);
  }
  function toggleSection(key: DashboardSectionKey) {
    setPrefs({
      ...prefs,
      sections: { ...prefs.sections, [key]: !prefs.sections[key] },
    });
  }
  function reset() {
    setPrefs(DEFAULT_PREFS);
  }

  return { prefs, setPrefs, toggleSection, reset };
}
