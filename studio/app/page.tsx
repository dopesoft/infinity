import { DashboardClient } from "@/components/dashboard/DashboardClient";

/* Dashboard is the root surface of the app - the cockpit Jarvis presents
 * when the boss opens Studio. Layout, motion, and ObjectViewer routing
 * all live in <DashboardClient>; this stub exists just so Next.js wires
 * `/` to the right component.
 */
export default function Home() {
  return <DashboardClient />;
}
