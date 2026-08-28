"use client";

import * as React from "react";
import { BookmarkPlus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { GroupLabel, ListRow } from "@/components/ui/list-row";
import { FilterPill, HScrollRow } from "@/components/ui/page-tabs";
import { Section } from "./Section";
import { ScrollList } from "./ScrollList";
import { relTime } from "@/lib/dashboard/format";
import type { Artifact, DashboardItem, Saved, SavedKind } from "@/lib/dashboard/types";

/* Saved - the boss's shelf, doubling as the living gallery of work Jarvis ships.
 *
 * Two sources share one section:
 *   • "saved"     - articles, links, notes, quotes the boss stashed (mem_saved).
 *   • "generated" - apps, docs, datasets Jarvis built (mem_artifacts, the
 *                   canonical "things I've made" store). These open live in the
 *                   canvas / their hosted URL via the ObjectViewer's routing.
 *
 * A segment filter (All · Saved · Made by Jarvis) only appears once there's
 * generated work to separate - a pure-bookmark shelf stays as clean as before.
 *
 * MAJORDOMO SWEEP, and the one judgement call worth flagging: this was a
 * horizontal rail of `w-[260px] rounded-xl border bg-card` tiles with a hover
 * lift and a dashed "Save something" tile on the end. Cards inside a section
 * are the boxes-in-boxes shape §1.2 exists to kill, and this section now sits
 * on a muted band, where a card is explicitly not allowed. So the rail is
 * RECOMPOSED as a list: same entries, same sort (newest first), same tap
 * targets, same segment filter, grouped by what made them. Nothing is dropped
 * - the shelf's horizontal swipe is what changed, not its contents - and the
 * page can no longer scroll sideways because of it.
 *
 * The segment chips route through the shared `FilterPill` primitive instead of
 * the local `SegChip` fork that duplicated it.
 */

const KIND_LABEL: Record<SavedKind, string> = {
  article: "article",
  link: "link",
  note: "note",
  quote: "quote",
};

const ARTIFACT_LABEL: Record<string, string> = {
  project: "app",
  document: "doc",
  dataset: "dataset",
  other: "artifact",
};

// Normalized shelf entry so bookmarks and artifacts share one row + one sort.
type Entry = {
  id: string;
  generated: boolean;
  label: string;
  title: string;
  body?: string;
  foot?: string;
  at: string;
  onClick: () => void;
};

type Segment = "all" | "saved" | "generated";

/** Rows shown before the shelf scrolls internally. */
const SAVED_ROWS = 6;

export function SavedCard({
  saved,
  artifacts,
  onOpen,
}: {
  saved: Saved[];
  artifacts: Artifact[];
  onOpen: (item: DashboardItem) => void;
}) {
  const [seg, setSeg] = React.useState<Segment>("all");

  const savedEntries = React.useMemo<Entry[]>(
    () =>
      saved.map((s) => ({
        id: `saved:${s.id}`,
        generated: false,
        label: KIND_LABEL[s.kind],
        title: s.title,
        body: s.body,
        foot: s.source,
        at: s.savedAt,
        onClick: () => onOpen({ kind: "saved", data: s }),
      })),
    [saved, onOpen],
  );

  const artifactEntries = React.useMemo<Entry[]>(
    () =>
      artifacts.map((a) => ({
        id: `artifact:${a.id}`,
        generated: true,
        label: ARTIFACT_LABEL[a.kind] ?? ARTIFACT_LABEL.other,
        title: a.name,
        body: a.description,
        at: a.createdAt,
        onClick: () => onOpen({ kind: "artifact", data: a }),
      })),
    [artifacts, onOpen],
  );

  const hasBoth = artifactEntries.length > 0 && savedEntries.length > 0;

  // Newest first within each group. "All" keeps the two groups labelled rather
  // than interleaving them, so "what Jarvis made" reads as a body of work.
  const byRecency = React.useCallback(
    (list: Entry[]) => [...list].sort((a, b) => +new Date(b.at) - +new Date(a.at)),
    [],
  );
  const groups = React.useMemo(() => {
    const made = { label: "Made by Jarvis", entries: byRecency(artifactEntries) };
    const stashed = { label: "Saved", entries: byRecency(savedEntries) };
    if (seg === "saved") return [stashed];
    if (seg === "generated") return [made];
    return [made, stashed].filter((g) => g.entries.length > 0);
  }, [seg, artifactEntries, savedEntries, byRecency]);

  const total = groups.reduce((n, g) => n + g.entries.length, 0);

  return (
    <Section title="Saved" action={{ label: "browse all", href: "/memory" }}>
      <div className="min-w-0">
        {hasBoth ? (
          <HScrollRow edgeBleed={false} className="mb-1">
            <FilterPill active={seg === "all"} onClick={() => setSeg("all")}>
              All
            </FilterPill>
            <FilterPill active={seg === "saved"} onClick={() => setSeg("saved")}>
              Saved
            </FilterPill>
            <FilterPill active={seg === "generated"} onClick={() => setSeg("generated")}>
              Made by Jarvis
            </FilterPill>
          </HScrollRow>
        ) : null}

        {total === 0 ? (
          <p className="py-2 text-[13px] text-quiet">
            Nothing on the shelf yet - article · link · note · quote.
          </p>
        ) : (
          <ScrollList max={SAVED_ROWS}>
            <div className="flex min-w-0 flex-col">
              {groups.map((g) =>
                g.entries.length === 0 ? null : (
                  <div key={g.label} className="flex min-w-0 flex-col">
                    {/* One group only (a filter is applied, or there is just
                        one kind of thing) needs no label above it. */}
                    {groups.length > 1 ? (
                      <GroupLabel label={g.label} count={g.entries.length} />
                    ) : null}
                    {g.entries.map((e) => (
                      <ListRow
                        key={e.id}
                        tone={e.generated ? "info" : "quiet"}
                        title={e.title}
                        meta={
                          <span suppressHydrationWarning>
                            {[e.label, e.body, e.foot, relTime(e.at)].filter(Boolean).join(" · ")}
                          </span>
                        }
                        onClick={e.onClick}
                      />
                    ))}
                  </div>
                ),
              )}
            </div>
          </ScrollList>
        )}

        <Button
          type="button"
          variant="ghost"
          className="mt-1 h-11 w-full justify-start px-0 text-[13.5px] font-medium text-quiet hover:bg-transparent hover:text-foreground"
        >
          <BookmarkPlus className="size-4" aria-hidden />
          Save something
        </Button>
      </div>
    </Section>
  );
}
