"use client";

import * as React from "react";
import {
  ArrowLeft,
  ArrowRight,
  ExternalLink,
  Hand,
  Loader2,
  Monitor,
  RotateCw,
  Smartphone,
  Square,
} from "lucide-react";
import { closeBrowserSession, navigateBrowserSession, setBrowserControl } from "@/lib/api";
import { useCanvasStore, type DevicePreset } from "@/lib/canvas/store";
import { useNow } from "@/lib/useNow";
import { Chip, ChipGroup } from "@/components/ui/chip";
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
  const [editing, setEditing] = React.useState(false);
  const [stopping, setStopping] = React.useState(false);
  const now = useNow(rebuiltAt != null, 1000);

  // A live agent browser owns the surface: this bar is then ITS bar. Before
  // this, the live browser rendered its own second address row inside this
  // chrome, so two bars stacked and the outer one described the project
  // preview — a different thing entirely, showing a URL from whichever chat
  // last set it.
  const live = store.browserActive && !!store.browserSessionId;
  const liveUrl = store.browserUrl;
  const shownUrl = live ? liveUrl : url;

  const [draft, setDraft] = React.useState(shownUrl);

  React.useEffect(() => {
    if (!editing) setDraft(shownUrl);
  }, [shownUrl, editing]);

  const stopLive = React.useCallback(async () => {
    if (stopping || !store.browserSessionId) return;
    setStopping(true);
    try {
      await closeBrowserSession(store.browserSessionId);
    } finally {
      setStopping(false);
      store.setBrowserActive(false);
      store.setBrowserUrl("");
      store.setBrowserController("agent");
    }
  }, [stopping, store]);

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
        </span>

        <form
          className="relative min-w-0 flex-1"
          onSubmit={(e) => {
            e.preventDefault();
            setEditing(false);
            const next = draft.trim();
            if (!next) return;
            // While a live session owns the surface, the address bar drives
            // THAT browser. Otherwise it drives the project preview.
            if (live) {
              if (next !== liveUrl) void navigateBrowserSession(store.browserSessionId, next);
              return;
            }
            if (next !== url) onNavigate?.(next);
          }}
        >
          <input
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onFocus={() => setEditing(true)}
            onBlur={() => {
              setEditing(false);
              setDraft(shownUrl);
            }}
            spellCheck={false}
            inputMode="url"
            enterKeyHint="go"
            autoCapitalize="off"
            autoCorrect="off"
            aria-label="Address"
            placeholder={live ? "enter a URL…" : "Nothing is running yet"}
            className="h-6 w-full min-w-0 truncate rounded-md bg-muted py-0 pl-2 pr-7 font-mono text-[10.5px] text-muted-foreground outline-none transition-colors focus:text-foreground focus:ring-1 focus:ring-ring"
          />
          {/* Reload and Stop are ONE control, right-aligned inside the address
              field. They never both apply: with a live session the useful
              action is stopping it, without one it is reloading the preview.
              Two separate buttons made you read the bar to work out which was
              which. */}
          <button
            type="button"
            aria-label={live ? "Stop the browser session" : "Reload the preview"}
            title={live ? "Stop the browser session" : "Reload the preview"}
            disabled={stopping}
            onClick={() => (live ? void stopLive() : store.refreshPreview())}
            className={cn(
              "absolute right-0.5 top-1/2 grid size-5 -translate-y-1/2 place-items-center rounded transition-colors hover:bg-accent hover:text-foreground disabled:opacity-40",
              live ? "text-danger" : "text-quiet",
            )}
          >
            {stopping ? (
              <Loader2 className="size-3 animate-spin" aria-hidden />
            ) : live ? (
              <Square className="size-3" aria-hidden />
            ) : (
              <RotateCw className={cn("size-3", rebuilding && "animate-spin")} aria-hidden />
            )}
          </button>
        </form>

        {/* Who is driving. Sits in front of the device toggle, and the address
            field flexes shorter to make room rather than anything wrapping. */}
        {live && store.browserController === "human" ? (
          <>
            <ChipGroup>
              <Chip
                interactive={false}
                raised
                responsiveLabel
                tone="warning"
                loud
                icon={<Hand />}
              >
                you&apos;re driving
              </Chip>
              <Chip
                tone="warning"
                loud
                onClick={() => void setBrowserControl(store.browserSessionId, "agent")}
                title="Give control back to Jarvis"
              >
                Hand back
              </Chip>
            </ChipGroup>
          </>
        ) : live ? (
          <ChipGroup>
            <Chip interactive={false} raised tone="success" dot="pulse">
              live
            </Chip>
          </ChipGroup>
        ) : null}

        <ChipGroup className="hidden sm:inline-flex">
          <DeviceButton current={store.device} target="mobile" onClick={store.setDevice}>
            <Smartphone aria-hidden />
          </DeviceButton>
          <DeviceButton current={store.device} target="desktop" onClick={store.setDevice}>
            <Monitor aria-hidden />
          </DeviceButton>
        </ChipGroup>

        {errorCount > 0 ? (
          <ChipGroup>
            <Chip raised loud dot tone="danger" onClick={onShowErrors}>
              {errorCount} {errorCount === 1 ? "error" : "errors"}
            </Chip>
          </ChipGroup>
        ) : null}

        <ChipGroup>
          <Chip
            iconOnly
            icon={<ExternalLink />}
            aria-label="Open in a real tab"
            disabled={!shownUrl}
            onClick={() => shownUrl && window.open(shownUrl, "_blank", "noopener")}
          />
        </ChipGroup>
      </div>

      <div className="min-h-0 flex-1">{children}</div>

      {/* The gutter answers "is what I am looking at current". A preview that
          silently serves a stale build is the bug this exists to prevent.
          It speaks about the PROJECT build, so it is silent while a live agent
          browser owns the surface — a rebuild age means nothing about a page
          on someone else's website. */}
      <div
        className={cn(
          "flex h-8 shrink-0 items-center gap-2 border-t border-hairline px-3 text-[11px]",
          live && "hidden",
        )}
      >
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
    <Chip
      iconOnly
      raised={active}
      icon={children}
      onClick={() => onClick(target)}
      aria-pressed={active}
      aria-label={`${target} width`}
    />
  );
}

function sinceLabel(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s}s ago`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m ago`;
  return `${Math.round(m / 60)}h ago`;
}
