"use client";

import { useEffect, useMemo, useState } from "react";
import {
  AlertCircle, ChevronLeft, ChevronRight, Download, FileSpreadsheet, FileText, FileType2,
  Film, ImageIcon, Loader2, MessageSquare, MoreVertical, Play, Presentation, Sparkles, Trash2,
} from "lucide-react";
import { useCanvasStore, docMetaFromArtifact } from "@/lib/canvas/store";
import {
  fetchWorkspaceBlob, downloadWorkspaceFile, deleteArtifact,
  type DocArtifact, type MediaItem, type RunDTO,
} from "@/lib/api";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
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
type Props = {
  documents: DocArtifact[];
  mediaRuns: RunDTO[];
  loading: boolean;
  /** Re-fetch the document list after a delete (realtime also fires; this makes
   *  the removal instant). */
  onRefresh?: () => void;
  /** Discuss a document in the CURRENT chat — the parent owns the chat, so it
   *  drops the doc into this session's stream and Jarvis responds. */
  onDiscuss?: (doc: DocArtifact) => void;
};

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

export function CanvasMediaGallery({ documents, mediaRuns, loading, onRefresh, onDiscuss }: Props) {
  const store = useCanvasStore();
  const [selected, setSelected] = useState<number | null>(null);
  // Pending delete works for any artifact (document OR media) — both are
  // mem_artifacts rows, so they share one delete path.
  const [pendingDelete, setPendingDelete] = useState<{ id: string; name: string } | null>(null);
  const [deleting, setDeleting] = useState(false);

  function downloadNative(doc: DocArtifact) {
    void downloadWorkspaceFile(doc.path, doc.filename);
  }
  function downloadPdf(doc: DocArtifact) {
    if (!doc.pdf_path) return;
    void downloadWorkspaceFile(doc.pdf_path, doc.filename.replace(/\.[^./]+$/, "") + ".pdf");
  }
  // Media bytes usually live in the workspace (path); fall back to the URL for
  // externally-hosted assets.
  function downloadMedia(m: MediaItem) {
    const name = m.name || `${m.kind}-${(m.id || "asset").slice(0, 8)}`;
    if (m.path) {
      void downloadWorkspaceFile(m.path, name);
      return;
    }
    const a = document.createElement("a");
    a.href = m.url;
    a.download = name;
    a.target = "_blank";
    a.rel = "noopener";
    document.body.appendChild(a);
    a.click();
    a.remove();
  }

  async function confirmDelete() {
    if (!pendingDelete) return;
    setDeleting(true);
    const ok = await deleteArtifact(pendingDelete.id);
    setDeleting(false);
    setPendingDelete(null);
    if (ok) onRefresh?.(); // docs refresh here; media updates via mem_runs realtime
  }

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

            {/* The unified grid — macOS Finder style: roomy tiles, the name on
                its own line underneath, and a per-item action menu. */}
            {total > 0 && (
              <div className="grid grid-cols-2 gap-x-4 gap-y-5 sm:grid-cols-3">
                {items.map((it, i) => {
                  const label = it.type === "doc" ? it.doc.filename : it.media.name || it.media.kind;
                  return (
                    <div key={it.type === "doc" ? `d:${it.doc.id}` : `m:${(it.media.id || it.media.url) + i}`} className="group flex min-w-0 flex-col">
                      {/* Thumbnail tile */}
                      <div className="relative aspect-square overflow-hidden rounded-lg border bg-black/40">
                        <button
                          type="button"
                          onClick={() => openItem(it, i)}
                          className="absolute inset-0 size-full text-left transition-shadow hover:shadow-md"
                          title={label}
                        >
                          {it.type === "doc" ? (
                            <DocThumb doc={it.doc} />
                          ) : it.media.kind === "video" ? (
                            <>
                              {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
                              <video src={it.media.url} muted playsInline preload="metadata" className="size-full object-cover" />
                              <span className="absolute inset-0 flex items-center justify-center">
                                <span className="flex size-9 items-center justify-center rounded-full bg-black/55 text-white">
                                  <Play className="size-4 translate-x-px" aria-hidden />
                                </span>
                              </span>
                              <Film className="absolute left-1.5 top-1.5 size-3.5 text-white/80 drop-shadow" aria-hidden />
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

                        {/* Per-item action menu. Documents get Discuss + both
                            downloads; media gets Download + Delete. */}
                        {it.type === "doc" ? (
                          <DocActionMenu
                            doc={it.doc}
                            canDiscuss={!!onDiscuss}
                            onDiscuss={() => onDiscuss?.(it.doc)}
                            onDownloadNative={() => downloadNative(it.doc)}
                            onDownloadPdf={() => downloadPdf(it.doc)}
                            onDelete={() => setPendingDelete({ id: it.doc.id, name: it.doc.filename })}
                          />
                        ) : it.media.id ? (
                          <MediaActionMenu
                            onDownload={() => downloadMedia(it.media)}
                            onDelete={() => setPendingDelete({ id: it.media.id as string, name: it.media.name || it.media.kind })}
                          />
                        ) : null}
                      </div>

                      {/* Name underneath (macOS Finder), wraps to 2 lines then ellipsis. */}
                      <button
                        type="button"
                        onClick={() => openItem(it, i)}
                        title={label}
                        className="mt-1.5 line-clamp-2 px-1 text-center text-[11px] leading-snug text-foreground/80 hover:text-foreground"
                      >
                        {label}
                      </button>
                    </div>
                  );
                })}
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

      {/* Delete confirmation — destructive, so confirm before erasing. */}
      <ResponsiveModal
        open={pendingDelete !== null}
        onOpenChange={(o) => !o && !deleting && setPendingDelete(null)}
        title="Delete this item?"
        description={pendingDelete?.name}
        size="sm"
        footer={
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setPendingDelete(null)} disabled={deleting}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => void confirmDelete()} disabled={deleting}>
              {deleting ? <Loader2 className="size-4 animate-spin" /> : <Trash2 className="size-4" />}
              Delete
            </Button>
          </div>
        }
      >
        <p className="text-sm text-muted-foreground">
          This removes it from Media and permanently deletes the file from your system. This
          can&apos;t be undone.
        </p>
      </ResponsiveModal>
    </div>
  );
}

// DocActionMenu — the round 3-dot button + menu on a document tile.
// Discuss (feed back to Jarvis as context), Download (native + PDF), Delete.
function DocActionMenu({
  doc, canDiscuss, onDiscuss, onDownloadNative, onDownloadPdf, onDelete,
}: {
  doc: DocArtifact;
  canDiscuss: boolean;
  onDiscuss: () => void;
  onDownloadNative: () => void;
  onDownloadPdf: () => void;
  onDelete: () => void;
}) {
  const hasPdf = !!doc.pdf_path && doc.format !== "pdf";
  const nativeLabel = `Download ${doc.format ? `.${doc.format}` : "original"}`;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          onClick={(e) => e.stopPropagation()}
          aria-label="Document actions"
          className="absolute right-1.5 top-1.5 z-10 flex size-7 items-center justify-center rounded-full bg-black/55 text-white opacity-90 backdrop-blur-sm transition hover:bg-black/75 hover:opacity-100 focus-visible:opacity-100"
        >
          <MoreVertical className="size-4" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48">
        {canDiscuss && (
          <DropdownMenuItem onSelect={onDiscuss}>
            <MessageSquare className="size-4" />
            Discuss with Jarvis
          </DropdownMenuItem>
        )}
        <DropdownMenuItem onSelect={onDownloadNative}>
          <Download className="size-4" />
          {nativeLabel}
        </DropdownMenuItem>
        {hasPdf && (
          <DropdownMenuItem onSelect={onDownloadPdf}>
            <FileType2 className="size-4" />
            Download PDF
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onSelect={onDelete}
          className="text-destructive focus:text-destructive"
        >
          <Trash2 className="size-4" />
          Delete
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// MediaActionMenu — the round 3-dot menu on an image/video tile (Download / Delete).
function MediaActionMenu({ onDownload, onDelete }: { onDownload: () => void; onDelete: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          onClick={(e) => e.stopPropagation()}
          aria-label="Media actions"
          className="absolute right-1.5 top-1.5 z-10 flex size-7 items-center justify-center rounded-full bg-black/55 text-white opacity-90 backdrop-blur-sm transition hover:bg-black/75 hover:opacity-100 focus-visible:opacity-100"
        >
          <MoreVertical className="size-4" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-44">
        <DropdownMenuItem onSelect={onDownload}>
          <Download className="size-4" />
          Download
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onDelete} className="text-destructive focus:text-destructive">
          <Trash2 className="size-4" />
          Delete
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
