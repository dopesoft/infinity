"use client";

import { motion } from "framer-motion";
import { BookmarkPlus, FileText, Link2, Quote, StickyNote } from "lucide-react";
import { Section } from "./Section";
import { cn } from "@/lib/utils";
import { relTime } from "@/lib/dashboard/format";
import type { DashboardItem, Saved, SavedKind } from "@/lib/dashboard/types";

/* Saved - articles, links, notes, quotes worth keeping in working memory.
 *
 * Horizontal-scroll on every breakpoint to avoid blowing up vertical
 * height. Cards are tiles with subtle hover lift; the last "tile" is
 * an add-new affordance.
 *
 * Each item taps into the ObjectViewer where the full body (or external
 * link) renders inline. Saved items are conversation seeds too - Jarvis
 * can pick up the thread on anything you've stashed.
 */

const KIND_META: Record<SavedKind, { Icon: typeof FileText; label: string; tone: string }> = {
  article: { Icon: FileText, label: "article", tone: "text-info" },
  link: { Icon: Link2, label: "link", tone: "text-muted-foreground" },
  note: { Icon: StickyNote, label: "note", tone: "text-success" },
  quote: { Icon: Quote, label: "quote", tone: "text-tier-procedural" },
};

export function SavedCard({
  saved,
  onOpen,
}: {
  saved: Saved[];
  onOpen: (item: DashboardItem) => void;
}) {
  return (
    <Section
      title="Saved"
      Icon={BookmarkPlus}
      delay={0.4}
      action={{ label: "browse all", href: "/memory" }}
    >
      <div className="-mx-3 px-3 sm:mx-0 sm:px-0">
        <div className="flex snap-x snap-mandatory items-stretch gap-3 overflow-x-auto pb-2 scroll-touch no-scrollbar">
          {saved.map((s) => (
            <SavedTile key={s.id} s={s} onClick={() => onOpen({ kind: "saved", data: s })} />
          ))}
          <AddNewTile />
        </div>
      </div>
    </Section>
  );
}

function SavedTile({ s, onClick }: { s: Saved; onClick: () => void }) {
  const meta = KIND_META[s.kind];
  return (
    <motion.button
      type="button"
      onClick={onClick}
      whileHover={{ y: -2 }}
      transition={{ duration: 0.18 }}
      className={cn(
        "group relative flex w-[260px] shrink-0 snap-start flex-col gap-2 overflow-hidden rounded-xl border bg-card p-3 text-left transition-shadow",
        "hover:border-foreground/25 hover:shadow-[0_8px_30px_-12px_hsl(var(--foreground)/0.2)]",
      )}
    >
      <div className="flex items-center gap-1.5">
        <meta.Icon className={cn("size-3", meta.tone)} aria-hidden />
        <span
          className={cn(
            "font-mono text-[10px] uppercase tracking-[0.14em]",
            meta.tone,
          )}
        >
          {meta.label}
        </span>
        {s.readingMinutes ? (
          <span className="font-mono text-[10px] text-muted-foreground">
            · {s.readingMinutes} min
          </span>
        ) : null}
        <span
          className="ml-auto font-mono text-[10px] text-muted-foreground"
          suppressHydrationWarning
        >
          {relTime(s.savedAt)}
        </span>
      </div>
      {s.title ? (
        <h3 className="line-clamp-2 text-[13px] font-semibold leading-tight text-foreground">
          {s.title}
        </h3>
      ) : null}
      {s.body ? (
        <p className="line-clamp-3 text-[11px] leading-relaxed text-muted-foreground">{s.body}</p>
      ) : null}
      {s.source ? (
        <p className="mt-auto truncate text-[10px] text-muted-foreground">{s.source}</p>
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
