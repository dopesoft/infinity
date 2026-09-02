"use client";

import {
  usePursuitSession,
  type PursuitLiveMessage,
  type PursuitSession,
} from "@/lib/pursuits/usePursuitSession";

/* Live Jarvis inside the job hunt cockpit.
 *
 * Sibling of lib/pursuits/pc/useCoachSession, over the same shared machinery.
 * The seed kind is the whole difference: "pursuit_jh" is the branch in
 * sessions_seed_api.go that hydrates turn one with jh.FormatChatContext, so the
 * first thing the boss says arrives at an agent that already knows every role,
 * its stage, its fit and ghost signals, the interview material banked, where
 * each outreach stands, and what has been written for which role.
 *
 * Unlike the coaching programme there is no beat script here. This is a plain
 * conversation about the board, so everything the boss types goes to the real
 * agent and nothing is answered locally.
 */

export type JHLiveMessage = PursuitLiveMessage;
export type JHSession = PursuitSession;

export function useJobHuntSession(pursuitId: string): JHSession {
  return usePursuitSession("pursuit_jh", pursuitId, {
    openFailureMessage:
      "I could not open a conversation just then. Your board is untouched either way.",
  });
}
