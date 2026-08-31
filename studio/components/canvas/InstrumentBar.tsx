"use client";

import * as React from "react";
import {
  Eye,
  FileText,
  FolderTree,
  GitBranch,
  Sparkles,
  SquareTerminal,
  X,
} from "lucide-react";
import { Chip, ChipGroup } from "@/components/ui/chip";
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
 * Files is an instrument and the chevron is a switcher, and they are not the
 * same thing. The switcher answers "take me to the one I am thinking of";
 * the tree answers "show me what is here" — the project, the folders, the
 * library of everything he has made. Collapsing the second into the first
 * was the mistake that left the boss with no way to see his own files.
 *
 * MOBILE: scrolls sideways rather than wrapping, so the content below never
 * shifts down when a tab appears.
 */

export type Instrument = "files" | "file" | "browser" | "changes" | "terminal" | "made";

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
    <div className="flex h-10 shrink-0 items-center gap-1.5 overflow-x-auto scroll-touch no-scrollbar border-b border-hairline px-2">
      {/* One track, so the instrument selector reads as the same object as the
          layout switch in the header rather than a second style of tab. */}
      <ChipGroup role="tablist" aria-label="Workbench instrument">
        {fileName ? (
          <Chip
            role="tab"
            aria-selected={active === "file"}
            raised={active === "file"}
            chevron
            icon={<FileText />}
            onClick={() => {
              onSelect("file");
              onOpenSwitcher?.();
            }}
            title={fileName}
            className="max-w-[11rem]"
          >
            {fileName}
          </Chip>
        ) : null}

        <Chip
          role="tab"
          aria-selected={active === "files"}
          raised={active === "files"}
          icon={<FolderTree />}
          onClick={() => onSelect("files")}
        >
          Files
        </Chip>

        <Chip
          role="tab"
          aria-selected={active === "browser"}
          raised={active === "browser"}
          icon={<Eye />}
          onClick={() => onSelect("browser")}
        >
          Browser
        </Chip>

        <Chip
          role="tab"
          aria-selected={active === "changes"}
          raised={active === "changes"}
          tone="warning"
          loud={!!changes && active !== "changes"}
          icon={<GitBranch />}
          onClick={() => onSelect("changes")}
        >
          <span className="inline-flex items-center gap-1.5">
            Changes
            {changes ? <Count on={active === "changes"}>{changes}</Count> : null}
          </span>
        </Chip>

        <Chip
          role="tab"
          aria-selected={active === "terminal"}
          raised={active === "terminal"}
          icon={<SquareTerminal />}
          onClick={() => onSelect("terminal")}
        >
          Terminal
        </Chip>

        <Chip
          role="tab"
          aria-selected={active === "made"}
          raised={active === "made"}
          icon={<Sparkles />}
          onClick={() => onSelect("made")}
        >
          <span className="inline-flex items-center gap-1.5">
            Made
            {madeCount ? <Count on={active === "made"}>{madeCount}</Count> : null}
          </span>
        </Chip>
      </ChipGroup>

      <span className="ml-auto flex shrink-0 items-center gap-1.5 pl-2">
        {trailing}
        {onClose ? (
          <ChipGroup>
            <Chip
              iconOnly
              icon={<X />}
              onClick={onClose}
              aria-label="Close the workbench"
              title="Close the workbench"
            />
          </ChipGroup>
        ) : null}
      </span>
    </div>
  );
}

/* The inline count inside an instrument chip. Distinct from ChipGroup's
 * corner count, which is for a control whose label has no room for it. */
function Count({ on, children }: { on: boolean; children: React.ReactNode }) {
  return (
    <span
      className={cn(
        "rounded-full px-1.5 font-mono text-[9.5px] tabular-nums",
        on ? "bg-muted" : "bg-accent",
      )}
    >
      {children}
    </span>
  );
}
