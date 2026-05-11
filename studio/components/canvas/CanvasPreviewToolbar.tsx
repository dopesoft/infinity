"use client";

import { useEffect, useState } from "react";
import {
  Check,
  Copy,
  ExternalLink,
  Monitor,
  Pencil,
  RotateCw,
  Smartphone,
  Tablet,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useCanvasStore, type DevicePreset } from "@/lib/canvas/store";
import { cn } from "@/lib/utils";

/**
 * CanvasPreviewToolbar — Lovable-style toolbar pinned above the iframe.
 *
 *   [ URL  …  pencil ] [ phone tablet desktop ] [ refresh ↻ open-in-new ↗ ]
 *
 * URL display starts read-only. Tap once → copy to clipboard.  Tap pencil
 * → editable mode (Enter to commit, Esc to revert).  Saved URL persists in
 * localStorage so the override survives reloads.
 *
 * Device toggle constrains the iframe wrapper width/height (handled in
 * CanvasPreview) — mobile=390×844, tablet=820×1180, desktop=fill. Persists.
 */
export function CanvasPreviewToolbar({ effectiveUrl }: { effectiveUrl: string }) {
  const store = useCanvasStore();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(effectiveUrl);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!editing) setDraft(effectiveUrl);
  }, [editing, effectiveUrl]);

  function commit() {
    const trimmed = draft.trim();
    if (!trimmed) {
      // Empty = clear override → fall back to env preview URL.
      store.setPreviewUrl("");
    } else {
      store.setPreviewUrl(trimmed);
    }
    setEditing(false);
    store.refreshPreview();
  }

  function cancel() {
    setDraft(effectiveUrl);
    setEditing(false);
  }

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
      {/* URL */}
      <div className="flex min-w-0 flex-1 items-center gap-1 rounded-md border bg-muted/40 px-1.5 dark:bg-zinc-900/60">
        {editing ? (
          <>
            <Input
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  commit();
                } else if (e.key === "Escape") {
                  e.preventDefault();
                  cancel();
                }
              }}
              inputMode="url"
              type="url"
              autoFocus
              className="h-7 flex-1 border-0 bg-transparent px-1 font-mono text-xs focus-visible:ring-0"
              placeholder="https://preview.dopesoft.io"
              aria-label="Preview URL"
            />
            <Button
              type="button"
              size="icon"
              variant="ghost"
              className="size-6 shrink-0"
              onClick={commit}
              aria-label="Save URL"
              title="Save"
            >
              <Check className="size-3.5" />
            </Button>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              className="size-6 shrink-0"
              onClick={cancel}
              aria-label="Cancel"
              title="Cancel"
            >
              <X className="size-3.5" />
            </Button>
          </>
        ) : (
          <>
            <button
              type="button"
              onClick={() => void copyUrl()}
              className="flex h-7 min-w-0 flex-1 items-center truncate px-1 text-left font-mono text-xs text-muted-foreground hover:text-foreground"
              title={effectiveUrl ? "Click to copy" : "No preview URL configured"}
              aria-label="Preview URL"
            >
              <span className="truncate">{effectiveUrl || "(no preview url)"}</span>
              {copied && <Check className="ml-1.5 size-3 shrink-0 text-success" />}
            </button>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              className="size-6 shrink-0"
              onClick={() => void copyUrl()}
              disabled={!effectiveUrl}
              aria-label="Copy URL"
              title="Copy"
            >
              {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
            </Button>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              className="size-6 shrink-0"
              onClick={() => setEditing(true)}
              aria-label="Edit URL"
              title="Edit URL"
            >
              <Pencil className="size-3.5" />
            </Button>
          </>
        )}
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

      {/* Actions */}
      <Button
        type="button"
        size="icon"
        variant="ghost"
        className="size-9 shrink-0"
        onClick={() => store.refreshPreview()}
        aria-label="Refresh preview"
        title="Refresh"
      >
        <RotateCw className="size-4" />
      </Button>
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
