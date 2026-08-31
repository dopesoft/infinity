"use client";

import * as React from "react";
import { ObjectViewer } from "@/components/dashboard/ObjectViewer";
import { fetchObject } from "@/lib/api";
import type { SearchHit } from "@/lib/api";
import type { DashboardItem, RecordDetail } from "@/lib/dashboard/types";

/**
 * useRecordSheet — open any search hit in place, as a sheet.
 *
 * A search result used to be a link. Tapping one pushed a route carrying
 * `?focus=<id>`, and NO page in Studio read that parameter, so six of the
 * eight kinds landed you on a page with the thing you searched for nowhere in
 * sight. Now the hit opens where you already are: a modal on a desktop, a
 * drawer on a phone, because ObjectViewer routes through ResponsiveModal.
 *
 * Shared by the dashboard search, the ⌘K palette and the ?focus= consumer, so
 * the same hit behaves identically wherever it is clicked. That is the whole
 * reason it is a hook and not three copies of the same effect.
 *
 * THE OPEN IS OPTIMISTIC. The sheet appears on the tap, built from the hit's
 * own title and meta, and fills in when /api/object answers. Fetch-then-open
 * would buy a spinner before a modal on every single tap, for a payload that
 * is usually a few hundred bytes.
 *
 * A failed fetch sets `failed`, which RecordBody renders as an explicit line.
 * It must never leave a record looking merely empty: "I could not load this"
 * and "there is nothing in this" are different facts about the world.
 */
export function useRecordSheet(opts?: {
  /** Fired when the sheet closes. The ?focus= consumer uses it to drop the
   *  params so a dismissed row does not reopen on the next render. */
  onClose?: () => void;
}): {
  open: (hit: SearchHit) => void;
  openById: (kind: string, id: string, title?: string) => void;
  close: () => void;
  item: DashboardItem | null;
  sheet: React.ReactNode;
} {
  const [record, setRecord] = React.useState<RecordDetail | null>(null);
  // Guards a slow detail response for a record the boss already closed or
  // navigated past. Same reason the search hook keeps one.
  const seq = React.useRef(0);

  const load = React.useCallback((kind: string, id: string, stub: RecordDetail) => {
    const mine = ++seq.current;
    setRecord(stub);
    void (async () => {
      const got = await fetchObject(kind, id);
      if (mine !== seq.current) return;
      setRecord(
        got
          ? { ...got, loading: false, failed: false }
          : { ...stub, loading: false, failed: true },
      );
    })();
  }, []);

  const open = React.useCallback(
    (hit: SearchHit) => {
      load(hit.kind, hit.id, {
        kind: hit.kind,
        id: hit.id,
        title: hit.title,
        subtitle: hit.meta,
        body: "",
        fields: [],
        href: hit.href,
        hrefLabel: "",
        loading: true,
      });
    },
    [load],
  );

  const openById = React.useCallback(
    (kind: string, id: string, title?: string) => {
      load(kind, id, {
        kind,
        id,
        title: title ?? "Opening…",
        subtitle: "",
        body: "",
        fields: [],
        href: "",
        hrefLabel: "",
        loading: true,
      });
    },
    [load],
  );

  const onClose = opts?.onClose;
  const close = React.useCallback(() => {
    seq.current++;
    setRecord(null);
    onClose?.();
  }, [onClose]);

  const item: DashboardItem | null = record ? { kind: "record", data: record } : null;

  return {
    open,
    openById,
    close,
    item,
    sheet: <ObjectViewer item={item} onClose={close} />,
  };
}
