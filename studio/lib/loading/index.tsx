"use client";

import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useState,
  useSyncExternalStore,
} from "react";
import { usePathname, useRouter } from "next/navigation";
import {
  beginLoading,
  endLoading,
  loadingServerSnapshot,
  loadingSnapshot,
  navTargetServerSnapshot,
  navTargetSnapshot,
  setNavTarget,
  subscribeLoading,
} from "./store";

export { beginLoading, endLoading } from "./store";

/** The key every route change shares, so two clicks are still one spinner. */
const ROUTE_KEY = "route";
/** A navigation that has not landed in 10s is not landing. */
const ROUTE_TTL_MS = 10_000;
/** A page whose own fetch has not returned in 20s has failed, not stalled. */
const PAGE_TTL_MS = 20_000;

/**
 * True while a screen is on its way in — a route change in flight, or the
 * page that landed still fetching its first payload.
 *
 * This is deliberately NOT the agent working. Jarvis thinking, a tool
 * running, a reply streaming: those have their own surfaces (the chat
 * activity ledger, <RunIndicator>, the bridge pill) and the app mark stays
 * still through all of them. This flag answers one question only: "did my
 * tap do anything?"
 */
export function useAppLoading(): boolean {
  return useSyncExternalStore(subscribeLoading, loadingSnapshot, loadingServerSnapshot);
}

/**
 * The path the app is navigating TO right now, or null when it is sitting
 * still. The rail and the drawer read it so the marker moves to the item you
 * pressed immediately, rather than a beat later when the screen lands — which
 * is the difference between a nav that answers you and one you press twice.
 */
export function useNavTarget(): string | null {
  return useSyncExternalStore(
    subscribeLoading,
    navTargetSnapshot,
    navTargetServerSnapshot,
  );
}

/**
 * Declare that THIS component is fetching the screen the boss just landed on.
 *
 * Pass the page's own loading flag. The hold is released the FIRST time that
 * flag goes false and is never taken again for the life of the component —
 * which is the whole semantic, and the reason this is one hook rather than a
 * choice each page makes for itself. "The screen is loading" means the gap
 * between arriving somewhere and it having content in it. Everything after
 * that (a realtime push, a debounced search, a poll) happens with the page
 * already on screen and must not move the mark: a logo that spins whenever a
 * row changes upstream is noise, and noise is what he stops reading.
 *
 * The hold is keyed to the component instance and released on unmount, so
 * navigating away mid-fetch can never strand it.
 */
export function usePageLoading(loading: boolean): void {
  const key = `page:${useId()}`;
  const [settled, setSettled] = useState(false);
  const active = loading && !settled;

  useEffect(() => {
    if (!loading) setSettled(true);
  }, [loading]);

  useEffect(() => {
    if (!active) {
      endLoading(key);
      return;
    }
    beginLoading(key, PAGE_TTL_MS);
    return () => endLoading(key);
  }, [active, key]);
}

/**
 * `useRouter()`, but a programmatic jump to another screen lights the mark
 * the same way clicking a link does.
 *
 * Every cross-page `router.push` / `router.replace` in Studio goes through
 * this. A plain `useRouter()` is still correct for a same-page query update
 * (`useTabParam`, <FocusSheet>) — that is not a screen load and must not
 * read as one.
 */
export function useAppRouter() {
  const router = useRouter();

  const mark = useCallback((href: string) => {
    if (typeof window === "undefined") return;
    try {
      const url = new URL(href, window.location.href);
      if (url.pathname === window.location.pathname) return;
      setNavTarget(url.pathname);
    } catch {
      return;
    }
    beginLoading(ROUTE_KEY, ROUTE_TTL_MS);
  }, []);

  return useMemo(
    () => ({
      push: (href: string, options?: Parameters<typeof router.push>[1]) => {
        mark(href);
        router.push(href, options);
      },
      replace: (href: string, options?: Parameters<typeof router.replace>[1]) => {
        mark(href);
        router.replace(href, options);
      },
      back: () => {
        beginLoading(ROUTE_KEY, ROUTE_TTL_MS);
        router.back();
      },
      forward: () => {
        beginLoading(ROUTE_KEY, ROUTE_TTL_MS);
        router.forward();
      },
      refresh: () => router.refresh(),
      prefetch: (href: string) => router.prefetch(href),
    }),
    [router, mark],
  );
}

/**
 * RouteLoadingWatcher — mounted once, in the root layout. Renders nothing.
 *
 * THE CHOKEPOINT. Next 14's App Router has no navigation events, so the
 * alternative to this was a prop threaded through every <Link> in the app —
 * which is the same failure as a header each screen draws itself: some links
 * would get it, some would not, and which ones would drift. Instead one
 * capture-phase listener on the document sees EVERY internal anchor click
 * before React does, and one `usePathname()` effect ends the hold the moment
 * the new screen commits. Add a link anywhere in Studio and it is covered by
 * construction; there is nothing to remember.
 *
 * Same-path clicks are ignored on purpose: a tab strip that writes `?tab=`
 * is not a screen load, and spinning for it would train him to ignore the
 * mark.
 */
export function RouteLoadingWatcher() {
  const pathname = usePathname();

  // The new screen is on the page: the hold is done. Also runs on first
  // mount, which is the reload case (nothing held, so it is a no-op).
  useEffect(() => {
    setNavTarget(null);
    endLoading(ROUTE_KEY);
  }, [pathname]);

  useEffect(() => {
    function onClick(e: MouseEvent) {
      // A modified click opens a tab; the current screen is not going
      // anywhere, so nothing should suggest it is.
      if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;

      const anchor = (e.target as Element | null)?.closest?.("a[href]") as
        | HTMLAnchorElement
        | null;
      if (!anchor) return;
      if (anchor.hasAttribute("download")) return;
      if (anchor.target && anchor.target !== "_self") return;

      let url: URL;
      try {
        url = new URL(anchor.href, window.location.href);
      } catch {
        return;
      }
      if (url.origin !== window.location.origin) return;
      if (url.pathname === window.location.pathname) return;

      setNavTarget(url.pathname);
      beginLoading(ROUTE_KEY, ROUTE_TTL_MS);
    }

    function onPopState() {
      beginLoading(ROUTE_KEY, ROUTE_TTL_MS);
    }

    document.addEventListener("click", onClick, true);
    window.addEventListener("popstate", onPopState);
    return () => {
      document.removeEventListener("click", onClick, true);
      window.removeEventListener("popstate", onPopState);
    };
  }, []);

  return null;
}
