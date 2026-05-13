"use client";

import { useEffect, useState } from "react";

/* useIsDesktop — matchMedia hook for the lg breakpoint (1024px by default).
 *
 * Used by ObjectViewer so only one of {Dialog, Drawer} mounts at a time.
 * Mounting both means both portals render their overlays into <body>,
 * stacking two backdrop-blurs and creating the "modal looks blurry"
 * visual bug. Render exactly one and the issue disappears.
 */
export function useIsDesktop(breakpointPx = 1024): boolean {
  const [isDesktop, setIsDesktop] = useState(false);
  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const mql = window.matchMedia(`(min-width: ${breakpointPx}px)`);
    setIsDesktop(mql.matches);
    const onChange = (e: MediaQueryListEvent) => setIsDesktop(e.matches);
    mql.addEventListener("change", onChange);
    return () => mql.removeEventListener("change", onChange);
  }, [breakpointPx]);
  return isDesktop;
}
