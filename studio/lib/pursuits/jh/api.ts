"use client";

import { authedFetch } from "@/lib/api";
import type { JHAction, JHCockpit, JHWriteRequest } from "./types";

/* Job Hunt cockpit client. Sibling of lib/pursuits/pc/api.ts, deliberately the
 * same shape.
 *
 * Every write returns the refreshed board, so a caller never has to follow a
 * mutation with a second read to stay in sync and can never render a board it
 * is one mutation ahead of. Errors are thrown carrying the server's own
 * message: a stage that would not move, or a value the store rejected, names
 * what would have worked, and swallowing that turns a refused write into a
 * board that silently did not change.
 */

export async function fetchCockpit(
  pursuitId: string,
  signal?: AbortSignal,
): Promise<JHCockpit> {
  const res = await authedFetch(
    `/api/pursuits/jh/state?pursuit_id=${encodeURIComponent(pursuitId)}`,
    { signal },
  );
  if (!res.ok) throw new Error(await errorText(res));
  return (await res.json()) as JHCockpit;
}

export async function writeCockpit(
  pursuitId: string,
  action: JHAction,
  body: JHWriteRequest,
): Promise<JHCockpit> {
  const res = await authedFetch(`/api/pursuits/jh/${action}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ pursuit_id: pursuitId, ...body }),
  });
  if (!res.ok) throw new Error(await errorText(res));
  return (await res.json()) as JHCockpit;
}

async function errorText(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { error?: string };
    if (body.error) return body.error;
  } catch {
    // fall through to the status line
  }
  return `request failed (${res.status})`;
}
