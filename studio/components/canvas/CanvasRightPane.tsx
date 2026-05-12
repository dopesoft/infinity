"use client";

import { useEffect, useRef } from "react";
import { MonitorPlay, X, Lock } from "lucide-react";
import { CanvasPreview } from "@/components/canvas/CanvasPreview";
import { CanvasFileTab } from "@/components/canvas/CanvasFileTab";
import { useCanvasStore } from "@/lib/canvas/store";
import { cn } from "@/lib/utils";
import type { useChat } from "@/hooks/useChat";

type ChatHook = ReturnType<typeof useChat>;

/**
 * CanvasRightPane — VS Code-style tabbed editor group.
 *
 * Tab 1 is always Preview (pinned, non-closable). Subsequent tabs are open
 * files, ordered by open time. Active-tab content renders below the tab
 * strip. We keep all tabs mounted-but-hidden so switching between Preview
 * and a Monaco file doesn't unmount Monaco (preserves cursor position,
 * scroll, and avoids a re-init flicker on every tab click).
 */
export function CanvasRightPane({ chat }: { chat: ChatHook }) {
  const store = useCanvasStore();
  const stripRef = useRef<HTMLDivElement>(null);

  // When the active tab changes, scroll it into view in the strip — mobile
  // and small desktop widths can clip the strip horizontally.
  useEffect(() => {
    const strip = stripRef.current;
    if (!strip) return;
    const el = strip.querySelector<HTMLElement>(`[data-tab-id="${cssAttr(store.activeTabId)}"]`);
    if (el && typeof el.scrollIntoView === "function") {
      el.scrollIntoView({ block: "nearest", inline: "nearest", behavior: "smooth" });
    }
  }, [store.activeTabId]);

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      {/* Tab strip */}
      <div
        ref={stripRef}
        className="flex h-10 items-center gap-0.5 overflow-x-auto scroll-touch border-b bg-muted/20 px-1 dark:bg-zinc-900/40"
        role="tablist"
        aria-label="Canvas tabs"
      >
        {store.tabs.map((tab) => {
          const isActive = tab.id === store.activeTabId;
          if (tab.kind === "preview") {
            return (
              <button
                key={tab.id}
                data-tab-id={tab.id}
                type="button"
                role="tab"
                aria-selected={isActive}
                onClick={() => store.setActiveTabId(tab.id)}
                className={cn(
                  "group inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium transition-colors",
                  isActive
                    ? "bg-background text-foreground shadow-sm"
                    : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                )}
                title="Live preview (pinned)"
              >
                <MonitorPlay className="size-3.5" />
                <span>Preview</span>
                <Lock className="size-2.5 text-muted-foreground/60" aria-hidden />
              </button>
            );
          }
          const basename = tab.path.replace(/^.*[\\/]/, "");
          const isDirty = store.dirtyPaths.has(tab.path);
          return (
            <div
              key={tab.id}
              data-tab-id={tab.id}
              role="tab"
              aria-selected={isActive}
              className={cn(
                "group inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md text-xs font-medium transition-colors",
                isActive
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
              )}
            >
              <button
                type="button"
                onClick={() => store.setActiveTabId(tab.id)}
                className="inline-flex h-8 items-center gap-1.5 pl-2.5 pr-1"
                title={tab.path}
              >
                {isDirty && (
                  <span
                    className="size-1.5 shrink-0 rounded-full bg-warning"
                    aria-label="modified this session"
                  />
                )}
                <span className="max-w-[160px] truncate">{basename}</span>
              </button>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  store.closeFile(tab.id);
                }}
                className="mr-1 inline-flex size-5 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
                aria-label={`Close ${basename}`}
                title="Close"
              >
                <X className="size-3" />
              </button>
            </div>
          );
        })}
      </div>

      {/* Content area — every tab is kept mounted to avoid Monaco re-init. */}
      <div className="relative min-h-0 flex-1">
        <div
          className={cn(
            "absolute inset-0 transition-opacity",
            store.activeTabId === "preview" ? "opacity-100" : "pointer-events-none opacity-0",
          )}
          aria-hidden={store.activeTabId !== "preview"}
        >
          <CanvasPreview />
        </div>
        {store.tabs.map((tab) => {
          if (tab.kind !== "file") return null;
          const isActive = store.activeTabId === tab.id;
          return (
            <div
              key={tab.id}
              className={cn(
                "absolute inset-0 transition-opacity",
                isActive ? "opacity-100" : "pointer-events-none opacity-0",
              )}
              aria-hidden={!isActive}
            >
              <CanvasFileTab
                path={tab.path}
                isActive={isActive}
                sessionId={chat.sessionId}
              />
            </div>
          );
        })}
      </div>
    </div>
  );
}

function cssAttr(s: string) {
  // CSS attribute selectors don't tolerate ":" without escaping. file tab
  // IDs are "file:/abs/path" — escape the colon so querySelector finds them.
  return s.replace(/:/g, "\\:");
}
