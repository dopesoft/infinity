"use client";

import * as React from "react";
import {
  ChevronDown,
  Eye,
  FileText,
  GitBranch,
  Sparkles,
  SquareTerminal,
  X,
} from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * InstrumentBar — the one row of controls above the workbench.
 *
 * WHAT THIS REPLACES
 *
 * Two competing tab systems and a third bar pretending to be a browser: a
 * Files/Changes strip on its own column, a Preview/Terminal/Media/file strip
 * on the pane, and a URL bar under that. Four rows of chrome, none of which
 * told you the one thing you actually want to know, which is whether the
 * thing you are building works right now.
 *
 * One row. On the left, WHAT YOU ARE LOOKING AT (the open file, with the
 * switcher on its chevron). On the right, the INSTRUMENTS, each carrying its
 * own live signal — the Changes count in amber, the error count in red.
 *
 * The tree is not an instrument, it is a switcher, so it lives on the
 * chevron rather than owning a permanent column.
 *
 * MOBILE: scrolls sideways rather than wrapping, so the content below never
 * shifts down when a tab appears.
 */

export type Instrument = "file" | "browser" | "changes" | "terminal" | "made";

export function InstrumentBar({
  active,
  onSelect,
  /** Basename of the open file, when there is one. Opens the switcher. */
  fileName,
  onOpenSwitcher,
  changes,
  madeCount,
  onClose,
  trailing,
}: {
  active: Instrument;
  onSelect: (i: Instrument) => void;
  fileName?: string;
  onOpenSwitcher?: () => void;
  changes?: number;
  madeCount?: number;
  onClose?: () => void;
  trailing?: React.ReactNode;
}) {
  return (
    <div className="flex h-10 shrink-0 items-center gap-1 overflow-x-auto scroll-touch no-scrollbar border-b border-hairline px-2">
      {fileName ? (
        <Tab
          on={active === "file"}
          onClick={() => {
            onSelect("file");
            onOpenSwitcher?.();
          }}
          title={fileName}
        >
          <FileText className="size-3.5 shrink-0" aria-hidden />
          <span className="max-w-[9rem] truncate">{fileName}</span>
          <ChevronDown className="size-3 shrink-0 text-quiet" aria-hidden />
        </Tab>
      ) : null}

      <Tab on={active === "browser"} onClick={() => onSelect("browser")}>
        <Eye className="size-3.5 shrink-0" aria-hidden />
        <span>Browser</span>
      </Tab>

      <Tab
        on={active === "changes"}
        onClick={() => onSelect("changes")}
        tone={changes ? "warning" : undefined}
      >
        <GitBranch className="size-3.5 shrink-0" aria-hidden />
        <span>Changes</span>
        {changes ? <Badge on={active === "changes"}>{changes}</Badge> : null}
      </Tab>

      <Tab on={active === "terminal"} onClick={() => onSelect("terminal")}>
        <SquareTerminal className="size-3.5 shrink-0" aria-hidden />
        <span>Terminal</span>
      </Tab>

      <Tab on={active === "made"} onClick={() => onSelect("made")}>
        <Sparkles className="size-3.5 shrink-0" aria-hidden />
        <span>Made</span>
        {madeCount ? <Badge on={active === "made"}>{madeCount}</Badge> : null}
      </Tab>

      <span className="ml-auto flex shrink-0 items-center gap-1.5 pl-2">
        {trailing}
        {onClose ? (
          <button
            type="button"
            onClick={onClose}
            aria-label="Close the workbench"
            title="Close the workbench"
            className="grid size-7 place-items-center rounded-md text-quiet transition-colors hover:bg-accent hover:text-foreground"
          >
            <X className="size-3.5" aria-hidden />
          </button>
        ) : null}
      </span>
    </div>
  );
}

function Tab({
  on,
  tone,
  title,
  onClick,
  children,
}: {
  on: boolean;
  tone?: "warning";
  title?: string;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={on}
      title={title}
      onClick={onClick}
      className={cn(
        "inline-flex h-7 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-lg px-2.5 text-[11.5px] transition-colors",
        on
          ? "bg-muted text-foreground"
          : tone === "warning"
            ? "text-warning hover:bg-accent/60"
            : "text-quiet hover:bg-accent/60 hover:text-foreground",
      )}
    >
      {children}
    </button>
  );
}

function Badge({ on, children }: { on: boolean; children: React.ReactNode }) {
  return (
    <span
      className={cn(
        "rounded-full px-1.5 font-mono text-[9.5px] tabular-nums",
        on ? "bg-accent" : "bg-muted",
      )}
    >
      {children}
    </span>
  );
}
