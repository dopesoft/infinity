import { redirect } from "next/navigation";

// /audit folded into Activity, which is now the single record of what he did
// and what he noticed, ordered by time.
export default function AuditRedirect() {
  redirect("/activity");
}
