"use client";

import { useCallback } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";

/**
 * URL-backed tab / section state. The active value lives in a query param so
 * it survives a hard refresh, browser back/forward, and is shareable as a
 * deep-link — NEVER component-local `useState`, which evaporates the moment
 * the page reloads (that's the "refresh dumps me back on the first tab" bug).
 *
 * Drop-in shape: returns `[value, setValue]` exactly like `useState`, so a
 * consumer migrates by swapping one line. The value is derived straight from
 * the URL each render, so it stays in sync with back/forward automatically.
 *
 * `setValue` rewrites ONLY `key` in the query string — every other param is
 * preserved — via a shallow `router.replace` (`scroll: false`, no full
 * reload, no scroll jump). Selecting the default value clears the param so
 * the canonical URL for the default tab stays clean.
 *
 * Pass `allowed` to harden against a hand-edited / stale URL: an unknown value
 * falls back to `defaultValue` instead of rendering an empty tab.
 *
 * This is the one place tab persistence is implemented; route every
 * page-level tab/section switcher through it (Reuse-first rule).
 */
export function useTabParam<T extends string>(
  key: string,
  defaultValue: T,
  allowed?: readonly T[],
): [T, (next: T) => void] {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();

  const raw = searchParams.get(key);
  const value: T =
    raw != null && (!allowed || (allowed as readonly string[]).includes(raw))
      ? (raw as T)
      : defaultValue;

  const setValue = useCallback(
    (next: T) => {
      const params = new URLSearchParams(searchParams.toString());
      if (next === defaultValue) {
        params.delete(key);
      } else {
        params.set(key, next);
      }
      const qs = params.toString();
      router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false });
    },
    [router, pathname, searchParams, key, defaultValue],
  );

  return [value, setValue];
}
