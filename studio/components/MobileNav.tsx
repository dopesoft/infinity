"use client";

import { useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Menu, Settings, X } from "lucide-react";
import {
  Drawer,
  DrawerContent,
  DrawerTitle,
  DrawerTrigger,
} from "@/components/ui/drawer";
import { ThemeToggle } from "@/components/ThemeToggle";
import { SignOutButton } from "@/components/SignOutButton";
import { NAV_TABS } from "@/lib/nav-tabs";
import { useNavBadge } from "@/lib/nav-badges";
import { cn } from "@/lib/utils";

/* MobileNav is the right-hand hamburger that opens a draggable
 * bottom-sheet drawer on phones. Drawer contains the full nav list +
 * theme toggle. Auto-dismisses when a nav link is tapped. */

// Append Settings here — it's not a primary tab on desktop (it lives
// elsewhere in the header), but on mobile we surface it inside the same
// drawer for one-tap reach.
const tabs = [
  ...NAV_TABS,
  { href: "/settings", label: "Settings", Icon: Settings },
];

export function MobileNav() {
  const pathname = usePathname();
  const [open, setOpen] = useState(false);

  return (
    <Drawer open={open} onOpenChange={setOpen}>
      <DrawerTrigger asChild>
        <button
          type="button"
          aria-label="Open navigation"
          className="inline-flex size-11 items-center justify-center rounded-md text-foreground hover:bg-accent active:scale-95"
        >
          {open ? <X className="size-5" /> : <Menu className="size-5" />}
        </button>
      </DrawerTrigger>
      <DrawerContent>
        <DrawerTitle className="sr-only">Navigation</DrawerTitle>
        <nav className="flex flex-col gap-1 px-3 pb-4 pt-2">
          {tabs.map((t) => {
            const active = pathname === t.href || pathname?.startsWith(t.href + "/");
            const Icon = t.Icon;
            return (
              <Link
                key={t.href}
                href={t.href}
                onClick={() => setOpen(false)}
                className={cn(
                  "flex min-h-12 items-center gap-3 rounded-lg px-3 py-3 text-base transition-colors",
                  active
                    ? "bg-accent text-accent-foreground"
                    : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                )}
              >
                <Icon
                  className={cn("size-5 shrink-0", active ? "text-foreground" : "text-muted-foreground")}
                  aria-hidden
                />
                <span className="flex-1 font-medium">{t.label}</span>
                <MobileNavBadgeChip href={t.href} />
                {active && (
                  <span className="size-1.5 rounded-full bg-foreground" aria-hidden />
                )}
              </Link>
            );
          })}

          <div className="mt-3 flex items-stretch gap-1 border-t pt-3">
            <ThemeToggle variant="cycle-row" className="flex-1" />
            <SignOutButton variant="row" onAfterSignOut={() => setOpen(false)} className="flex-1" />
          </div>
        </nav>
      </DrawerContent>
    </Drawer>
  );
}

// MobileNavBadgeChip — slightly bigger than the desktop chip because the
// mobile drawer rows are wider and thumbs need a meatier target.
function MobileNavBadgeChip({ href }: { href: string }) {
  const count = useNavBadge(href);
  if (count <= 0) return null;
  return (
    <span
      aria-label={`${count} pending`}
      className="inline-flex h-5 min-w-[20px] items-center justify-center rounded-full bg-warning/20 px-1.5 font-mono text-[11px] font-semibold leading-none text-warning"
    >
      {count > 99 ? "99+" : count}
    </span>
  );
}
