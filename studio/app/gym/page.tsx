import { redirect } from "next/navigation";

// /gym folded into Skills along with the rest of /lab. "Gym" was a metaphor
// stacked on a metaphor; what lived there is how he gets better at things.
export default function GymRedirect() {
  redirect("/skills");
}
