"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronLeft, ChevronRight, Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { fetchWorkspaceBlob } from "@/lib/api";

/**
 * PdfDeckViewer — a real page-by-page document preview.
 *
 * The browser's native PDF iframe scrolls continuously and shows several slides
 * at once, so a thumbnail click lands at a scroll offset that reads as the
 * WRONG page (the boss's "click 2, it skips to 3"). Instead we render the
 * workspace-rasterized page images (one PNG per page, via pdftoppm) and show
 * exactly ONE at a time, with a thumbnail rail that jumps straight to the page
 * you pick. Page images are fetched as blobs (an <img src> can't carry the
 * bearer) and cached; thumbnails lazy-load as they scroll into view, and the
 * current page prefetches its neighbours for instant navigation.
 */
export function PdfDeckViewer({ pages, filename }: { pages: string[]; filename: string }) {
  const [current, setCurrent] = useState(0);
  // path -> object URL. Component-scoped so URLs are revoked on unmount.
  const cache = useRef<Map<string, string>>(new Map());
  const [, force] = useState(0);

  useEffect(() => {
    const c = cache.current;
    return () => {
      c.forEach((u) => URL.revokeObjectURL(u));
      c.clear();
    };
  }, []);

  const ensure = useCallback(async (path: string | undefined) => {
    if (!path || cache.current.has(path)) return;
    const blob = await fetchWorkspaceBlob(path);
    if (blob && !cache.current.has(path)) {
      cache.current.set(path, URL.createObjectURL(blob));
      force((n) => n + 1);
    }
  }, []);

  const go = useCallback(
    (n: number) => setCurrent(Math.max(0, Math.min(pages.length - 1, n))),
    [pages.length],
  );

  // Load the current page and prefetch its neighbours.
  useEffect(() => {
    void ensure(pages[current]);
    void ensure(pages[current + 1]);
    void ensure(pages[current - 1]);
  }, [current, pages, ensure]);

  // Keyboard paging — feels like a real slide viewer.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "ArrowRight" || e.key === "PageDown") go(current + 1);
      else if (e.key === "ArrowLeft" || e.key === "PageUp") go(current - 1);
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [current, go]);

  const mainUrl = cache.current.get(pages[current]);

  return (
    <div className="flex h-full min-h-0 w-full">
      {/* Desktop: vertical thumbnail rail. */}
      <div className="scroll-touch hidden w-44 shrink-0 flex-col gap-3 overflow-y-auto border-r bg-muted/20 p-3 sm:flex dark:bg-zinc-900/40">
        {pages.map((p, i) => (
          <Thumb
            key={p}
            index={i}
            active={i === current}
            url={cache.current.get(p)}
            onEnsure={() => void ensure(p)}
            onClick={() => go(i)}
          />
        ))}
      </div>

      {/* Main stage + controls. */}
      <div className="flex min-w-0 flex-1 flex-col bg-zinc-100 dark:bg-zinc-950">
        <div className="scroll-touch flex min-h-0 flex-1 items-center justify-center overflow-auto p-3 sm:p-6">
          {mainUrl ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img
              src={mainUrl}
              alt={`${filename} — page ${current + 1}`}
              className="max-h-full max-w-full rounded-sm object-contain shadow-lg ring-1 ring-black/10"
            />
          ) : (
            <Loader2 className="size-6 animate-spin text-muted-foreground" />
          )}
        </div>

        {/* Mobile: horizontal thumbnail strip. */}
        <div className="scroll-touch flex shrink-0 gap-2 overflow-x-auto border-t bg-muted/20 p-2 sm:hidden dark:bg-zinc-900/40">
          {pages.map((p, i) => (
            <Thumb
              key={p}
              index={i}
              active={i === current}
              url={cache.current.get(p)}
              onEnsure={() => void ensure(p)}
              onClick={() => go(i)}
              horizontal
            />
          ))}
        </div>

        {/* Pager. */}
        <div className="flex h-11 shrink-0 items-center justify-center gap-3 border-t bg-background px-3">
          <button
            type="button"
            onClick={() => go(current - 1)}
            disabled={current <= 0}
            className="flex size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted disabled:opacity-30"
            aria-label="Previous page"
          >
            <ChevronLeft className="size-4" />
          </button>
          <span className="min-w-16 text-center text-xs font-medium tabular-nums text-muted-foreground">
            {current + 1} / {pages.length}
          </span>
          <button
            type="button"
            onClick={() => go(current + 1)}
            disabled={current >= pages.length - 1}
            className="flex size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted disabled:opacity-30"
            aria-label="Next page"
          >
            <ChevronRight className="size-4" />
          </button>
        </div>
      </div>
    </div>
  );
}

function Thumb({
  index,
  active,
  url,
  onEnsure,
  onClick,
  horizontal = false,
}: {
  index: number;
  active: boolean;
  url?: string;
  onEnsure: () => void;
  onClick: () => void;
  horizontal?: boolean;
}) {
  const ref = useRef<HTMLButtonElement>(null);

  // Lazy-fetch the thumbnail only once it's near the viewport.
  useEffect(() => {
    const el = ref.current;
    if (!el || url) return;
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          onEnsure();
          io.disconnect();
        }
      },
      { rootMargin: "300px" },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [url, onEnsure]);

  // Keep the active thumbnail visible as you page with the keyboard.
  useEffect(() => {
    if (active) ref.current?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [active]);

  return (
    <button
      ref={ref}
      type="button"
      onClick={onClick}
      className={cn(
        "group relative shrink-0 overflow-hidden rounded-md bg-white transition-all",
        horizontal ? "h-20 w-32" : "aspect-[4/3] w-full",
        active
          ? "ring-2 ring-primary ring-offset-2 ring-offset-muted/20 shadow-lg dark:ring-offset-zinc-900/40"
          : "opacity-65 ring-1 ring-border hover:opacity-100 hover:ring-muted-foreground/40",
      )}
      aria-label={`Go to page ${index + 1}`}
      aria-current={active ? "page" : undefined}
    >
      {url ? (
        // eslint-disable-next-line @next/next/no-img-element
        <img src={url} alt={`Page ${index + 1}`} className="size-full object-contain" />
      ) : (
        <div className="size-full animate-pulse bg-muted" />
      )}
      <span
        className={cn(
          "absolute bottom-0 right-0 rounded-tl-md px-1.5 text-[11px] font-semibold tabular-nums",
          active ? "bg-primary text-primary-foreground" : "bg-black/55 text-white",
        )}
      >
        {index + 1}
      </span>
    </button>
  );
}
