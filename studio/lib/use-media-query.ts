"use client";

import { useCallback, useSyncExternalStore } from "react";

/**
 * useMediaQuery - SSR-safe matchMedia wrapper built on useSyncExternalStore.
 *
 * Why useSyncExternalStore and not useState(false)+useEffect: the old shape
 * returned `false` on the first render and only flipped to the real value in a
 * mount effect. That's invisible for components mounted at page load (the
 * effect runs before you interact), but it BREAKS any component that mounts
 * later in response to a click — e.g. a modal rendered `{open && <Modal/>}` or
 * one that returns null until it has data. On that fresh mount the first render
 * saw `false`, and <ResponsiveModal> latches its Dialog-vs-Drawer choice on
 * that first render — so on a desktop it wrongly opened a bottom Drawer.
 *
 * useSyncExternalStore's getSnapshot runs on EVERY render, including the first
 * of a freshly-mounted component, so a post-hydration mount reads the true
 * viewport width synchronously. getServerSnapshot returns false so SSR +
 * hydration stay matched (no hydration warning); React then re-syncs to the
 * live value. This is the canonical SSR-safe media-query pattern.
 */
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      if (typeof window === "undefined" || !window.matchMedia) return () => {};
      const mql = window.matchMedia(query);
      mql.addEventListener("change", onChange);
      return () => mql.removeEventListener("change", onChange);
    },
    [query],
  );
  const getSnapshot = () => {
    if (typeof window === "undefined" || !window.matchMedia) return false;
    return window.matchMedia(query).matches;
  };
  const getServerSnapshot = () => false;
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}

/**
 * useIsDesktop - true at Tailwind `lg:` and above (≥1024px). The canonical
 * breakpoint for "render as Dialog instead of Drawer" decisions across
 * Studio. Use this on every modal-style surface so the project pattern
 * holds: Dialog on lg+, Drawer on <lg.
 */
export function useIsDesktop(): boolean {
  return useMediaQuery("(min-width: 1024px)");
}
