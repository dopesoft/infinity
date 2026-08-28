"use client";

import { authedFetch } from "@/lib/api";
import type { PCAction, PCCockpit, PCWriteRequest } from "./types";

/* Psycho-Cybernetics cockpit client.
 *
 * Every write returns the refreshed cockpit, so a caller never has to follow a
 * mutation with a second read to stay in sync. Errors are thrown with the
 * server's own message rather than swallowed: a coaching answer that silently
 * failed to save is worse than one that visibly did not.
 */

export async function fetchCockpit(
  pursuitId: string,
  signal?: AbortSignal,
): Promise<PCCockpit> {
  const res = await authedFetch(
    `/api/pursuits/pc/state?pursuit_id=${encodeURIComponent(pursuitId)}`,
    { signal },
  );
  if (!res.ok) throw new Error(await errorText(res));
  return (await res.json()) as PCCockpit;
}

export async function writeCockpit(
  pursuitId: string,
  action: PCAction,
  body: PCWriteRequest,
): Promise<PCCockpit> {
  const res = await authedFetch(`/api/pursuits/pc/${action}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ pursuit_id: pursuitId, ...body }),
  });
  if (!res.ok) throw new Error(await errorText(res));
  return (await res.json()) as PCCockpit;
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
