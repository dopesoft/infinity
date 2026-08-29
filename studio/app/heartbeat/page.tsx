import { redirect } from "next/navigation";

/**
 * /heartbeat folded into Activity. "What he noticed" and "what he did" were
 * never two questions, and a page named after a health check is a page named
 * after its mechanism. Permanent redirect.
 */
export default function HeartbeatRedirect() {
  redirect("/activity");
}
