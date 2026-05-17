"use client";

import { useEffect, useState } from "react";

/**
 * useMediaQuery - SSR-safe matchMedia wrapper. Returns false during
 * server render, hydrates to the real value on mount, then stays live.
 */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(false);
  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const mql = window.matchMedia(query);
    setMatches(mql.matches);
    const onChange = (e: MediaQueryListEvent) => setMatches(e.matches);
    mql.addEventListener("change", onChange);
    return () => mql.removeEventListener("change", onChange);
  }, [query]);
  return matches;
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
