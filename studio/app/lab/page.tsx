import { redirect } from "next/navigation";

/**
 * /lab folded into Skills. A candidate skill, a broken skill and a learned
 * lesson are all skills, so four tabs on a second page were four tabs too
 * many. Permanent redirect: old links in chat history and notifications
 * still land somewhere real.
 */
export default function LabRedirect() {
  redirect("/skills");
}
