"use client";

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useRouter } from "next/navigation";
import { RefreshCw } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * iOS-style pull-to-refresh for the Studio PWA.
 *
 * Standalone Home-Screen PWAs on iOS hide Safari's chrome — which means
 * no native pull-to-refresh, no URL-bar reload, and a stale resume after
 * long backgrounding. This component restores the gesture by listening
 * at the document level, engaging only when window.scrollY === 0, and
 * applying iOS-style resistance to the pull distance.
 *
 * On fire it does two things:
 *   1. router.refresh()  — Next.js App Router cache invalidation +
 *      RSC refetch. Pages that read from Server Components reload their
 *      data; client components that key off the same routes re-mount.
 *   2. dispatchEvent("infinity:pull-to-refresh") — a custom event that
 *      client-side data hooks (memory list, gym list, etc.) can listen
 *      to in order to refetch SWR/useQuery keys without a full reload.
 *
 * Touches that originate inside a dialog/drawer/popover are ignored so
 * the gesture never fights an open modal.
 */

interface PullToRefreshProps {
  threshold?: number;
  children: ReactNode;
}

const RESISTANCE = 2.5;
const MAX_PULL = 140;

export const INFINITY_REFRESH_EVENT = "infinity:pull-to-refresh";

export function PullToRefresh({
  threshold = 70,
  children,
}: PullToRefreshProps) {
  const router = useRouter();
  const [pull, setPull] = useState(0);
  const [refreshing, setRefreshing] = useState(false);
  const [animating, setAnimating] = useState(false);
  const startYRef = useRef<number | null>(null);
  const pullRef = useRef(0);
  const refreshingRef = useRef(false);

  useEffect(() => {
    pullRef.current = pull;
  }, [pull]);
  useEffect(() => {
    refreshingRef.current = refreshing;
  }, [refreshing]);

  const trigger = useCallback(async () => {
    setRefreshing(true);
    setAnimating(true);
    try {
      // Let client components re-fetch their own data sources (SWR /
      // useQuery / WebSocket reconcile) in parallel with the RSC refetch.
      window.dispatchEvent(new CustomEvent(INFINITY_REFRESH_EVENT));
      router.refresh();
      // Hold the spinner for a short minimum so the gesture feels
      // intentional even when the refetch is instant. 350ms is the
      // sweet spot iOS Mail uses.
      await new Promise((resolve) => window.setTimeout(resolve, 350));
    } catch {
      // refresh() doesn't throw; the custom event is best-effort. Any
      // consumer-side errors surface through their own toast paths.
    } finally {
      setRefreshing(false);
      setPull(0);
      window.setTimeout(() => setAnimating(false), 220);
    }
  }, [router]);

  useEffect(() => {
    const onStart = (e: TouchEvent) => {
      if (window.scrollY > 0) return;
      if (refreshingRef.current) return;
      if (e.touches.length !== 1) return;
      // Don't engage when the touch is inside a modal/drawer/popover.
      // Radix-based components in Studio expose [role="dialog"] +
      // [data-radix-*] attributes; sheets carry [data-sheet-panel].
      const target = e.target as HTMLElement | null;
      if (
        target?.closest(
          '[role="dialog"], [role="alertdialog"], [data-sheet-panel], [data-vaul-drawer], [data-radix-popper-content-wrapper], [data-no-pull-to-refresh]',
        )
      ) {
        return;
      }
      startYRef.current = e.touches[0].clientY;
      setAnimating(false);
    };
    const onMove = (e: TouchEvent) => {
      if (startYRef.current == null) return;
      const dy = e.touches[0].clientY - startYRef.current;
      if (dy <= 0) {
        if (pullRef.current !== 0) setPull(0);
        return;
      }
      const resisted = Math.min(dy / RESISTANCE, MAX_PULL);
      setPull(resisted);
    };
    const onEnd = () => {
      if (startYRef.current == null) return;
      startYRef.current = null;
      setAnimating(true);
      if (pullRef.current >= threshold && !refreshingRef.current) {
        void trigger();
      } else {
        setPull(0);
        window.setTimeout(() => setAnimating(false), 220);
      }
    };

    document.addEventListener("touchstart", onStart, { passive: true });
    document.addEventListener("touchmove", onMove, { passive: true });
    document.addEventListener("touchend", onEnd, { passive: true });
    document.addEventListener("touchcancel", onEnd, { passive: true });
    return () => {
      document.removeEventListener("touchstart", onStart);
      document.removeEventListener("touchmove", onMove);
      document.removeEventListener("touchend", onEnd);
      document.removeEventListener("touchcancel", onEnd);
    };
  }, [threshold, trigger]);

  const progress = Math.min(pull / threshold, 1);
  const indicatorY = refreshing ? threshold * 0.7 : pull * 0.55;
  const visible = pull > 4 || refreshing;

  return (
    <>
      <div
        aria-hidden
        className="pointer-events-none fixed left-1/2 top-0 z-[60] -translate-x-1/2"
        style={{
          transform: `translate(-50%, calc(env(safe-area-inset-top) + ${indicatorY}px))`,
          opacity: visible ? 1 : 0,
          transition: animating
            ? "transform 200ms ease-out, opacity 180ms ease-out"
            : undefined,
        }}
      >
        <div className="mt-1 flex h-9 w-9 items-center justify-center rounded-full bg-background/95 shadow-md ring-1 ring-foreground/10 backdrop-blur">
          <RefreshCw
            className={cn(
              "h-4 w-4 text-foreground/70",
              refreshing && "animate-spin",
            )}
            style={
              refreshing
                ? undefined
                : { transform: `rotate(${progress * 270}deg)` }
            }
          />
        </div>
      </div>
      {children}
    </>
  );
}

/**
 * Hook for client components to re-fetch their own data sources when
 * the user pulls to refresh. Call this in any page that has a refetch
 * function tied to SWR / useQuery / local state.
 *
 *   const { mutate } = useSWR("/api/memory");
 *   usePullToRefreshSubscribe(() => mutate());
 */
export function usePullToRefreshSubscribe(onRefresh: () => void): void {
  useEffect(() => {
    const handler = () => onRefresh();
    window.addEventListener(INFINITY_REFRESH_EVENT, handler);
    return () => window.removeEventListener(INFINITY_REFRESH_EVENT, handler);
  }, [onRefresh]);
}
