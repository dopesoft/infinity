"use client";

import { useEffect, useState } from "react";
import { authedFetch } from "@/lib/api";

// Chat attachments. The composer uploads bytes to Core FIRST (multipart), then
// the WS frame references the returned ids. Core turns those ids into native
// image / PDF blocks for the brain, mirrors the file onto Jarvis's workspace
// and persists the metadata on the turn so the chip survives reload.

export type UploadedAttachment = {
  id: string;
  name: string;
  mime_type?: string;
  size_bytes?: number;
  /** Set for images: the JWT-protected raw route (fetch via authedFetch). */
  preview_url?: string;
  /** Where the file landed on the cloud workspace, when the bridge was up. */
  storage_path?: string;
  extract_status?: "pending" | "ok" | "empty" | "failed" | "skipped";
  extract_error?: string;
  page_count?: number;
  text_preview?: string;
};

export type UploadResult = {
  ok: UploadedAttachment[];
  failed: { name: string; error: string }[];
};

/** Per-file cap mirrors attachments.MaxUploadBytes on Core. */
export const MAX_ATTACHMENT_BYTES = 25 * 1024 * 1024;

export async function uploadAttachments(sessionId: string, files: File[]): Promise<UploadResult> {
  const result: UploadResult = { ok: [], failed: [] };
  const send: File[] = [];
  for (const f of files) {
    if (f.size > MAX_ATTACHMENT_BYTES) {
      result.failed.push({ name: f.name, error: `over the ${MAX_ATTACHMENT_BYTES / 1024 / 1024} MB limit` });
      continue;
    }
    send.push(f);
  }
  if (send.length === 0) return result;
  const form = new FormData();
  form.set("session_id", sessionId);
  for (const f of send) form.append("file", f, f.name);
  try {
    const res = await authedFetch("/api/attachments/upload", { method: "POST", body: form });
    const body = (await res.json().catch(() => null)) as
      | { attachments?: UploadedAttachment[]; failed?: { name: string; error: string }[]; error?: string }
      | null;
    if (!res.ok && (!body || !Array.isArray(body.attachments) || body.attachments.length === 0)) {
      const reason = body?.error || body?.failed?.[0]?.error || `upload failed (${res.status})`;
      for (const f of send) result.failed.push({ name: f.name, error: reason });
      return result;
    }
    result.ok.push(...(body?.attachments ?? []));
    result.failed.push(...(body?.failed ?? []));
  } catch (err) {
    const reason = err instanceof Error ? err.message : "network error";
    for (const f of send) result.failed.push({ name: f.name, error: reason });
  }
  return result;
}

/** The raw-bytes route for an attachment id (JWT-protected; use the hook or openAttachment). */
export function attachmentRawPath(id: string): string {
  return `/api/attachments/${encodeURIComponent(id)}/raw`;
}

function isLocalObjectUrl(url: string): boolean {
  return url.startsWith("blob:") || url.startsWith("data:");
}

const objectUrlCache = new Map<string, Promise<string | null>>();

/** Fetch a JWT-protected attachment and return a browser object URL (cached per path). */
export function fetchAttachmentObjectUrl(path: string): Promise<string | null> {
  if (isLocalObjectUrl(path)) return Promise.resolve(path);
  const cached = objectUrlCache.get(path);
  if (cached) return cached;
  const p = (async () => {
    try {
      const res = await authedFetch(path);
      if (!res.ok) return null;
      const blob = await res.blob();
      return URL.createObjectURL(blob);
    } catch {
      return null;
    }
  })();
  objectUrlCache.set(path, p);
  p.then((url) => {
    if (!url) objectUrlCache.delete(path);
  });
  return p;
}

/**
 * Resolves an attachment URL to something an <img> can render. Local
 * blob:/data: URLs pass straight through; Core raw routes are fetched with
 * the JWT and turned into an object URL (cached across re-renders).
 */
export function useAttachmentObjectUrl(url?: string): string | undefined {
  const [resolved, setResolved] = useState<string | undefined>(() =>
    url && isLocalObjectUrl(url) ? url : undefined,
  );
  useEffect(() => {
    if (!url) {
      setResolved(undefined);
      return;
    }
    if (isLocalObjectUrl(url)) {
      setResolved(url);
      return;
    }
    let cancelled = false;
    void fetchAttachmentObjectUrl(url).then((u) => {
      if (!cancelled) setResolved(u ?? undefined);
    });
    return () => {
      cancelled = true;
    };
  }, [url]);
  return resolved;
}

/** Open an attachment in a new tab (fetches with the JWT first). */
export async function openAttachment(path: string): Promise<boolean> {
  const url = await fetchAttachmentObjectUrl(path);
  if (!url) return false;
  window.open(url, "_blank", "noopener");
  return true;
}
