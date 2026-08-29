import { redirect } from "next/navigation";

// Trust is the mechanism; approving is the thing you do. The queue lives in
// Settings under "Approvals"; real-time approvals still come to you inline
// in Chat, which is where you can actually answer them.
export default function TrustRedirect() {
  redirect("/settings?section=trust");
}
