"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Menu } from "lucide-react";
import {
  Drawer,
  DrawerContent,
  DrawerTitle,
  DrawerTrigger,
} from "@/components/ui/drawer";
import { ThemeToggle } from "@/components/ThemeToggle";
import { SignOutButton } from "@/components/SignOutButton";
import { NAV, isNavActive } from "@/lib/nav-tabs";
import { useNavBadge } from "@/lib/nav-badges";
import { cn } from "@/lib/utils";
import { PRESS_ICON, PRESS_ROW } from "@/components/ui/press";
import { useNavTarget } from "@/lib/loading";

/**
 * NavDrawer - the phone navigation. A hamburger and a bottom sheet.
 *
 * Deliberately NOT a bottom tab bar. A bar costs ~52pt of height on every
 * screen for the whole life of the app, which on a phone is a whole row of
 * content, and section switching is not frequent enough to pay that rent.
 * A sheet costs nothing at rest, carries the count beside every row so you
 * can see what wants you before you pick, and dismisses with the same swipe
 * down as everything else on the phone.
 *
 * Same `NAV` registry as the desktop rail, in the same order, so the
 * hierarchy you learn in one place is true in the other. That is the thing
 * the old pills-plus-kebab-plus-hamburger arrangement could never manage.
 */
export function NavDrawer() {
  const pathname = usePathname();
  const [open, setOpen] = useState(false);

  // Close on navigation. Without this the sheet survives a route change on
  // iOS when the tap lands on an already-active row.
  useEffect(() => {
    setOpen(false);
  }, [pathname]);

  const navTarget = useNavTarget();

  return (
    <Drawer open={open} onOpenChange={setOpen}>
      <DrawerTrigger asChild>
        <button
          type="button"
          aria-label="Open navigation"
          className={cn(
            "grid size-11 shrink-0 place-items-center rounded-lg text-foreground",
            PRESS_ICON,
          )}
        >
          <Menu className="size-5" aria-hidden />
        </button>
      </DrawerTrigger>
      <DrawerContent>
        <DrawerTitle className="sr-only">Navigation</DrawerTitle>
        <nav className="flex flex-col px-2 pb-3 pt-1">
          {NAV.map((entry) => (
            <DrawerRow
              key={entry.href}
              href={entry.href}
              label={entry.label}
              Icon={entry.Icon}
              here={isNavActive(pathname, entry.href)}
              lit={isNavActive(navTarget ?? pathname, entry.href)}
              // Dismiss on the PRESS, not when the new screen arrives. The
              // sheet used to sit there for the whole navigation, so the one
              // thing covering the screen was also the one thing giving no
              // sign it had heard you. The pathname effect above stays as the
              // backstop for a nav that starts anywhere else.
              onNavigate={() => setOpen(false)}
            />
          ))}
          <div className="mt-2 flex items-stretch gap-1.5 border-t border-hairline px-1 pt-3">
            <ThemeToggle variant="cycle-row" className="flex-1" />
            <SignOutButton
              variant="row"
              onAfterSignOut={() => setOpen(false)}
              className="flex-1"
            />
          </div>
        </nav>
      </DrawerContent>
    </Drawer>
  );
}

function DrawerRow({
  href,
  label,
  Icon,
  here,
  lit,
  onNavigate,
}: {
  href: string;
  label: string;
  Icon: React.ComponentType<{ className?: string }>;
  /** Where you are. Drives `aria-current` only. */
  here: boolean;
  /** Where the app is pointing — the destination while a nav is in flight. */
  lit: boolean;
  onNavigate: () => void;
}) {
  const count = useNavBadge(href);
  return (
    <Link
      href={href}
      onClick={onNavigate}
      aria-current={here ? "page" : undefined}
      className={cn(
        "relative flex min-h-12 items-center gap-3 rounded-lg px-3",
        PRESS_ROW,
        // Same law as the rail: ink and a marker, not a filled block.
        lit ? "text-foreground" : "text-muted-foreground",
      )}
    >
      <Icon
        className={cn("size-[18px] shrink-0", lit ? "text-foreground" : "text-quiet")}
      />
      <span className={cn("flex-1 text-[15px]", lit && "font-medium")}>{label}</span>
      {lit ? (
        <span aria-hidden className="absolute inset-y-2 left-0 w-[2px] rounded-full bg-foreground" />
      ) : null}
      {count > 0 && (
        <span className="font-mono text-[11px] tabular-nums text-warning">
          {count > 99 ? "99+" : count}
        </span>
      )}
    </Link>
  );
}
