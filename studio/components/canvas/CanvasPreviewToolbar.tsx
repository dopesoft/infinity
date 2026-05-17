"use client";

import { useState } from "react";
import {
  Check,
  ExternalLink,
  Monitor,
  RotateCw,
  Smartphone,
  Tablet,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { useCanvasStore, type DevicePreset } from "@/lib/canvas/store";
import { cn } from "@/lib/utils";

/**
 * CanvasPreviewToolbar - Lovable-style toolbar pinned above the iframe.
 *
 *   [ URL  …  pencil ] [ phone tablet desktop ] [ refresh ↻ open-in-new ↗ ]
 *
 * URL display starts read-only. Tap once → copy to clipboard.  Tap pencil
 * → editable mode (Enter to commit, Esc to revert).  Saved URL persists in
 * localStorage so the override survives reloads.
 *
 * Device toggle constrains the iframe wrapper width/height (handled in
 * CanvasPreview) - mobile=390×844, tablet=820×1180, desktop=fill. Persists.
 */
export function CanvasPreviewToolbar({ effectiveUrl }: { effectiveUrl: string }) {
  const store = useCanvasStore();
  const [copied, setCopied] = useState(false);

  // The URL is derived from the active session via the bridge supervisor;
  // there's no manual override anymore (it broke the session=project mental
  // model - a stale localStorage value would point you at the wrong app for
  // a different session). Tapping the URL still copies it to clipboard for
  // sharing / opening on another device.
  async function copyUrl() {
    if (!effectiveUrl) return;
    try {
      await navigator.clipboard.writeText(effectiveUrl);
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    } catch {
      /* ignore */
    }
  }

  return (
    <div className="flex h-11 shrink-0 items-center gap-1 border-b bg-background px-1.5">
      {/* URL pill - refresh lives inside the pill (browser-omnibox style);
          the URL text itself is tap-to-copy. Stop-propagation on the refresh
          button so tapping refresh doesn't also fire the copy. */}
      <div className="flex h-7 min-w-0 flex-1 items-center gap-0.5 rounded-md border bg-muted/40 pl-2 pr-0.5 dark:bg-zinc-900/60">
        <button
          type="button"
          onClick={() => void copyUrl()}
          disabled={!effectiveUrl}
          title={effectiveUrl ? "Click to copy" : "No preview URL configured"}
          aria-label="Preview URL"
          className="flex min-w-0 flex-1 items-center truncate text-left font-mono text-xs text-muted-foreground transition-colors hover:text-foreground disabled:cursor-not-allowed disabled:opacity-60"
        >
          <span className="truncate">{effectiveUrl || "(no preview url)"}</span>
          {copied && <Check className="ml-1.5 size-3 shrink-0 text-success" />}
        </button>
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            store.refreshPreview();
          }}
          aria-label="Refresh preview"
          title="Refresh"
          className="inline-flex size-6 shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-background hover:text-foreground"
        >
          <RotateCw className="size-3.5" />
        </button>
      </div>

      {/* Device toggle */}
      <div className="hidden items-center gap-0 rounded-md border bg-muted/40 p-0.5 sm:flex dark:bg-zinc-900/60">
        <DeviceButton current={store.device} target="mobile" onClick={store.setDevice}>
          <Smartphone className="size-3.5" />
        </DeviceButton>
        <DeviceButton current={store.device} target="tablet" onClick={store.setDevice}>
          <Tablet className="size-3.5" />
        </DeviceButton>
        <DeviceButton current={store.device} target="desktop" onClick={store.setDevice}>
          <Monitor className="size-3.5" />
        </DeviceButton>
      </div>

      <Button
        type="button"
        size="icon"
        variant="ghost"
        className="size-9 shrink-0"
        onClick={() => {
          if (effectiveUrl) window.open(effectiveUrl, "_blank", "noopener");
        }}
        disabled={!effectiveUrl}
        aria-label="Open preview in new tab"
        title="Open in new tab"
      >
        <ExternalLink className="size-4" />
      </Button>
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
      aria-label={`${target} preset`}
      title={target}
      className={cn(
        "inline-flex size-7 items-center justify-center rounded transition-colors",
        active
          ? "bg-background text-foreground shadow-sm"
          : "text-muted-foreground hover:text-foreground",
      )}
    >
      {children}
    </button>
  );
}
