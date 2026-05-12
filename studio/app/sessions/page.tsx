import { redirect } from "next/navigation";

/**
 * The Sessions tab was collapsed into the Live header's session drawer.
 * Any bookmark or in-app link pointing at /sessions just lands on /live;
 * the boss can pop the drawer from the session name there.
 */
export default function SessionsRedirect() {
  redirect("/live");
}
