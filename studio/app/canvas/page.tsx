import { redirect } from "next/navigation";

/* /canvas is now folded into /live. Chat + files/git + canvas live in one
 * unified workspace; standalone /canvas was retired with the v2 redesign.
 * Existing deep-links still land somewhere sensible. */
export default function CanvasRedirect() {
  redirect("/live");
}
