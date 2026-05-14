"use client";

import { authedFetch } from "@/lib/api";

/* Seed a new /live session bound to a dashboard item.
 *
 * Returns the new session id on success so the caller can navigate to
 * /live?session=<id>. The agent loop reads mem_sessions.seeded_from at
 * turn 1 and hydrates the artifact as a sticky Context block at the
 * top of the conversation.
 *
 * Returns null on any failure — caller should fall back to navigating
 * to /live (unseeded) so the boss can still start typing.
 */
export async function seedSession(
  kind: string,
  id: string,
  snapshot?: unknown,
): Promise<string | null> {
  try {
    const res = await authedFetch("/api/sessions/seed", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ kind, id, snapshot }),
    });
    if (!res.ok) return null;
    const body = (await res.json()) as { id?: string };
    return body.id ?? null;
  } catch {
    return null;
  }
}
