import { redirect } from "next/navigation";

// /audit's old home was /gym?tab=audit. /gym folded into /lab, and
// system-event auditing is closer in spirit to /heartbeat (system
// observations + findings) than to /lab (proposals + lessons), so
// land there.
export default function AuditRedirect() {
  redirect("/heartbeat");
}
