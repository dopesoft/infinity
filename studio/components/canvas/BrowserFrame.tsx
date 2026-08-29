"use client";

import * as React from "react";
import {
  ArrowLeft,
  ArrowRight,
  ExternalLink,
  Monitor,
  RotateCw,
  Smartphone,
} from "lucide-react";
import { useCanvasStore, type DevicePreset } from "@/lib/canvas/store";
import { useNow } from "@/lib/useNow";
import { cn } from "@/lib/utils";

/**
 * BrowserFrame — a real browser, not a URL bar.
 *
 * WHAT CHANGED, AND WHY
 *
 * The old toolbar was a read-only URL pill, a device toggle, a refresh and
 * an open-in-new. It looked like a browser without behaving like one, which
 * is exactly why it was confusing: you could not go back, could not type an
 * address, and it never told you the two things you actually want to know.
 *
 * Those two things are now the loudest on the bar:
 *
 *   • Did it rebuild since his last edit? (the gutter, with the age)
 *   • Is the page throwing errors? (red, in the chrome, one tap from them)
 *
 * That second one is how you catch him shipping something broken without
 * reading a log.
 *
 * MOBILE: the address collapses to the host, and the device toggle hides —
 * on a phone you are already looking at the phone width.
 */
export function BrowserFrame({
  url,
  onNavigate,
  /** Rising edge = the preview rebuilt. Epoch ms, or null when never. */
  rebuiltAt,
  rebuilding,
  errorCount = 0,
  onShowErrors,
  children,
}: {
  url: string;
  onNavigate?: (next: string) => void;
  rebuiltAt: number | null;
  rebuilding?: boolean;
  errorCount?: number;
  onShowErrors?: () => void;
  children: React.ReactNode;
}) {
  const store = useCanvasStore();
  const [draft, setDraft] = React.useState(url);
  const [editing, setEditing] = React.useState(false);
  const now = useNow(rebuiltAt != null, 1000);

  React.useEffect(() => {
    if (!editing) setDraft(url);
  }, [url, editing]);

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex h-9 shrink-0 items-center gap-1.5 border-b border-hairline px-2">
        <span className="flex shrink-0 items-center gap-0.5 text-quiet">
          <button
            type="button"
            aria-label="Back"
            onClick={() => window.history.back()}
            className="grid size-6 place-items-center rounded transition-colors hover:bg-accent hover:text-foreground"
          >
            <ArrowLeft className="size-3.5" aria-hidden />
          </button>
          <button
            type="button"
            aria-label="Forward"
            onClick={() => window.history.forward()}
            className="grid size-6 place-items-center rounded opacity-40 transition-colors hover:bg-accent hover:text-foreground"
          >
            <ArrowRight className="size-3.5" aria-hidden />
          </button>
          <button
            type="button"
            aria-label="Reload the preview"
            onClick={() => store.refreshPreview()}
            className="grid size-6 place-items-center rounded transition-colors hover:bg-accent hover:text-foreground"
          >
            <RotateCw className={cn("size-3.5", rebuilding && "animate-spin")} aria-hidden />
          </button>
        </span>

        <form
          className="min-w-0 flex-1"
          onSubmit={(e) => {
            e.preventDefault();
            setEditing(false);
            if (draft.trim() && draft !== url) onNavigate?.(draft.trim());
          }}
        >
          <input
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onFocus={() => setEditing(true)}
            onBlur={() => {
              setEditing(false);
              setDraft(url);
            }}
            spellCheck={false}
            inputMode="url"
            enterKeyHint="go"
            aria-label="Address"
            placeholder="Nothing is running yet"
            className="h-6 w-full min-w-0 truncate rounded-md bg-muted px-2 font-mono text-[10.5px] text-muted-foreground outline-none transition-colors focus:text-foreground focus:ring-1 focus:ring-ring"
          />
        </form>

        <span className="hidden shrink-0 items-center gap-px rounded-md bg-muted p-0.5 sm:flex">
          <DeviceButton current={store.device} target="mobile" onClick={store.setDevice}>
            <Smartphone className="size-3" aria-hidden />
          </DeviceButton>
          <DeviceButton current={store.device} target="desktop" onClick={store.setDevice}>
            <Monitor className="size-3" aria-hidden />
          </DeviceButton>
        </span>

        {errorCount > 0 ? (
          <button
            type="button"
            onClick={onShowErrors}
            className="inline-flex h-6 shrink-0 items-center rounded-full border border-danger/40 px-2 text-[10.5px] text-danger transition-colors hover:bg-danger/10"
          >
            {errorCount} {errorCount === 1 ? "error" : "errors"}
          </button>
        ) : null}

        <button
          type="button"
          aria-label="Open in a real tab"
          disabled={!url}
          onClick={() => url && window.open(url, "_blank", "noopener")}
          className="grid size-6 shrink-0 place-items-center rounded text-quiet transition-colors hover:bg-accent hover:text-foreground disabled:opacity-40"
        >
          <ExternalLink className="size-3.5" aria-hidden />
        </button>
      </div>

      <div className="min-h-0 flex-1">{children}</div>

      {/* The gutter answers "is what I am looking at current". A preview that
          silently serves a stale build is the bug this exists to prevent. */}
      <div className="flex h-8 shrink-0 items-center gap-2 border-t border-hairline px-3 text-[11px]">
        <span
          className={cn(
            "size-1.5 shrink-0 rounded-full",
            rebuilding ? "animate-pulse bg-brand" : rebuiltAt ? "bg-brand" : "bg-quiet",
          )}
          aria-hidden
        />
        <span className="min-w-0 truncate text-muted-foreground">
          {rebuilding
            ? "Rebuilding now"
            : rebuiltAt
              ? `Rebuilt ${sinceLabel(now - rebuiltAt)}, after his last edit`
              : "Nothing has been built yet"}
        </span>
        {errorCount === 0 && rebuiltAt ? (
          <span className="ml-auto shrink-0 text-quiet">No errors</span>
        ) : null}
      </div>
    </div>
  );
}

function DeviceButton({
  current,
  target,
  onClick,
  children,
}: {
  current: DevicePreset;
  target: DevicePreset;
  onClick: (p: DevicePreset) => void;
  children: React.ReactNode;
}) {
  const active = current === target;
  return (
    <button
      type="button"
      onClick={() => onClick(target)}
      aria-pressed={active}
      aria-label={`${target} width`}
      className={cn(
        "grid size-5 place-items-center rounded transition-colors",
        active ? "bg-background text-foreground shadow-sm" : "text-quiet hover:text-foreground",
      )}
    >
      {children}
    </button>
  );
}

function sinceLabel(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s}s ago`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m ago`;
  return `${Math.round(m / 60)}h ago`;
}
