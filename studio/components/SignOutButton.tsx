"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { LogOut } from "lucide-react";
import { useAuth } from "@/lib/auth/session";
import { cn } from "@/lib/utils";

type Variant = "icon" | "row";

// SignOutButton renders two ways: a square icon button for the desktop
// header, or a full-width row to slot into the mobile drawer below the
// nav links. Both share the same signOut + redirect logic.
export function SignOutButton({
  variant = "icon",
  onAfterSignOut,
  className,
}: {
  variant?: Variant;
  onAfterSignOut?: () => void;
  className?: string;
}) {
  const { signOut, user } = useAuth();
  const router = useRouter();
  const [busy, setBusy] = useState(false);

  async function handle() {
    if (busy) return;
    setBusy(true);
    try {
      await signOut();
      onAfterSignOut?.();
      router.replace("/login");
      router.refresh();
    } finally {
      setBusy(false);
    }
  }

  if (!user) return null;

  if (variant === "row") {
    return (
      <button
        type="button"
        onClick={handle}
        disabled={busy}
        className={cn(
          "flex min-h-12 w-full items-center justify-center gap-2 rounded-lg px-3 py-3 text-base text-muted-foreground transition-colors hover:bg-accent/60 hover:text-foreground disabled:opacity-60",
          className,
        )}
      >
        <LogOut className="size-5 shrink-0" aria-hidden />
        <span className="font-medium">Sign out</span>
      </button>
    );
  }

  return (
    <button
      type="button"
      aria-label="Sign out"
      onClick={handle}
      disabled={busy}
      className={cn(
        "inline-flex size-9 items-center justify-center rounded-md text-foreground hover:bg-accent active:scale-95 disabled:opacity-60 lg:size-10",
        className,
      )}
    >
      <LogOut className="size-4" aria-hidden />
    </button>
  );
}
