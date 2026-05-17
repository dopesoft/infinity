"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { NAV_TABS } from "@/lib/nav-tabs";
import { useNavBadge } from "@/lib/nav-badges";

/* Header tab nav. Calm pill-shaped chips, active tab gets a soft
 * surface + brand dot. Counts ride on the right as a pill Badge. */
export function TabNav() {
  const pathname = usePathname();

  return (
    <nav
      role="tablist"
      aria-label="Primary"
      className="flex items-center gap-1 rounded-full border bg-card p-1"
    >
      {NAV_TABS.map((tab) => {
        const active =
          pathname === tab.href || pathname?.startsWith(tab.href + "/");
        const Icon = tab.Icon;
        return (
          <Link
            key={tab.href}
            href={tab.href}
            role="tab"
            aria-selected={active}
            className={cn(
              "inline-flex h-9 items-center gap-1.5 rounded-full px-3 text-sm font-medium transition-colors",
              active
                ? "bg-accent text-foreground"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            <Icon className="size-4 shrink-0" aria-hidden />
            <span>{tab.label}</span>
            <NavBadgeChip href={tab.href} active={active} />
          </Link>
        );
      })}
    </nav>
  );
}

function NavBadgeChip({ href, active }: { href: string; active?: boolean }) {
  const count = useNavBadge(href);
  if (count <= 0) return null;
  return (
    <Badge
      variant={active ? "brand" : "pill"}
      className={cn(
        "ml-0.5 h-5 min-w-[20px] justify-center rounded-full px-1.5 font-mono text-[10px] font-semibold tabular-nums",
      )}
      aria-label={`${count} pending`}
    >
      {count > 99 ? "99+" : count}
    </Badge>
  );
}
