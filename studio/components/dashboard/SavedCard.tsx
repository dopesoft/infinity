"use client";

import * as React from "react";
import { motion } from "framer-motion";
import {
  AppWindow,
  BookmarkPlus,
  Database,
  FileCode,
  FileText,
  Link2,
  Quote,
  Sparkles,
  StickyNote,
} from "lucide-react";
import { Section } from "./Section";
import { cn } from "@/lib/utils";
import { relTime } from "@/lib/dashboard/format";
import type { Artifact, DashboardItem, Saved, SavedKind } from "@/lib/dashboard/types";

/* Saved - the boss's shelf, doubling as the living gallery of work Jarvis ships.
 *
 * Two sources share one card:
 *   • "saved"     - articles, links, notes, quotes the boss stashed (mem_saved).
 *   • "generated" - apps, docs, datasets Jarvis built (mem_artifacts, the
 *                   canonical "things I've made" store). These open live in the
 *                   canvas / their hosted URL via the ObjectViewer's routing.
 *
 * A segment filter (All · Saved · Made by Jarvis) only appears once there's
 * generated work to separate - a pure-bookmark shelf stays as clean as before.
 *
 * Horizontal-scroll on every breakpoint to keep vertical height bounded.
 */

const KIND_META: Record<SavedKind, { Icon: typeof FileText; label: string; tone: string }> = {
  article: { Icon: FileText, label: "article", tone: "text-info" },
  link: { Icon: Link2, label: "link", tone: "text-muted-foreground" },
  note: { Icon: StickyNote, label: "note", tone: "text-success" },
  quote: { Icon: Quote, label: "quote", tone: "text-tier-procedural" },
};

const ARTIFACT_META: Record<string, { Icon: typeof FileText; label: string }> = {
  project: { Icon: AppWindow, label: "app" },
  document: { Icon: FileText, label: "doc" },
  dataset: { Icon: Database, label: "dataset" },
  other: { Icon: FileCode, label: "artifact" },
};

// Normalized shelf entry so bookmarks and artifacts share one tile + sort.
type Entry = {
  id: string;
  generated: boolean;
  Icon: typeof FileText;
  label: string;
  tone: string;
  title: string;
  body?: string;
  foot?: string;
  at: string;
  onClick: () => void;
};

type Segment = "all" | "saved" | "generated";

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

  const savedEntries: Entry[] = saved.map((s) => {
    const meta = KIND_META[s.kind];
    return {
      id: `saved:${s.id}`,
      generated: false,
      Icon: meta.Icon,
      label: meta.label,
      tone: meta.tone,
      title: s.title,
      body: s.body,
      foot: s.source,
      at: s.savedAt,
      onClick: () => onOpen({ kind: "saved", data: s }),
    };
  });

  const artifactEntries: Entry[] = artifacts.map((a) => {
    const meta = ARTIFACT_META[a.kind] ?? ARTIFACT_META.other;
    return {
      id: `artifact:${a.id}`,
      generated: true,
      Icon: meta.Icon,
      label: meta.label,
      tone: "text-tier-procedural",
      title: a.name,
      body: a.description,
      at: a.createdAt,
      onClick: () => onOpen({ kind: "artifact", data: a }),
    };
  });

  const hasBoth = artifactEntries.length > 0 && savedEntries.length > 0;
  const shown = React.useMemo(() => {
    const pool =
      seg === "saved"
        ? savedEntries
        : seg === "generated"
          ? artifactEntries
          : [...artifactEntries, ...savedEntries];
    return [...pool].sort((a, b) => +new Date(b.at) - +new Date(a.at));
  }, [seg, saved, artifacts, onOpen]);
  const scrollKey = shown.map((e) => e.id).join("|");

  const scrollerRef = React.useRef<HTMLDivElement>(null);
  React.useLayoutEffect(() => {
    const el = scrollerRef.current;
    if (!el) return;
    // Pin to newest (left). Double-set catches late layout / scroll restoration.
    el.scrollLeft = 0;
    requestAnimationFrame(() => {
      el.scrollLeft = 0;
    });
  }, [scrollKey, seg]);

  return (
    <Section
      title="Saved"
      Icon={BookmarkPlus}
      delay={0.4}
      action={{ label: "browse all", href: "/memory" }}
    >
      {hasBoth ? (
        <div className="mb-2 flex items-center gap-1.5">
          <SegChip active={seg === "all"} onClick={() => setSeg("all")}>
            All
          </SegChip>
          <SegChip active={seg === "saved"} onClick={() => setSeg("saved")}>
            Saved
          </SegChip>
          <SegChip active={seg === "generated"} onClick={() => setSeg("generated")}>
            <Sparkles className="size-3" aria-hidden /> Made by Jarvis
          </SegChip>
        </div>
      ) : null}
      <div className="-mx-3 px-3 sm:mx-0 sm:px-0">
        <div
          ref={scrollerRef}
          className="flex snap-x snap-mandatory items-stretch gap-3 overflow-x-auto overflow-anchor-none pt-2 pb-3 scroll-touch no-scrollbar"
        >
          {shown.map((e) => (
            <ShelfTile key={e.id} e={e} />
          ))}
          <AddNewTile />
        </div>
      </div>
    </Section>
  );
}

function SegChip({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "inline-flex items-center gap-1 rounded-full border px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.12em] transition-colors",
        active
          ? "border-foreground/25 bg-foreground/10 text-foreground"
          : "border-transparent text-muted-foreground hover:bg-accent hover:text-foreground",
      )}
    >
      {children}
    </button>
  );
}

function ShelfTile({ e }: { e: Entry }) {
  return (
    <motion.button
      type="button"
      onClick={e.onClick}
      whileHover={{ y: -2 }}
      transition={{ duration: 0.18 }}
      className={cn(
        "group relative flex w-[260px] shrink-0 snap-start flex-col gap-2 overflow-hidden rounded-xl border bg-card p-3 text-left transition-shadow",
        "hover:border-foreground/25 hover:shadow-[0_8px_30px_-12px_hsl(var(--foreground)/0.2)]",
        e.generated && "border-tier-procedural/30",
      )}
    >
      <div className="flex items-center gap-1.5">
        <e.Icon className={cn("size-3", e.tone)} aria-hidden />
        <span className={cn("font-mono text-[10px] uppercase tracking-[0.14em]", e.tone)}>
          {e.label}
        </span>
        {e.generated ? <Sparkles className="size-3 text-tier-procedural" aria-hidden /> : null}
        <span
          className="ml-auto font-mono text-[10px] text-muted-foreground"
          suppressHydrationWarning
        >
          {relTime(e.at)}
        </span>
      </div>
      {e.title ? (
        <h3 className="line-clamp-2 text-[13px] font-semibold leading-tight text-foreground">
          {e.title}
        </h3>
      ) : null}
      {e.body ? (
        <p className="line-clamp-3 text-[11px] leading-relaxed text-muted-foreground">{e.body}</p>
      ) : null}
      {e.generated ? (
        <span className="mt-auto inline-flex items-center gap-1 text-[10px] font-medium text-tier-procedural">
          Open <span aria-hidden>→</span>
        </span>
      ) : e.foot ? (
        <p className="mt-auto truncate text-[10px] text-muted-foreground">{e.foot}</p>
      ) : null}
    </motion.button>
  );
}

function AddNewTile() {
  return (
    <button
      type="button"
      className="flex w-[200px] shrink-0 snap-start flex-col items-center justify-center gap-2 rounded-xl border border-dashed bg-card/30 p-4 text-center text-xs text-muted-foreground transition-colors hover:border-foreground/30 hover:bg-card hover:text-foreground"
    >
      <BookmarkPlus className="size-5" aria-hidden />
      <span>Save something</span>
      <span className="text-[10px] text-muted-foreground/80">
        article · link · note · quote
      </span>
    </button>
  );
}
