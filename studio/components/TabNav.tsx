"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { cn } from "@/lib/utils";
import { NAV_TABS } from "@/lib/nav-tabs";
import { useNavBadge } from "@/lib/nav-badges";

export function TabNav() {
  const pathname = usePathname();

  return (
    <nav
      role="tablist"
      aria-label="Primary"
      className="flex items-center gap-1 rounded-lg bg-muted p-1 text-muted-foreground"
    >
      {NAV_TABS.map((tab) => {
        const active = pathname === tab.href || pathname?.startsWith(tab.href + "/");
        const Icon = tab.Icon;
        return (
          <Link
            key={tab.href}
            href={tab.href}
            role="tab"
            aria-selected={active}
            className={cn(
              "inline-flex min-h-9 items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
              active
                ? "bg-background text-foreground shadow-sm"
                : "hover:text-foreground",
            )}
          >
            <Icon className="size-4 shrink-0" aria-hidden />
            {tab.label}
            <NavBadgeChip href={tab.href} />
          </Link>
        );
      })}
    </nav>
  );
}

// NavBadgeChip renders a small warning pill with the pending-action count
// next to the tab label. Hidden when the count is 0 so quiet tabs stay
// visually quiet. Uses the warning palette so it pops against the muted
// inactive-tab background and the surface of the active tab equally.
function NavBadgeChip({ href }: { href: string }) {
  const count = useNavBadge(href);
  if (count <= 0) return null;
  return (
    <span
      aria-label={`${count} pending`}
      className="ml-0.5 inline-flex h-4 min-w-[16px] items-center justify-center rounded-full bg-warning/20 px-1 font-mono text-[10px] font-semibold leading-none text-warning"
    >
      {count > 99 ? "99+" : count}
    </span>
  );
}
