"use client";

import { useMemo, useState } from "react";
import { AlertCircle, Film, ImageIcon, Loader2, Play, Sparkles } from "lucide-react";
import { useRuns } from "@/lib/runs/useRuns";
import type { MediaItem, RunDTO } from "@/lib/api";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { ModalMedia, ModalDl } from "@/components/ui/modal-content";

/**
 * CanvasMediaGallery — the 3rd-column "Media" tab. Sibling to Preview (a live
 * webpage) and Terminal (a live shell); this is live generated media. It is a
 * pure consumer of the server-tracked-progress substrate: every media.generate
 * run (the media_job tool) books a mem_runs row, so this reads `useRuns` and:
 *   - shows a spinner for in-flight jobs (survives refresh / nav / device)
 *   - renders finished assets from run.meta.media (stamped by media_job)
 *   - surfaces failures in plain language
 * Session-scoped via targetId, exactly like Preview/Terminal.
 */
export function CanvasMediaGallery({ sessionId }: { sessionId: string }) {
  const { runs, loading } = useRuns({
    kind: "media.generate",
    targetId: sessionId || undefined,
    limit: 50,
  });
  const [selected, setSelected] = useState<MediaItem | null>(null);

  // Flatten every finished asset across runs (most-recent first) for the grid,
  // and keep the in-flight runs to render as pending cards above them.
  const { pending, failed, assets } = useMemo(() => {
    const pending: RunDTO[] = [];
    const failed: RunDTO[] = [];
    const assets: MediaItem[] = [];
    for (const r of runs) {
      if (r.status === "running") pending.push(r);
      else if (r.status === "error") failed.push(r);
      else if (r.meta?.media?.length) assets.push(...r.meta.media);
    }
    return { pending, failed, assets };
  }, [runs]);

  const empty = !loading && pending.length === 0 && failed.length === 0 && assets.length === 0;

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <header className="flex h-9 shrink-0 items-center gap-2 border-b bg-muted/20 px-3 dark:bg-zinc-900/40">
        <Sparkles className="size-3.5 text-muted-foreground" aria-hidden />
        <span className="text-xs font-medium text-muted-foreground">Media</span>
        {assets.length > 0 && (
          <span className="ml-auto text-[11px] text-muted-foreground/70">
            {assets.length} {assets.length === 1 ? "asset" : "assets"}
          </span>
        )}
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto scroll-touch p-3">
        {empty ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
            <ImageIcon className="size-8 text-muted-foreground/40" aria-hidden />
            <p className="text-sm font-medium text-foreground/80">No media yet</p>
            <p className="max-w-[14rem] text-xs text-muted-foreground">
              Ask Jarvis to make an image or video — it appears here as it generates.
            </p>
          </div>
        ) : (
          <div className="space-y-3">
            {/* In-flight jobs — the navigation-proof spinner. */}
            {pending.map((r) => (
              <div
                key={r.id}
                className="flex items-center gap-3 rounded-lg border bg-muted/30 px-3 py-2.5"
              >
                <Loader2 className="size-4 shrink-0 animate-spin text-info" aria-hidden />
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-foreground/90">{r.label}</p>
                  <p className="truncate text-[11px] text-muted-foreground">
                    {r.progress_label || "generating…"}
                  </p>
                </div>
              </div>
            ))}

            {/* Failures — plain-language, never silent. */}
            {failed.map((r) => (
              <div
                key={r.id}
                className="flex items-start gap-2.5 rounded-lg border border-danger/40 bg-danger/5 px-3 py-2.5"
              >
                <AlertCircle className="mt-0.5 size-4 shrink-0 text-danger" aria-hidden />
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-foreground/90">
                    {r.label} — didn&apos;t finish
                  </p>
                  <p className="line-clamp-2 text-[11px] text-muted-foreground">
                    {r.human_error?.summary || r.result_summary || r.error || "Generation failed."}
                  </p>
                </div>
              </div>
            ))}

            {/* Finished assets. */}
            {assets.length > 0 && (
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                {assets.map((m, i) => (
                  <button
                    key={(m.id || m.url) + i}
                    type="button"
                    onClick={() => setSelected(m)}
                    className="group relative aspect-square min-w-0 overflow-hidden rounded-lg border bg-black/40 transition-shadow hover:shadow-md"
                    title={m.name || m.kind}
                  >
                    {m.kind === "video" ? (
                      <>
                        {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
                        <video
                          src={m.url}
                          muted
                          playsInline
                          preload="metadata"
                          className="size-full object-cover"
                        />
                        <span className="absolute inset-0 flex items-center justify-center">
                          <span className="flex size-9 items-center justify-center rounded-full bg-black/55 text-white">
                            <Play className="size-4 translate-x-px" aria-hidden />
                          </span>
                        </span>
                        <Film
                          className="absolute right-1.5 top-1.5 size-3.5 text-white/80 drop-shadow"
                          aria-hidden
                        />
                      </>
                    ) : (
                      // eslint-disable-next-line @next/next/no-img-element
                      <img
                        src={m.url}
                        alt={m.name || "Generated image"}
                        loading="lazy"
                        className="size-full object-cover transition-transform group-hover:scale-[1.02]"
                      />
                    )}
                  </button>
                ))}
              </div>
            )}
          </div>
        )}
      </div>

      <ResponsiveModal
        open={selected !== null}
        onOpenChange={(o) => !o && setSelected(null)}
        title={selected?.name || "Generated media"}
        description={selected ? `${selected.kind} · made by Jarvis` : undefined}
        size="lg"
      >
        {selected && (
          <div className="min-w-0">
            <ModalMedia src={selected.url} kind={selected.kind} alt={selected.name} />
            <div className="mt-4">
              <ModalDl
                entries={[
                  { k: "kind", v: selected.kind },
                  ...(selected.name ? [{ k: "name", v: selected.name }] : []),
                  ...(selected.mime ? [{ k: "type", v: selected.mime }] : []),
                  ...(selected.path ? [{ k: "saved to", v: selected.path }] : []),
                ]}
              />
            </div>
          </div>
        )}
      </ResponsiveModal>
    </div>
  );
}
