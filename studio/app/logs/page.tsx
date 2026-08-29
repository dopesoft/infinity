import { redirect } from "next/navigation";

/**
 * /logs folded into Activity. "Turns" and "runs" are agent-loop internals
 * that had leaked into a tab strip; they are conversations and jobs, and
 * they belong in the same river as everything else, ordered by time.
 *
 * /logs/[turnId] is NOT redirected — a single turn's detail is still a real
 * destination, and Activity links into it.
 */
export default function LogsRedirect() {
  redirect("/activity");
}
