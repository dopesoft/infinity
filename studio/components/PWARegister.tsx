"use client";

import { useEffect } from "react";
import { registerServiceWorker } from "@/lib/push";

/* PWARegister - mounts the service worker once when Studio loads.
 *
 * Rendered inside <RootLayout> so it's active on every route. SW
 * registration is idempotent - calling register('/sw.js') with an
 * already-installed worker simply resolves with the existing
 * registration. Failures are swallowed (browsers without SW support).
 */
export function PWARegister() {
  useEffect(() => {
    void registerServiceWorker();
  }, []);
  return null;
}
