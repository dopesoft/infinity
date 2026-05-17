import { redirect } from "next/navigation";

// /gym folded into /lab. The "extracted lessons" the old gym page
// confusingly surfaced now live under /lab's Lessons tab with a
// plain-English explainer of what they actually do (inject into every
// turn's system prompt via plasticity.Provider).
export default function GymRedirect() {
  redirect("/lab?tab=lessons");
}
