"use client";

import * as React from "react";
import { FileText } from "lucide-react";
import { CanvasGitPanel } from "@/components/canvas/CanvasGitPanel";
import { fetchCanvasChangesSummary, type ChangeFile } from "@/lib/canvas/api";
import { useCanvasStore } from "@/lib/canvas/store";
import { cn } from "@/lib/utils";

/**
 * ChangesReview — a review surface, not a tree.
 *
 * What this adds over the git panel it wraps: EVERY FILE SAYS WHAT CHANGED IN
 * IT before you read a line of code. That one line is the difference between
 * reviewing his work and squinting at a diff.
 *
 * The summaries are deterministic (Core derives them from `git diff
 * --numstat`), and deliberately so: a generated sentence about intent is a
 * guess, and a guess you trust is worse than no sentence at all. When the
 * numbers do not support a sentence there is simply none, and the +/- counts
 * carry it.
 *
 * The commit box, staging and push all still live in <CanvasGitPanel>, which
 * already owns the Trust round-trip. This is the layer above it.
 */
export function ChangesReview({
  sessionId,
  onFileOpen,
}: {
  sessionId: string;
  onFileOpen: (path: string) => void;
}) {
  const store = useCanvasStore();
  const [files, setFiles] = React.useState<ChangeFile[] | null>(null);
  const [blind, setBlind] = React.useState(false);

  React.useEffect(() => {
    if (!store.root) return;
    const ac = new AbortController();
    let alive = true;
    const tick = async () => {
      const res = await fetchCanvasChangesSummary(store.root, sessionId, ac.signal);
      if (!alive) return;
      // `files: null` means Core could not look. That is NOT "nothing
      // changed", and rendering it as a clean tree would hide real work.
      setBlind(res == null || res.files == null);
      setFiles(res?.files ?? null);
    };
    void tick();
    const id = window.setInterval(tick, 4000);
    return () => {
      alive = false;
      ac.abort();
      window.clearInterval(id);
    };
  }, [store.root, sessionId, store.bridgeEpoch]);

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      {files && files.length > 0 ? (
        <div className="shrink-0 border-b border-hairline">
          <p className="px-3 pb-1.5 pt-3 text-[13px] font-medium">
            He changed {files.length} file{files.length === 1 ? "" : "s"}
          </p>
          <div className="flex max-h-52 min-w-0 flex-col overflow-y-auto scroll-touch px-1.5 pb-2">
            {files.map((f) => (
              <button
                key={f.path}
                type="button"
                onClick={() => onFileOpen(f.path)}
                className="flex min-h-row min-w-0 items-center gap-2.5 rounded-lg px-1.5 py-1.5 text-left transition-colors hover:bg-accent/60"
              >
                <FileText className="size-3.5 shrink-0 text-quiet" aria-hidden />
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate text-[12.5px] font-medium">{basename(f.path)}</span>
                  {/* Empty on purpose when the facts do not support a
                      sentence. Nothing is invented to fill the line. */}
                  {f.summary ? (
                    <span className="truncate text-[11px] text-quiet">{f.summary}</span>
                  ) : null}
                </span>
                <span className="flex shrink-0 items-center gap-1.5 font-mono text-[10.5px] tabular-nums">
                  {f.added > 0 ? <span className="text-brand">+{f.added}</span> : null}
                  {f.removed > 0 ? <span className="text-danger">-{f.removed}</span> : null}
                </span>
              </button>
            ))}
          </div>
        </div>
      ) : blind ? (
        <p className="shrink-0 border-b border-hairline px-3 py-3 text-[12px] text-warning">
          I could not read the change list here, so this may be out of date.
        </p>
      ) : null}

      <div className={cn("min-h-0 flex-1")}>
        <CanvasGitPanel sessionId={sessionId || null} onFileOpen={onFileOpen} />
      </div>
    </div>
  );
}

function basename(p: string): string {
  return p.replace(/^.*[\\/]/, "");
}
