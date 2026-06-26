"use client";

import { useEffect, useMemo, useState } from "react";
import {
  AlertCircle, ChevronLeft, ChevronRight, FileSpreadsheet, FileText, FileType2,
  Film, ImageIcon, Loader2, Play, Presentation, Sparkles,
} from "lucide-react";
import { useCanvasStore, docMetaFromArtifact } from "@/lib/canvas/store";
import { fetchWorkspaceBlob, type DocArtifact, type MediaItem, type RunDTO } from "@/lib/api";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { ModalMedia, ModalDl } from "@/components/ui/modal-content";

/**
 * CanvasMediaGallery — the 3rd-column "Media" tab: the single home for
 * EVERYTHING Jarvis generates this session — images, video, AND documents
 * (pdf/docx/xlsx/pptx/md). One date-sorted grid with thumbnails.
 *
 * Pure renderer: CanvasRightPane owns the data (useSessionArtifacts for the
 * server-tracked documents + useRuns for media) and passes it in, so the count
 * badge, the rehydration of doc tabs, and this grid all read one source.
 *
 * Click routing matches the agreed UX:
 *   - a document → opens its own document tab (compare-friendly; has Download)
 *   - an image / video → opens the interactive viewer here, paging through
 *     just the session's media (the pager keeps working because docs never
 *     enter it — they get tabs).
 */
type Props = { documents: DocArtifact[]; mediaRuns: RunDTO[]; loading: boolean };

type GalleryItem =
  | { type: "doc"; at: number; doc: DocArtifact }
  | { type: "media"; at: number; media: MediaItem };

function docIcon(format: string) {
  switch (format) {
    case "xlsx": return FileSpreadsheet;
    case "pptx": return Presentation;
    case "pdf": return FileType2;
    default: return FileText; // docx, md
  }
}

// DocThumb shows the page-1 PNG (fetched as an authed blob, since the bearer
// can't ride an <img src>), falling back to a format icon while/when absent.
function DocThumb({ doc }: { doc: DocArtifact }) {
  const [url, setUrl] = useState<string | null>(null);
  useEffect(() => {
    if (!doc.thumb_path) return;
    let revoked = false;
    let made: string | null = null;
    fetchWorkspaceBlob(doc.thumb_path).then((b) => {
      if (revoked) return;
      if (b) {
        made = URL.createObjectURL(b);
        setUrl(made);
      }
    });
    return () => {
      revoked = true;
      if (made) URL.revokeObjectURL(made);
    };
  }, [doc.thumb_path]);
  const Icon = docIcon(doc.format);
  if (url) {
    // eslint-disable-next-line @next/next/no-img-element
    return <img src={url} alt={doc.filename} loading="lazy" className="size-full bg-white object-cover object-top" />;
  }
  return (
    <div className="flex size-full flex-col items-center justify-center gap-1 bg-muted/40">
      <Icon className="size-8 text-muted-foreground/60" aria-hidden />
      <span className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground/70">{doc.format}</span>
    </div>
  );
}

export function CanvasMediaGallery({ documents, mediaRuns, loading }: Props) {
  const store = useCanvasStore();
  const [selected, setSelected] = useState<number | null>(null);

  const { pending, failed, mediaItems } = useMemo(() => {
    const pending: RunDTO[] = [];
    const failed: RunDTO[] = [];
    const mediaItems: { at: number; media: MediaItem }[] = [];
    for (const r of mediaRuns) {
      if (r.status === "running") pending.push(r);
      else if (r.status === "error") failed.push(r);
      else if (r.meta?.media?.length) {
        const at = Date.parse(r.ended_at || r.started_at || "") || 0;
        for (const m of r.meta.media) mediaItems.push({ at, media: m });
      }
    }
    return { pending, failed, mediaItems };
  }, [mediaRuns]);

  // Unified, newest-first grid: documents + media assets together.
  const items = useMemo<GalleryItem[]>(() => {
    const docs: GalleryItem[] = documents.map((d) => ({ type: "doc", at: Date.parse(d.created_at) || 0, doc: d }));
    const media: GalleryItem[] = mediaItems.map((m) => ({ type: "media", at: m.at, media: m.media }));
    return [...docs, ...media].sort((a, b) => b.at - a.at);
  }, [documents, mediaItems]);

  // Indices of the media items in `items` — the pager cycles through these.
  const mediaIdxs = useMemo(
    () => items.reduce<number[]>((acc, it, i) => (it.type === "media" ? (acc.push(i), acc) : acc), []),
    [items],
  );
  const selItem = selected != null ? items[selected] ?? null : null;
  const selMedia = selItem && selItem.type === "media" ? selItem.media : null;

  function openItem(it: GalleryItem, idx: number) {
    if (it.type === "doc") store.openDocument(docMetaFromArtifact(it.doc));
    else setSelected(idx);
  }
  function pageMedia(dir: 1 | -1) {
    if (selected == null || mediaIdxs.length < 2) return;
    const pos = mediaIdxs.indexOf(selected);
    if (pos < 0) return;
    setSelected(mediaIdxs[(pos + dir + mediaIdxs.length) % mediaIdxs.length]);
  }

  const total = items.length;
  const empty = !loading && total === 0 && pending.length === 0 && failed.length === 0;

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <header className="flex h-9 shrink-0 items-center gap-2 border-b bg-muted/20 px-3 dark:bg-zinc-900/40">
        <Sparkles className="size-3.5 text-muted-foreground" aria-hidden />
        <span className="text-xs font-medium text-muted-foreground">Media</span>
        {total > 0 && (
          <span className="ml-auto text-[11px] text-muted-foreground/70">
            {total} {total === 1 ? "file" : "files"}
          </span>
        )}
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto scroll-touch p-3">
        {empty ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
            <ImageIcon className="size-8 text-muted-foreground/40" aria-hidden />
            <p className="text-sm font-medium text-foreground/80">Nothing here yet</p>
            <p className="max-w-[15rem] text-xs text-muted-foreground">
              Everything Jarvis makes this session — images, video, spreadsheets, docs — shows up here. Tap any one to open it.
            </p>
          </div>
        ) : (
          <div className="space-y-3">
            {/* In-flight media jobs — the navigation-proof spinner. */}
            {pending.map((r) => (
              <div key={r.id} className="flex items-center gap-3 rounded-lg border bg-muted/30 px-3 py-2.5">
                <Loader2 className="size-4 shrink-0 animate-spin text-info" aria-hidden />
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-foreground/90">{r.label}</p>
                  <p className="truncate text-[11px] text-muted-foreground">{r.progress_label || "generating…"}</p>
                </div>
              </div>
            ))}

            {/* Failures — plain-language, never silent. */}
            {failed.map((r) => (
              <div key={r.id} className="flex items-start gap-2.5 rounded-lg border border-danger/40 bg-danger/5 px-3 py-2.5">
                <AlertCircle className="mt-0.5 size-4 shrink-0 text-danger" aria-hidden />
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-foreground/90">{r.label} — didn&apos;t finish</p>
                  <p className="line-clamp-2 text-[11px] text-muted-foreground">
                    {r.human_error?.summary || r.result_summary || r.error || "Generation failed."}
                  </p>
                </div>
              </div>
            ))}

            {/* The unified grid. */}
            {total > 0 && (
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                {items.map((it, i) => (
                  <button
                    key={it.type === "doc" ? `d:${it.doc.id}` : `m:${(it.media.id || it.media.url) + i}`}
                    type="button"
                    onClick={() => openItem(it, i)}
                    className="group relative aspect-square min-w-0 overflow-hidden rounded-lg border bg-black/40 text-left transition-shadow hover:shadow-md"
                    title={it.type === "doc" ? it.doc.filename : it.media.name || it.media.kind}
                  >
                    {it.type === "doc" ? (
                      <>
                        <DocThumb doc={it.doc} />
                        <span className="absolute inset-x-0 bottom-0 truncate bg-gradient-to-t from-black/75 to-transparent px-1.5 pb-1 pt-3 text-[10px] font-medium text-white">
                          {it.doc.filename}
                        </span>
                      </>
                    ) : it.media.kind === "video" ? (
                      <>
                        {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
                        <video src={it.media.url} muted playsInline preload="metadata" className="size-full object-cover" />
                        <span className="absolute inset-0 flex items-center justify-center">
                          <span className="flex size-9 items-center justify-center rounded-full bg-black/55 text-white">
                            <Play className="size-4 translate-x-px" aria-hidden />
                          </span>
                        </span>
                        <Film className="absolute right-1.5 top-1.5 size-3.5 text-white/80 drop-shadow" aria-hidden />
                      </>
                    ) : (
                      // eslint-disable-next-line @next/next/no-img-element
                      <img
                        src={it.media.url}
                        alt={it.media.name || "Generated image"}
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
        open={selMedia !== null}
        onOpenChange={(o) => !o && setSelected(null)}
        title={selMedia?.name || "Generated media"}
        description={selMedia ? `${selMedia.kind} · made by Jarvis` : undefined}
        size="lg"
      >
        {selMedia && (
          <div className="min-w-0">
            <div className="relative">
              <ModalMedia src={selMedia.url} kind={selMedia.kind} alt={selMedia.name} />
              {mediaIdxs.length > 1 && (
                <>
                  <button
                    type="button"
                    onClick={() => pageMedia(-1)}
                    aria-label="Previous"
                    className="absolute left-1 top-1/2 flex size-9 -translate-y-1/2 items-center justify-center rounded-full bg-black/45 text-white transition-colors hover:bg-black/65"
                  >
                    <ChevronLeft className="size-5" aria-hidden />
                  </button>
                  <button
                    type="button"
                    onClick={() => pageMedia(1)}
                    aria-label="Next"
                    className="absolute right-1 top-1/2 flex size-9 -translate-y-1/2 items-center justify-center rounded-full bg-black/45 text-white transition-colors hover:bg-black/65"
                  >
                    <ChevronRight className="size-5" aria-hidden />
                  </button>
                </>
              )}
            </div>
            <div className="mt-4">
              <ModalDl
                entries={[
                  { k: "kind", v: selMedia.kind },
                  ...(selMedia.name ? [{ k: "name", v: selMedia.name }] : []),
                  ...(selMedia.mime ? [{ k: "type", v: selMedia.mime }] : []),
                  ...(selMedia.path ? [{ k: "saved to", v: selMedia.path }] : []),
                ]}
              />
            </div>
          </div>
        )}
      </ResponsiveModal>
    </div>
  );
}
