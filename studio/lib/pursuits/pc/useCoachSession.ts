"use client";

import {
  usePursuitSession,
  type PursuitLiveMessage,
  type PursuitLiveRole,
  type PursuitSession,
} from "@/lib/pursuits/usePursuitSession";

/* Live Jarvis inside the coaching session.
 *
 * The guided beats are deterministic and instant (lib/pursuits/pc/coaching.ts).
 * This hook is the OTHER half: the moment the boss says something the script
 * did not anticipate, that sentence goes to the real agent.
 *
 * The machinery — the app-wide socket filtered to one session, the lazy seed,
 * the streaming reply, the empty-turn guard — lives in usePursuitSession and is
 * shared with the job hunt cockpit. What is coaching-specific, and all this
 * file is allowed to hold, is the seed kind and the sentence he reads when a
 * session could not be opened: on this surface the reassurance that matters is
 * that his programme is written down either way.
 */

export type CoachLiveRole = PursuitLiveRole;
export type CoachLiveMessage = PursuitLiveMessage;
export type CoachSession = PursuitSession;

export function useCoachSession(pursuitId: string): CoachSession {
  return usePursuitSession("pursuit_pc", pursuitId, {
    openFailureMessage:
      "I could not open a conversation just then. Your programme is saved either way.",
  });
}
