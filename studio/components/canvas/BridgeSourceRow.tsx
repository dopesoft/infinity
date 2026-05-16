"use client";

import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";
import {
  fetchBridgeSession,
  fetchBridgeStatus,
  type BridgeSessionView,
  type BridgeStatus,
} from "@/lib/canvas/api";

/**
 * BridgeSourceRow — tiny single-line label under the Files filter row
 * declaring which bridge owns the filesystem currently rendered. Matches
 * the visual mass of DeployStatusRow (border-b, font-mono 11px, colored
 * dot) so the two stack cleanly when both render.
 *
 * Hidden when:
 *   - no session is attached (nothing to scope to)
 *   - bridge is not configured at all
 *   - the active bridge is unknown (router hasn't picked yet)
 */
export function BridgeSourceRow({ sessionId }: { sessionId: string | null }) {
  const [status, setStatus] = useState<BridgeStatus | null>(null);
  const [session, setSession] = useState<BridgeSessionView | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    void (async () => {
      const s = await fetchBridgeStatus(ac.signal);
      if (s) setStatus(s);
    })();
    return () => ac.abort();
  }, []);

  useEffect(() => {
    if (!sessionId) {
      setSession(null);
      return;
    }
    let alive = true;
    const ac = new AbortController();
    const tick = async () => {
      const next = await fetchBridgeSession(sessionId, ac.signal);
      if (!alive || !next) return;
      setSession(next);
    };
    void tick();
    const id = setInterval(() => void tick(), 10_000);
    return () => {
      alive = false;
      clearInterval(id);
      ac.abort();
    };
  }, [sessionId]);

  if (!sessionId) return null;
  if (!status?.configured) return null;
  if (!session?.active_kind) return null;

  const isMac = session.active_kind === "mac";
  const dotClass = isMac ? "bg-success" : "bg-info";
  const textClass = isMac ? "text-success" : "text-info";
  const bgClass = isMac ? "bg-success/5" : "bg-info/5";

  return (
    <div
      className={cn(
        "flex shrink-0 items-center gap-2 border-b px-3 py-1 text-[11px]",
        bgClass,
        textClass,
      )}
    >
      <span className={cn("inline-block size-2 rounded-full", dotClass)} aria-hidden />
      <span className="font-mono">Source: {isMac ? "Mac" : "Cloud"}</span>
    </div>
  );
}
