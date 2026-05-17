import { redirect } from "next/navigation";

// /code-proposals folded into /lab. See nav-tabs.ts for rationale.
export default function CodeProposalsRedirect() {
  redirect("/lab?tab=proposals");
}
