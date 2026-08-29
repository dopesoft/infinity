import { redirect } from "next/navigation";

// /code-proposals folded into Skills: a fix he drafted for his own code is a
// skill of his that is not working.
export default function CodeProposalsRedirect() {
  redirect("/skills");
}
