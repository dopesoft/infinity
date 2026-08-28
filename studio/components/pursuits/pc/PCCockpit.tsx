"use client";

import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Brain, Loader2, MessageSquare } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ResponsiveModal, ResponsiveModalHeader } from "@/components/ui/responsive-modal";
import { PageTabs, PageTabsList, PageTabsTrigger } from "@/components/ui/page-tabs";
import { TabsContent } from "@/components/ui/tabs";
import { seedSession } from "@/lib/dashboard/seed";
import { fetchCockpit } from "@/lib/pursuits/pc/api";
import { PCOnboarding } from "./PCOnboarding";
import { TodayPanel } from "./TodayPanel";
import { IdentityPanel } from "./IdentityPanel";
import { MemoriesPanel } from "./MemoriesPanel";
import { HistoryPanel } from "./HistoryPanel";
import { EmptyNote, PCCard } from "./PCPrimitives";
import type { PCCockpit as PCCockpitData } from "@/lib/pursuits/pc/types";

/* The Psycho-Cybernetics cockpit.
 *
 * Opened by tapping the coached pursuit on the dashboard. It is the whole
 * experience in one surface rather than a preview, so it uses the full-screen
 * mode of the shared ResponsiveModal (92dvh on both breakpoints) instead of
 * hand-rolling an overlay.
 *
 * Two states only:
 *   • identity or objective not set  → guided onboarding, nothing else
 *   • otherwise                      → Today first, everything else behind tabs
 *
 * Every mutation returns the refreshed cockpit from the server, so this
 * component never patches its own copy of the state and can't drift from what
 * a coaching chat would see.
 */

const TABS = [
  { value: "today", label: "today" },
  { value: "identity", label: "identity" },
  { value: "memories", label: "memories" },
  { value: "history", label: "history" },
] as const;

export function PCCockpit({
  pursuitId,
  title,
  open,
  onOpenChange,
}: {
  pursuitId: string;
  title: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const router = useRouter();
  const [cockpit, setCockpit] = useState<PCCockpitData | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [seeding, setSeeding] = useState(false);
  const [tab, setTab] = useState<string>("today");

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      setError(null);
      try {
        setCockpit(await fetchCockpit(pursuitId, signal));
      } catch (e) {
        if (signal?.aborted) return;
        // A cockpit we could not read is NOT an empty cockpit. Say so, rather
        // than rendering blank panels that look like a programme with nothing
        // in it.
        setError(e instanceof Error ? e.message : "Could not load this programme.");
      } finally {
        if (!signal?.aborted) setLoading(false);
      }
    },
    [pursuitId],
  );

  useEffect(() => {
    if (!open) return;
    const ac = new AbortController();
    setTab("today");
    void load(ac.signal);
    return () => ac.abort();
  }, [open, load]);

  /* Discuss with Jarvis uses the same seeded-session convention as every
   * other dashboard surface. The `pursuit_pc` kind is what makes the server
   * hydrate the full cockpit into turn one, so the coach opens already knowing
   * his identity, objective, day, evidence, memories and patterns. */
  async function discuss() {
    setSeeding(true);
    try {
      const sessionId = await seedSession("pursuit_pc", pursuitId);
      onOpenChange(false);
      router.push(sessionId ? `/live?session=${encodeURIComponent(sessionId)}` : "/live");
    } finally {
      setSeeding(false);
    }
  }

  const state = cockpit?.state;
  const needsOnboarding =
    !!cockpit &&
    (!state?.current_identity.trim() || !state?.current_objective.trim());

  const subtitle = state
    ? [
        `day ${state.current_day} of ${state.cycle_length_days}`,
        state.cycle_number > 1 ? `cycle ${state.cycle_number}` : null,
        state.missed_days_count > 0 ? `${state.missed_days_count} missed` : null,
      ]
        .filter(Boolean)
        .join(" · ")
    : undefined;

  return (
    <ResponsiveModal
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      description="Psycho-Cybernetics programme"
      size="full"
      desktopHeight="full"
      header={
        <ResponsiveModalHeader
          icon={<Brain className="size-4" aria-hidden />}
          title={title}
          subtitle={subtitle}
          titleSize="lg"
        />
      }
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Close
          </Button>
          <Button onClick={() => void discuss()} disabled={seeding}>
            {seeding ? (
              <Loader2 className="size-4 animate-spin" aria-hidden />
            ) : (
              <MessageSquare className="size-4" aria-hidden />
            )}
            Discuss with Jarvis
          </Button>
        </>
      }
    >
      {error ? (
        <PCCard className="border-danger/40 bg-danger/5">
          <p className="text-sm font-medium text-danger">This programme would not load.</p>
          <p className="mt-1 break-words text-sm text-muted-foreground">{error}</p>
          <Button variant="secondary" className="mt-3" onClick={() => void load()}>
            Try again
          </Button>
        </PCCard>
      ) : loading && !cockpit ? (
        <div className="flex items-center gap-2 py-10 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" aria-hidden />
          Loading your programme
        </div>
      ) : !cockpit ? (
        <PCCard>
          <EmptyNote>Nothing to show yet.</EmptyNote>
        </PCCard>
      ) : needsOnboarding ? (
        <PCOnboarding cockpit={cockpit} onDone={setCockpit} />
      ) : (
        <PageTabs value={tab} onValueChange={setTab} className="min-w-0">
          <PageTabsList scrollable>
            {TABS.map((t) => (
              <PageTabsTrigger key={t.value} value={t.value}>
                {t.label}
              </PageTabsTrigger>
            ))}
          </PageTabsList>

          <TabsContent value="today" className="mt-4 min-w-0">
            <TodayPanel cockpit={cockpit} onUpdated={setCockpit} />
          </TabsContent>
          <TabsContent value="identity" className="mt-4 min-w-0">
            <IdentityPanel cockpit={cockpit} onUpdated={setCockpit} />
          </TabsContent>
          <TabsContent value="memories" className="mt-4 min-w-0">
            <MemoriesPanel cockpit={cockpit} onUpdated={setCockpit} />
          </TabsContent>
          <TabsContent value="history" className="mt-4 min-w-0">
            <HistoryPanel cockpit={cockpit} />
          </TabsContent>
        </PageTabs>
      )}
    </ResponsiveModal>
  );
}
