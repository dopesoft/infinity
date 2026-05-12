"use client";

import { useEffect, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth/session";
import { fetchProfile, getMeta } from "@/lib/api";

const EXEMPT_PREFIXES = ["/login", "/onboarding"];

/* OnboardingGate runs once per session after the user is signed in. On first
 * load we check two signals: the infinity_meta "boss_onboarded" flag and the
 * count of boss-profile facts. If both are absent we route the user to the
 * First Run wizard so the agent starts with identity context instead of a
 * blank slate. The gate is silent on subsequent loads — once boss_onboarded
 * is set (either by completing the wizard or skipping it) we never redirect
 * again. Routes under /login or /onboarding bypass the check so the wizard
 * itself isn't trapped in a redirect loop. */
export function OnboardingGate({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth();
  const router = useRouter();
  const pathname = usePathname();
  const [checked, setChecked] = useState(false);

  useEffect(() => {
    if (loading) return;
    if (!user) {
      setChecked(true);
      return;
    }
    if (pathname && EXEMPT_PREFIXES.some((p) => pathname.startsWith(p))) {
      setChecked(true);
      return;
    }
    let cancelled = false;
    (async () => {
      const flag = await getMeta("boss_onboarded");
      if (cancelled) return;
      if (flag === "true") {
        setChecked(true);
        return;
      }
      const profile = await fetchProfile();
      if (cancelled) return;
      if (profile && profile.length > 0) {
        setChecked(true);
        return;
      }
      router.replace("/onboarding");
    })();
    return () => {
      cancelled = true;
    };
  }, [loading, user, pathname, router]);

  if (!checked) {
    return (
      <div className="flex h-app min-h-app items-center justify-center bg-background text-sm text-muted-foreground">
        Loading…
      </div>
    );
  }
  return <>{children}</>;
}
