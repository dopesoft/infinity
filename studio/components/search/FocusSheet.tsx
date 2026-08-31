"use client";

import * as React from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useRecordSheet } from "@/components/search/RecordSheet";

/**
 * FocusSheet — makes every `?focus=<id>&kind=<kind>` link in the app actually
 * open something.
 *
 * Core has emitted those links from /api/search since the palette shipped, and
 * NOTHING in Studio read the parameter. `/memory?focus=…`, `/skills?focus=…`,
 * `/automations?focus=…` all landed you on the right page with the row you
 * searched for nowhere in sight and no indication anything had gone wrong —
 * built on one side, unwired on the other, which is the same as not built.
 *
 * One consumer, mounted once in AppShell, rather than a reader per page: the
 * link has the same shape on every route, so four copies would be four chances
 * to drift. `kind` rides in the href because an id alone does not say which
 * table it lives in, and probing eight tables per link is eight queries to
 * answer a question the sender already knew the answer to.
 */
export function FocusSheet() {
  const params = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();

  const focus = params.get("focus") ?? "";
  const kind = params.get("kind") ?? "";

  // Held in a ref so `clearParams` does not have to be rebuilt (and re-fire
  // the open effect) every time an unrelated query param changes.
  const latest = React.useRef({ params, pathname });
  latest.current = { params, pathname };

  const clearParams = React.useCallback(() => {
    const { params: p, pathname: path } = latest.current;
    const next = new URLSearchParams(p.toString());
    next.delete("focus");
    next.delete("kind");
    const qs = next.toString();
    router.replace(qs ? `${path}?${qs}` : path, { scroll: false });
  }, [router]);

  const { openById, sheet } = useRecordSheet({ onClose: clearParams });

  // Only (re)open when the TARGET changes. Without the guard, any unrelated
  // query-string edit on the page would re-fetch and re-open the same row.
  const target = focus && kind ? `${kind}:${focus}` : "";
  const opened = React.useRef("");
  React.useEffect(() => {
    if (!target) {
      opened.current = "";
      return;
    }
    if (opened.current === target) return;
    opened.current = target;
    openById(kind, focus);
  }, [target, kind, focus, openById]);

  return <>{sheet}</>;
}
