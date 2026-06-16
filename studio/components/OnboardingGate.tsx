"use client";

import { useEffect } from "react";
import { usePathname, useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth/session";
import { fetchProfile, getMeta } from "@/lib/api";

const EXEMPT_PREFIXES = ["/login", "/onboarding"];
const LOCAL_FLAG = "boss_onboarded";

/* OnboardingGate runs once per session after the user is signed in. On first
 * load we check two signals: the infinity_meta "boss_onboarded" flag and the
 * count of boss-profile facts. If both are absent we route the user to the
 * First Run wizard so the agent starts with identity context instead of a
 * blank slate. The gate is silent on subsequent loads - once boss_onboarded
 * is set (either by completing the wizard or skipping it) we never redirect
 * again. Routes under /login or /onboarding bypass the check so the wizard
 * itself isn't trapped in a redirect loop. */
export function OnboardingGate({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth();
  const router = useRouter();
  const pathname = usePathname();

  // The onboarding check is a REDIRECT-ONLY concern - a brand-new boss with no
  // profile gets routed to the wizard. It must NOT gate first paint: blocking
  // the whole app behind getMeta + fetchProfile (each a network round-trip,
  // re-run on every iOS-PWA cold start where localStorage is evicted) was the
  // "5-10s of Loading… before anything showed" the boss hit - and it sat in
  // front of the dashboard's own SWR cache, negating it. So we render children
  // immediately and run the check in the background, redirecting only if the
  // boss genuinely hasn't onboarded. An already-onboarded boss never sees a
  // flash; a brand-new one briefly sees the shell before the wizard (one-time).
  useEffect(() => {
    if (loading || !user) return;
    if (pathname && EXEMPT_PREFIXES.some((p) => pathname.startsWith(p))) return;
    if (typeof window !== "undefined" && localStorage.getItem(LOCAL_FLAG) === "true") return;
    let cancelled = false;
    (async () => {
      const flag = await getMeta("boss_onboarded");
      if (cancelled) return;
      if (flag === "true") {
        try {
          localStorage.setItem(LOCAL_FLAG, "true");
        } catch {}
        return;
      }
      const profile = await fetchProfile();
      if (cancelled) return;
      if (profile && profile.length > 0) {
        // Onboarded (has profile facts) - persist the fast-path flag so we
        // never pay this network check again on this device.
        try {
          localStorage.setItem(LOCAL_FLAG, "true");
        } catch {}
        return;
      }
      router.replace("/onboarding");
    })();
    return () => {
      cancelled = true;
    };
  }, [loading, user, pathname, router]);

  return <>{children}</>;
}
