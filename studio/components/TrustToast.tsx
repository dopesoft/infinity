"use client";

import { useEffect, useState } from "react";
import { useRouter, usePathname, useSearchParams } from "next/navigation";
import { ShieldCheck, X } from "lucide-react";
import { fetchTrustContracts, type TrustContractDTO } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";
import { cn } from "@/lib/utils";

/**
 * TrustToast - iOS-style sticky banner that appears at the top of the app
 * whenever there's at least one pending trust contract AND the boss isn't
 * already on the Trust review surface (/settings?section=trust) or on
 * /live where the inline approval card on the tool call is also visible.
 *
 * Tap → routes to /settings?section=trust (the dedicated review panel)
 * so the boss never gets accidentally dropped into a chat when they
 * meant to approve. The "x" dismisses for this device for 5 minutes;
 * if more contracts land in that window, the count updates but the
 * banner stays hidden.
 *
 * Single source of truth: /api/trust-contracts?status=pending. We hit it
 * on mount and whenever the mem_trust_contracts realtime channel fires.
 */
const DISMISS_KEY = "infinity:trust-toast:dismissed_until";

function readDismissedUntil(): number {
  if (typeof window === "undefined") return 0;
  try {
    const raw = window.localStorage.getItem(DISMISS_KEY);
    return raw ? Number(raw) : 0;
  } catch {
    return 0;
  }
}

function writeDismissedUntil(ts: number) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(DISMISS_KEY, String(ts));
  } catch {
    /* private mode - banner just reappears next mount */
  }
}

export function TrustToast() {
  const [pending, setPending] = useState<TrustContractDTO[]>([]);
  const [dismissedUntil, setDismissedUntil] = useState<number>(0);
  const [now, setNow] = useState<number>(0);
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();

  useEffect(() => {
    setNow(Date.now());
    setDismissedUntil(readDismissedUntil());
  }, []);

  async function refresh() {
    const rows = await fetchTrustContracts("pending");
    setPending(rows ?? []);
  }

  useEffect(() => {
    refresh();
  }, []);

  useRealtime("mem_trust_contracts", refresh);

  // Hide on /live - the tool card has inline Approve/Deny, no banner needed.
  // Hide on the Trust review surface itself (/settings?section=trust) since
  // the boss is already looking at the full queue. Also hide if dismissed.
  const onTrustSection =
    pathname === "/settings" && searchParams?.get("section") === "trust";
  const hide =
    pending.length === 0 ||
    pathname === "/live" ||
    pathname === "/trust" ||
    onTrustSection ||
    pathname?.startsWith("/login") ||
    (dismissedUntil > 0 && now < dismissedUntil);

  if (hide) return null;

  const first = pending[0];
  const summary = first.title || "Approval needed";

  function onDismiss(e: React.MouseEvent) {
    e.stopPropagation();
    const until = Date.now() + 5 * 60 * 1000;
    setDismissedUntil(until);
    writeDismissedUntil(until);
  }

  function onTap() {
    router.push("/settings?section=trust");
  }

  return (
    <div className="pointer-events-none fixed inset-x-0 top-0 z-[60] flex justify-center pt-safe">
      <button
        type="button"
        onClick={onTap}
        aria-label="Open pending approval"
        className={cn(
          "pointer-events-auto mx-3 mt-2 flex w-full max-w-md items-center gap-2",
          "rounded-2xl border border-warning/40 bg-popover/90 px-3 py-2 shadow-lg backdrop-blur",
          "min-h-12 active:scale-[0.99] transition-transform",
        )}
      >
        <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-warning/15">
          <ShieldCheck className="size-4 text-warning" aria-hidden />
        </span>
        <span className="min-w-0 flex-1 text-left">
          <span className="block truncate text-sm font-semibold text-foreground">
            {pending.length > 1
              ? `${pending.length} approvals pending`
              : "Approval needed"}
          </span>
          <span className="block truncate text-[11px] text-muted-foreground">
            {summary} - tap to review
          </span>
        </span>
        <span
          role="button"
          aria-label="Dismiss for 5 minutes"
          onClick={onDismiss}
          className="-mr-1 flex size-8 shrink-0 items-center justify-center rounded-full text-muted-foreground hover:text-foreground"
        >
          <X className="size-4" />
        </span>
      </button>
    </div>
  );
}
