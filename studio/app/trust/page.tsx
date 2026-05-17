import { redirect } from "next/navigation";

/**
 * /trust has been folded into Settings (audit + bulk pending) per the
 * IA defrag (2026-05-16). Real-time approvals still live inline in
 * /live as ToolCallCard awaiting_approval. The dashboard "Need you"
 * badge counts the same pending bucket.
 *
 * Keeping the route as a permanent 308 so links from chat history,
 * notifications, and the dashboard still land somewhere useful.
 */
export default function TrustRedirect() {
  redirect("/settings?section=trust");
}
